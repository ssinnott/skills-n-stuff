// Package wf holds the core domain: the task model, the outcome protocol,
// leases, and the three seams (queue, runner, workspace). See DESIGN.md.
package wf

import "context"

// WorkState is wf's own vocabulary; backends map it, none own it. A task
// with no state has never been run.
type WorkState string

const (
	// StateRunning is set for the life of one run.
	StateRunning WorkState = "running"
	// StateReview means the last run completed and a human decides what
	// happens next: another workflow, or `wf close`.
	StateReview WorkState = "review"
	// StateNeedsHuman means the last run failed or ended without DONE.
	StateNeedsHuman WorkState = "needs-human"
	// StateDone is set when a human closes the task.
	StateDone WorkState = "done"
)

// Task is one unit of work, normalized across backends. It is long-lived:
// runs under any number of workflows happen against it, and a human closes
// it when the work is done.
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
}

// CloseResult is the evidence a task closes with.
type CloseResult struct {
	Message string
	PRs     []string
	// Docs are vault-relative paths of produced documents.
	Docs []string
}

// HasEvidence reports whether the task's work left a trace; a close without
// one is refused.
func (r CloseResult) HasEvidence() bool {
	return len(r.PRs) > 0 || len(r.Docs) > 0
}

// CreateInput describes a task to file.
type CreateInput struct {
	Title          string
	Body           string
	RelatedTo      string
	IdempotencyKey string
}

// SetMetaOptions carries the per-write flags a backend may support.
type SetMetaOptions struct {
	// JSON marks the value as raw JSON rather than a plain string.
	JSON bool
}

// Queue is the tracker seam: the verbs the run loop and `wf close` need,
// plus metadata access.
type Queue interface {
	// Ready returns open, unblocked, actionable work.
	Ready(ctx context.Context, limit int) ([]Task, error)
	Get(ctx context.Context, ref string) (Task, error)
	Claim(ctx context.Context, ref, actor string) error
	Release(ctx context.Context, ref string) error
	Comment(ctx context.Context, ref, body string) error
	Close(ctx context.Context, ref string, result CloseResult) error
	Create(ctx context.Context, in CreateInput) (Task, error)
	SetMeta(ctx context.Context, ref, key, value string, opts SetMetaOptions) error
	UnsetMeta(ctx context.Context, ref, key string) error
}

// Workspace is an isolated checkout for one run: one worktree per task.
// Branch is on the interface because a run has to *record* it, not
// recompute it later from a title a human is free to edit.
type Workspace interface {
	Path() string
	// Branch is the ref the checkout is on; empty for a provider with none.
	Branch() string
	Dispose(ctx context.Context) error
}

// WorkspaceProvider builds workspaces. branch names the branch an earlier
// run on the task left behind; a provider reuses it when it still exists,
// so a later run picks up where the last one pushed, and starts fresh
// otherwise.
type WorkspaceProvider interface {
	Create(ctx context.Context, task Task, branch string) (Workspace, error)
}

// RunOptions configures a single agent run.
type RunOptions struct {
	Cwd    string
	Prompt string
	// TaskRef names the session file on disk.
	TaskRef string
	// ProfileDir becomes PI_CODING_AGENT_DIR, selecting the worker's skills.
	ProfileDir string
	// Model becomes pi's --model value; empty runs pi's own default.
	Model string
}

// RunResult is what a settled run reports back. A non-zero exit is not an
// error: the agent may still have reported outcomes worth applying, and one
// that did not escalates for having no DONE.
type RunResult struct {
	// TranscriptTail is trailing output, scanned for outcome verbs.
	TranscriptTail string
}

// RunHandle is a live run; SessionPath is recorded before the agent produces anything.
type RunHandle interface {
	SessionID() string
	SessionPath() string
	Wait(ctx context.Context) (RunResult, error)
}

// Runner is the agent seam.
type Runner interface {
	Start(ctx context.Context, opts RunOptions) (RunHandle, error)
}
