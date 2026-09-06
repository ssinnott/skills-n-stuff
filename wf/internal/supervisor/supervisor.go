// Package supervisor runs the dispatch loop: lease, workspace, agent,
// outcomes, release.
//
// The loop holds no durable state of its own. Everything it needs to resume
// after a crash is either on the tracker — the lease, the state — or in the
// local ledger — the workspace and session bindings — which is why killing
// the supervisor mid-flight costs nothing but the in-flight run, and why a
// second supervisor on another machine can pick up work this one abandoned
// (on that machine's own workspace and session, never the first one's).
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
)

// ErrNothingReady is returned by RunOnce when the queue has no actionable,
// claimable work.
var ErrNothingReady = errors.New("nothing ready")

// Supervisor dispatches tasks to agents.
type Supervisor struct {
	Queue     wf.Queue
	Runner    wf.Runner
	Workflows *workflow.Set
	Config    *config.Config
	// Store is wf's own task ledger: runs, and the machine-local bindings
	// each run produced — a workspace checkout, an agent session. Nil
	// disables it, and the loop runs unchanged — nothing here takes a
	// lifecycle decision from the ledger, so a missing one costs history
	// and never work. See ledger.go for what is written and why shareable
	// facts are not written here at all.
	Store store.Store
	// Workspaces builds a provider for a workflow. Injected so the loop can
	// be tested without git.
	Workspaces func(w workflow.Workflow) wf.WorkspaceProvider
	// Log receives progress lines. Nil discards them.
	Log func(format string, args ...any)
}

func (s *Supervisor) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}

func (s *Supervisor) ttl() time.Duration {
	if s.Config != nil && s.Config.LeaseTTLSeconds > 0 {
		return time.Duration(s.Config.LeaseTTLSeconds) * time.Second
	}
	return wf.DefaultTTL
}

func (s *Supervisor) actor() string {
	if s.Config != nil && s.Config.Actor != "" {
		return s.Config.Actor
	}
	return "wf"
}

// Result reports one dispatched task.
type Result struct {
	Task wf.Task
	// TaskID is the tracker's own id — the same as Task.ID — repeated here
	// because it is what `wf show` and the ledger key on.
	TaskID  string
	Applied wf.ApplyResult
	Session string
	// Run is the id this dispatch was recorded under, and the Via every
	// binding it produced carries.
	Run string
}

// Dispatch carries the choices a caller makes about one run, as opposed to
// the ones the queue makes. There is exactly one today and it is the whole
// reason the type exists: naming a workflow is an *override* of implicit
// selection, not a replacement for it, so it belongs in an optional struct
// rather than as a second positional argument every caller has to answer.
type Dispatch struct {
	// Workflow names the recipe to run, overriding the task's own metadata
	// and its labels. Empty leaves selection exactly as it was, which is
	// what queue-driven dispatch depends on: a queue that routes by label
	// must keep working with nobody naming anything.
	Workflow string
}

// RunOnce dispatches a single task. An empty ref takes the top claimable
// task off the ready queue.
func (s *Supervisor) RunOnce(ctx context.Context, ref string) (Result, error) {
	return s.RunOnceWith(ctx, ref, Dispatch{})
}

// RunOnceWith is RunOnce with the caller's own choices about the run.
//
// This is `wf run <ref> --workflow <name>`: the invocation surface every
// client wants — a button on a task note, a slash command in the pi
// extension, a bare shell line — all shelling out to the same command,
// because the CLI is the only orchestration surface and the clients hold no
// state of their own.
func (s *Supervisor) RunOnceWith(ctx context.Context, ref string, opts Dispatch) (Result, error) {
	task, err := s.pick(ctx, ref)
	if err != nil {
		return Result{}, err
	}
	return s.dispatch(ctx, task, opts)
}

