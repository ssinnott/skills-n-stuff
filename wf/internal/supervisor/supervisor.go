// Package supervisor runs one dispatch: lease, workspace, agent, outcomes,
// release. Every run is a human's choice — `wf run <ref>` — so there is no
// loop and no queue-picking here. It holds no durable state of its own;
// everything it needs to resume after a crash is on the tracker or in the
// local ledger, so killing it mid-flight costs nothing but the in-flight
// run. See DESIGN.md and DESIGN-slim.md.
package supervisor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
)

// Supervisor dispatches a task to an agent.
type Supervisor struct {
	Queue     wf.Queue
	Runner    wf.Runner
	Workflows *workflow.Set
	Config    *config.Config
	// Store is wf's own task ledger: runs and machine-local bindings. Nil
	// disables it and the run goes ahead unchanged. See ledger.go.
	Store store.Store
	// Workspaces builds a provider for a workflow. Injected so a run can
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
	Task    wf.Task
	Applied wf.ApplyResult
}

// Dispatch carries the choices a caller makes about one run, as opposed to
// the ones the task's own metadata and labels make.
type Dispatch struct {
	// Workflow names the recipe to run, overriding the task's own metadata
	// and labels. Empty leaves selection exactly as it was.
	Workflow string
}

// RunOnce dispatches one task by ref. A task another live wf instance holds
// is refused; a stale lease is taken over.
func (s *Supervisor) RunOnce(ctx context.Context, ref string, opts Dispatch) (Result, error) {
	if ref == "" {
		return Result{}, fmt.Errorf("no task named")
	}
	task, err := s.Queue.Get(ctx, ref)
	if err != nil {
		return Result{}, err
	}
	if !wf.Claimable(task.Meta[wf.LeaseKey], s.actor(), time.Now()) {
		lease, _ := wf.ParseLease(task.Meta[wf.LeaseKey])
		return Result{}, fmt.Errorf("%s is leased: %s", task.ShortID, lease.Describe(time.Now()))
	}
	return s.dispatch(ctx, task, opts)
}

// dispatch runs one task end to end.
func (s *Supervisor) dispatch(ctx context.Context, task wf.Task, opts Dispatch) (Result, error) {
	flow, err := s.resolveWorkflow(task, opts.Workflow)
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
		space, err = s.Workspaces(flow).Create(ctx, task, s.previousBranch(task))
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
	s.recordSession(task, runID, handle.SessionID(), handle.SessionPath(), time.Now().UTC())

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
		return Result{Task: task, Applied: applied}, nil
	}

	outcomes := wf.ParseOutcomes(runResult.TranscriptTail)
	applied, err := wf.Apply(ctx, s.Queue, task, outcomes, wf.ApplyOptions{
		Transcript: runResult.TranscriptTail,
		Bind:       s.bindOptions(flow, workspaceDir),
	})
	if err != nil {
		return Result{}, err
	}

	outcome := wf.SessionDone
	if applied.Escalated {
		outcome = wf.SessionEscalated
	}

	// A worktree is disposed only when the run completed; an escalated run
	// leaves its checkout on disk as evidence. The branch survives either
	// way, for the next run on the task.
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

	return Result{Task: task, Applied: applied}, nil
}

// resolveWorkflow selects the recipe for a run: the one named at the call
// site, else the one the task's metadata or labels select. A human is at
// the terminal for every run, so nothing here escalates — an unloaded or
// unselected workflow is a plain error, and running the wrong recipe
// quietly under some default would be worse than not running.
func (s *Supervisor) resolveWorkflow(task wf.Task, named string) (workflow.Workflow, error) {
	if s.Workflows == nil {
		return workflow.Workflow{}, fmt.Errorf("no workflows loaded")
	}
	if named != "" {
		if flow, ok := s.Workflows.Get(named); ok {
			return flow, nil
		}
		return workflow.Workflow{}, fmt.Errorf("no workflow named %q is loaded", named)
	}
	flow, ok := s.Workflows.Select(task)
	if ok {
		return flow, nil
	}
	if flow.Name != "" {
		return workflow.Workflow{}, fmt.Errorf("%s names workflow %q, which is not loaded", task.ShortID, flow.Name)
	}
	return workflow.Workflow{}, fmt.Errorf("no workflow selected for %s: pass --workflow, or label the task", task.ShortID)
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
	// Cosmetic, for humans reading the tracker; the lease is what matters.
	if err := s.Queue.Claim(ctx, task.ID, s.actor()); err != nil {
		s.logf("%s: claim: %v", task.ShortID, err)
	}
	return nil
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
