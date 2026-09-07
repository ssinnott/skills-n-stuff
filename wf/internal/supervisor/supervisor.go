// Package supervisor runs the dispatch loop: lease, workspace, agent,
// outcomes, release. It holds no durable state of its own — everything it
// needs to resume after a crash is on the tracker or in the local ledger —
// so killing it mid-flight costs nothing but the in-flight run. See
// DESIGN.md and DESIGN-slim.md.
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
	// Store is wf's own task ledger: runs and machine-local bindings. Nil
	// disables it and the loop runs unchanged. See ledger.go.
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
// the ones the queue makes.
type Dispatch struct {
	// Workflow names the recipe to run, overriding the task's own metadata
	// and labels. Empty leaves selection exactly as it was.
	Workflow string
}

// RunOnce dispatches a single task. An empty ref takes the top claimable
// task off the ready queue.
func (s *Supervisor) RunOnce(ctx context.Context, ref string) (Result, error) {
	return s.RunOnceWith(ctx, ref, Dispatch{})
}

// RunOnceWith is RunOnce with the caller's own choices about the run: `wf
// run <ref> --workflow <name>`.
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
		// attempted covers the life of this call, so an escalated or failed
		// task (still open and claimable) is not retried forever.
		attempted = map[string]bool{}
		inFlight  = map[string]bool{}
		wg        sync.WaitGroup
		slots     = make(chan struct{}, max)
	)

	for {
		if ctx.Err() != nil {
			break
		}

		// More than a slot's worth: unclaimable ones are skipped.
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
			// Nothing claimable this pass: wait for in-flight work, which may
			// unblock dependents, and stop if it did not.
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

	// Highest priority first; a tracker's ready order (kata: newest first)
	// is its own business, but which task to hand an agent is wf's call.
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

// dispatchable reports whether wf should hand this task to an agent now: a
// task waiting on a human is not ours to retry.
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
	// From here on the lease is held; every exit path releases it.
	defer func() {
		if err := s.Queue.Release(ctx, task.ID); err != nil {
			s.logf("%s: release lease: %v", task.ShortID, err)
		}
	}()

	startedAt := time.Now().UTC()
	runID := newRunID(startedAt)
	s.beginRun(task, flow, runID, startedAt)
	// Every path out of here settles the run, so a dispatch that died on a
	// tracker write or broken checkout does not read as one still going.
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

	// Written before the run is waited on, so a crashed or hung run is still attachable.
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
		s.endRun(task, runID, wf.SessionFailed, time.Now().UTC())
		settled = true
		return Result{Task: task, TaskID: task.ID, Applied: applied, Session: binding.Path, Run: runID}, nil
	}

	outcomes := wf.ParseOutcomes(runResult.TranscriptTail)
	// IdempotencyKey is keyed on the session so a retry closes once, not twice.
	applied, err := wf.Apply(ctx, s.Queue, task, outcomes, wf.ApplyOptions{
		Transcript:     runResult.TranscriptTail,
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

	// A worktree is disposed only on a clean close; an escalated run leaves
	// its checkout on disk as evidence.
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
// than guessing when the task names one we do not have. A workflow named at
// the call site wins over the task's metadata and labels; an unloaded name
// given there is a plain error, since a human is already at the terminal,
// while an unloaded name from task metadata escalates to the tracker, since
// that dispatch may be unattended.
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
	// Cosmetic; the lease is what matters.
	if err := s.Queue.Claim(ctx, task.ID, s.actor()); err != nil {
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