// Run keeps dispatching until the queue is empty or the context ends,
// holding at most max runs in flight.
func (s *Supervisor) Run(ctx context.Context, max int) ([]Result, error) {
	if max <= 0 {
		max = 1
	}

	var (
		mu      sync.Mutex
		results []Result
		// attempted covers the life of this call. A task that escalated or
		// failed stays open and claimable, so without this the loop would
		// pick it straight back up and retry it forever.
		attempted = map[string]bool{}
		inFlight  = map[string]bool{}
		wg        sync.WaitGroup
		slots     = make(chan struct{}, max)
	)

	for {
		if ctx.Err() != nil {
			break
		}

		// Ask for more than a slot's worth: tasks we cannot claim are
		// skipped, and a page of one would stall on the first of them.
		candidates, err := s.Queue.Ready(ctx, max*3)
		if err != nil {
			wg.Wait()
			return results, err
		}

		dispatched := 0
		for _, task := range candidates {
			if ctx.Err() != nil {
				break
			}

			mu.Lock()
			seen := inFlight[task.ID] || attempted[task.ID]
			mu.Unlock()
			if seen || !dispatchable(task, s.actor()) {
				continue
			}

			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
			}
			if ctx.Err() != nil {
				break
			}

			mu.Lock()
			inFlight[task.ID] = true
			attempted[task.ID] = true
			mu.Unlock()
			dispatched++
			wg.Add(1)

			go func(t wf.Task) {
				defer wg.Done()
				defer func() {
					mu.Lock()
					delete(inFlight, t.ID)
					mu.Unlock()
					<-slots
				}()

				result, err := s.dispatch(ctx, t, Dispatch{})
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					s.logf("%s failed: %v", t.ShortID, err)
					return
				}
				results = append(results, result)
			}(task)
		}

		if dispatched == 0 {
			// Nothing claimable this pass. Wait for in-flight work to
			// finish — it may unblock dependents — and stop if it did not.
			wg.Wait()
			mu.Lock()
			done := len(inFlight) == 0
			mu.Unlock()
			if done {
				break
			}
		}
	}

	wg.Wait()
	return results, ctx.Err()
}

// pick resolves the task to dispatch, honoring leases.
func (s *Supervisor) pick(ctx context.Context, ref string) (wf.Task, error) {
	if ref != "" {
		task, err := s.Queue.Get(ctx, ref)
		if err != nil {
			return wf.Task{}, err
		}
		if !wf.Claimable(task.Meta[wf.LeaseKey], s.actor(), time.Now()) {
			lease, _ := wf.ParseLease(task.Meta[wf.LeaseKey])
			return wf.Task{}, fmt.Errorf("%s is leased: %s", task.ShortID, lease.Describe(time.Now()))
		}
		return task, nil
	}

	candidates, err := s.Queue.Ready(ctx, 20)
	if err != nil {
		return wf.Task{}, err
	}

	// Highest priority first. A tracker's ready order is its own business —
	// kata returns newest first — but which ready task to hand an agent is
	// wf's decision, and priority is what the number is for.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Priority < candidates[j].Priority
	})

	for _, task := range candidates {
		if dispatchable(task, s.actor()) {
			return task, nil
		}
	}
	return wf.Task{}, ErrNothingReady
}

// dispatchable reports whether wf should hand this task to an agent now.
// A task waiting on a human is not ours to retry: it stays in the queue and
// in the escalation list until someone clears the flag.
func dispatchable(task wf.Task, actor string) bool {
	if attention, ok := task.Meta[wf.AttentionKey].(string); ok && attention != "" {
		return false
	}
	return wf.Claimable(task.Meta[wf.LeaseKey], actor, time.Now())
}

