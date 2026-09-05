// Package wf holds the core domain: the task model, the outcome protocol,
// leases, session binding, and the three seams (queue, runner, workspace).
//
// Two things deliberately do NOT appear on the Queue interface, because no
// tracker in scope implements them and pushing them down means writing them
// once per adapter: leases (see lease.go) and the work-state vocabulary
// below. Both live here and are stored through a backend as opaque metadata.
package wf

import "context"

// WorkState is wf's own vocabulary. Backends map it; none of them own it.
type WorkState string

const (
	StateReady      WorkState = "ready"
	StateClaimed    WorkState = "claimed"
	StateRunning    WorkState = "running"
	StateReview     WorkState = "review"
	StateBlocked    WorkState = "blocked"
	StateNeedsHuman WorkState = "needs-human"
	StateDone       WorkState = "done"
)

// Task is one unit of queued work, normalized across backends.
type Task struct {
	// ID is the durable ref, surviving renames and moves (a ULID on kata).
	ID string
	// ShortID is the human-facing ref, for display and deep links.
	ShortID  string
	Title    string
	Body     string
	Priority int
	Labels   []string
	Owner    string
	Meta     map[string]any
	// Rev is an opaque revision for optimistic concurrency, when the
	// backend offers one.
	Rev string
}

// CloseResult is the evidence a task closes with.
type CloseResult struct {
	Message string
	PRs     []string
	Commits []string
	// Docs are paths of produced documents, recorded as review evidence.
	Docs  []string
	Tests []string
}

// HasEvidence reports whether the run left any trace of the work. A close
// without one is refused — see Apply.
func (r CloseResult) HasEvidence() bool {
	return len(r.PRs) > 0 || len(r.Commits) > 0 || len(r.Docs) > 0 || len(r.Tests) > 0
}

// CreateInput describes a task to file, including the links that make
// NEXT and ISSUE outcomes schedulable rather than merely recorded.
type CreateInput struct {
	Title          string
	Body           string
	Labels         []string
	Priority       int
	RelatedTo      string
	BlockedBy      string
	Meta           map[string]string
	IdempotencyKey string
}

// SetMetaOptions carries the per-write flags a backend may support.
type SetMetaOptions struct {
	// JSON marks the value as raw JSON rather than a plain string.
	JSON bool
	// IfMatch is an optimistic concurrency guard, where available.
	IfMatch string
}

// Queue is the tracker seam. Seven verbs plus metadata access is the whole
// contract, which is only possible because the outcome protocol defines
// what applying a result means independent of any tracker.
type Queue interface {
	Name() string
	// Ready returns open, unblocked, actionable work — the backend's own
	// definition of ready, which wf trusts rather than recomputing.
	Ready(ctx context.Context, limit int) ([]Task, error)
	Get(ctx context.Context, ref string) (Task, error)
	Claim(ctx context.Context, ref, actor string) error
	Release(ctx context.Context, ref string) error
	Comment(ctx context.Context, ref, body string) error
	Close(ctx context.Context, ref string, result CloseResult, idempotencyKey string) error
	Create(ctx context.Context, in CreateInput) (Task, error)
	SetMeta(ctx context.Context, ref, key, value string, opts SetMetaOptions) error
	UnsetMeta(ctx context.Context, ref, key string) error
	GetMeta(ctx context.Context, ref string) (map[string]any, error)
}

// Workspace is an isolated checkout for one task. One worktree per task:
// two agents in one checkout is the failure that costs an afternoon.
type Workspace interface {
	Path() string
	Dispose(ctx context.Context) error
}

// WorkspaceProvider builds workspaces. Worktree is the only implementation
// that ships; docker and ssh are why this is an interface.
type WorkspaceProvider interface {
	Name() string
	Create(ctx context.Context, task Task) (Workspace, error)
}

// RunOptions configures a single agent run.
type RunOptions struct {
	Cwd    string
	Prompt string
	// TaskRef names the session file on disk, so a directory of sessions
	// is readable without consulting the tracker.
	TaskRef string
	// ProfileDir becomes PI_CODING_AGENT_DIR, selecting the worker's
	// package and skill set.
	ProfileDir string
}

// RunResult is what a settled run reports back.
type RunResult struct {
	OK       bool
	ExitCode int
	// TranscriptTail is trailing output, scanned for outcome verbs.
	TranscriptTail string
}

// RunHandle is a live run. SessionPath is the field that matters most:
// it is what `wf attach` reopens, and it is recorded before the agent
// produces anything.
type RunHandle interface {
	SessionID() string
	SessionPath() string
	Cwd() string
	Wait(ctx context.Context) (RunResult, error)
	Abort() error
}

// Runner is the agent seam. pi is the only implementation that ships.
type Runner interface {
	Name() string
	Start(ctx context.Context, opts RunOptions) (RunHandle, error)
}