// dispatch runs one task end to end.
func (s *Supervisor) dispatch(ctx context.Context, task wf.Task, opts Dispatch) (Result, error) {
	flow, err := s.resolveWorkflow(ctx, task, opts.Workflow)
	if err != nil {
		return Result{}, err
	}
	s.logf("%s → %s: %s", task.ShortID, flow.Name, task.Title)

	if err := s.acquire(ctx, task); err != nil {
		return Result{}, err
	}
	// From here on the lease is held, so every exit path must release it.
	defer func() {
		if err := s.Queue.Release(ctx, task.ID); err != nil {
			s.logf("%s: release lease: %v", task.ShortID, err)
		}
	}()

	// The run is recorded before it has produced anything, which is the same
	// argument the spawn-time session binding rests on: a run written down
	// when it finishes is exactly the run that never gets written down.
	startedAt := time.Now().UTC()
	runID := newRunID(startedAt)
	// The record is keyed by the task's own id, which every dispatch
	// already carries — there is nothing to look up or mint.
	s.beginRun(task, flow, runID, startedAt)
	// Every path out of here settles the run. A dispatch that died on a
	// tracker write or a broken checkout is still a run that happened, and a
	// row left open forever would read as one still going.
	settled := false
	defer func() {
		if !settled {
			s.endRun(task, runID, wf.SessionFailed, time.Now().UTC())
		}
	}()

	workspaceDir := ""
	var space wf.Workspace
	if flow.NeedsWorkspace() {
		if s.Workspaces == nil {
			return Result{}, fmt.Errorf("no workspace provider configured")
		}
		space, err = s.Workspaces(flow).Create(ctx, task)
		if err != nil {
			return Result{}, err
		}
		workspaceDir = space.Path()
		s.recordWorkspace(task, runID, space, time.Now().UTC())
	}
	if workspaceDir == "" {
		workspaceDir = s.vaultOrCwd()
	}

	if err := wf.SetState(ctx, s.Queue, task.ID, wf.StateRunning); err != nil {
		return Result{}, err
	}

	handle, err := s.Runner.Start(ctx, wf.RunOptions{
		Cwd:        workspaceDir,
		Prompt:     s.prompt(flow, task, workspaceDir),
		TaskRef:    task.ShortID,
		ProfileDir: s.Config.ProfileDir(flow.Profile),
		Model:      s.Config.ResolveModel(flow.Model),
	})
	if err != nil {
		return Result{}, fmt.Errorf("start agent for %s: %w", task.ShortID, err)
	}

	// The binding is written before the run is waited on, so a crashed or
	// hung run is still attachable — those are the runs worth reading.
	binding := wf.SessionBinding{
		ID:      handle.SessionID(),
		Path:    handle.SessionPath(),
		Cwd:     handle.Cwd(),
		Started: time.Now().UTC(),
	}
	s.recordSession(task, runID, binding)

	stopRenewal := s.renewLease(ctx, task)
	runResult, runErr := handle.Wait(ctx)
	stopRenewal()

	if runErr != nil {
		applied, escErr := wf.Escalate(ctx, s.Queue, task, "the agent process failed: "+runErr.Error(), runResult.TranscriptTail)
		if escErr != nil {
			return Result{}, escErr
		}
		s.keepWorkspace(space)
		// The checkout stays live and stays bound: a crashed agent is the
		// case the whole record exists to make inspectable.
		s.endRun(task, runID, wf.SessionFailed, time.Now().UTC())
		settled = true
		return Result{Task: task, TaskID: task.ID, Applied: applied, Session: binding.Path, Run: runID}, nil
	}

	outcomes := wf.ParseOutcomes(runResult.TranscriptTail)
	applied, err := wf.Apply(ctx, s.Queue, task, outcomes, wf.ApplyOptions{
		Transcript: runResult.TranscriptTail,
		// Keyed on the session so a retried run closes once, not twice.
		IdempotencyKey: "wf-close-" + binding.ID,
		Bind:           s.bindOptions(flow, workspaceDir),
	})
	if err != nil {
		return Result{}, err
	}

	outcome := wf.SessionDone
	if applied.Escalated {
		outcome = wf.SessionEscalated
	}

	// A worktree is disposed only when the run closed cleanly. An escalated
	// run leaves its checkout on disk: that is the evidence a human needs.
	if applied.Escalated {
		s.keepWorkspace(space)
	} else if space != nil {
		if err := space.Dispose(ctx); err != nil {
			s.logf("%s: dispose workspace: %v", task.ShortID, err)
		} else {
			s.disposedWorkspace(task, runID, space)
		}
	}

	s.endRun(task, runID, outcome, time.Now().UTC())
	settled = true

	return Result{Task: task, TaskID: task.ID, Applied: applied, Session: binding.Path, Run: runID}, nil
}

// resolveWorkflow selects the canned workflow for a task, escalating rather
// than guessing when the task names one we do not have.
//
// A workflow named at the call site wins over the task's metadata and its
// labels, and a name that is not loaded is a plain error rather than an
// escalation. The two failures look alike and are not: a task carrying
// `wf.workflow: x` for a recipe nobody installed is a configuration problem
// discovered by a dispatch that may be unattended, so the tracker is the
// only place to say so. A name someone just typed already has a human at the
// other end of the terminal, and filing a comment about their typo is noise
// on the task.
func (s *Supervisor) resolveWorkflow(ctx context.Context, task wf.Task, named string) (workflow.Workflow, error) {
	if named != "" {
		if s.Workflows != nil {
			if flow, ok := s.Workflows.Get(named); ok {
				return flow, nil
			}
		}
		return workflow.Workflow{}, fmt.Errorf("no workflow named %q is loaded", named)
	}
	if s.Workflows == nil {
		return defaultWorkflow(), nil
	}
	flow, ok := s.Workflows.Select(task)
	if ok {
		return flow, nil
	}
	if flow.Name != "" {
		// The task named a workflow that is not loaded. Running it under a
		// default would silently do the wrong work.
		_, err := wf.Escalate(ctx, s.Queue, task,
			fmt.Sprintf("task names workflow %q, which is not loaded", flow.Name), "")
		if err != nil {
			return workflow.Workflow{}, err
		}
		return workflow.Workflow{}, fmt.Errorf("%s names unknown workflow %q", task.ShortID, flow.Name)
	}
	return defaultWorkflow(), nil
}

// defaultWorkflow is what a task with no workflow gets: the task text, the
// outcome protocol, and a worktree.
func defaultWorkflow() workflow.Workflow {
	return workflow.Workflow{
		Name:      "default",
		Workspace: "worktree",
		Prompt:    "{{TASK_TITLE}}\n\n{{TASK_BODY}}",
	}
}

func (s *Supervisor) prompt(flow workflow.Workflow, task wf.Task, workspaceDir string) string {
	return wf.SeedPreamble(task.ShortID, task.Title) + "\n\n---\n\n" + flow.Render(task, workspaceDir)
}

func (s *Supervisor) bindOptions(flow workflow.Workflow, workspaceDir string) wf.BindOptions {
	if !flow.BindDocs || s.Config == nil || s.Config.Vault == "" {
		return wf.BindOptions{}
	}
	return wf.BindOptions{
		Vault:        s.Config.Vault,
		VaultDir:     flow.VaultDir,
		WorkspaceDir: workspaceDir,
	}
}

func (s *Supervisor) vaultOrCwd() string {
	if s.Config != nil && s.Config.Vault != "" {
		return s.Config.Vault
	}
	return "."
}

// acquire writes the lease before claiming, so the record that decides
// liveness exists before the record that advertises ownership.
func (s *Supervisor) acquire(ctx context.Context, task wf.Task) error {
	lease := wf.NewLease(s.actor(), s.ttl(), time.Now())
	encoded, err := lease.Encode()
	if err != nil {
		return err
	}
	if err := s.Queue.SetMeta(ctx, task.ID, wf.LeaseKey, encoded, wf.SetMetaOptions{JSON: true}); err != nil {
		return fmt.Errorf("write lease on %s: %w", task.ShortID, err)
	}
	if err := s.Queue.Claim(ctx, task.ID, s.actor()); err != nil {
		// Ownership is cosmetic; the lease is what matters. A tracker that
		// refuses the claim must not stop the run.
		s.logf("%s: claim: %v", task.ShortID, err)
	}
	return wf.SetState(ctx, s.Queue, task.ID, wf.StateClaimed)
}

// renewLease keeps the lease live while the agent works, so a long run is
// not reclaimed for being slow. It returns a stop function.
func (s *Supervisor) renewLease(ctx context.Context, task wf.Task) func() {
	interval := s.ttl() / 3
	if interval < time.Second {
		interval = time.Second
	}

	done := make(chan struct{})
	var once sync.Once

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				lease := wf.NewLease(s.actor(), s.ttl(), time.Now())
				encoded, err := lease.Encode()
				if err != nil {
					continue
				}
				if err := s.Queue.SetMeta(ctx, task.ID, wf.LeaseKey, encoded, wf.SetMetaOptions{JSON: true}); err != nil {
					s.logf("%s: renew lease: %v", task.ShortID, err)
				}
			}
		}
	}()

	return func() { once.Do(func() { close(done) }) }
}

func (s *Supervisor) keepWorkspace(space wf.Workspace) {
	if space == nil {
		return
	}
	if keeper, ok := space.(interface{ Keep() }); ok {
		keeper.Keep()
	}
}
