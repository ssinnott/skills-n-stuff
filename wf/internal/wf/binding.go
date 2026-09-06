package wf

// The task object: an identity, a history of runs, and the typed bindings
// those runs produced.
//
// This file is the contract. Everything that binds work to the world — a
// checkout, an agent session, a pull request, a produced document, the
// tracker row itself — is a Binding, and every binding a run produced
// carries that run's id in Via. See DESIGN-task.md for why this replaces
// the loose metadata keys it grew out of.
//
// Two rules shape the vocabulary:
//
//   - Names describe roles, never products. A session's runner is "pi" as a
//     *value*; a queue's backend is "kata" as a *value*. A second runner is
//     then a new value, not a parallel set of keys with parallel readers.
//   - Every kind is plural. "The current one" is a query over a list, not a
//     separate field, because a re-run that overwrites its predecessor's
//     checkout destroys the evidence an escalation was kept for.

import "time"

// Kind names what a binding points at.
type Kind string

const (
	// KindQueue is a row in a tracker. A task may have none: work can
	// start before it is filed.
	KindQueue Kind = "queue"
	// KindRepo is a repository the work happens in.
	KindRepo Kind = "repo"
	// KindWorkspace is an isolated checkout — a git worktree today.
	KindWorkspace Kind = "workspace"
	// KindSession is one agent session, bound at spawn so a crashed or
	// hung run is still attachable.
	KindSession Kind = "session"
	// KindPR is a pull request the work opened.
	KindPR Kind = "pr"
	// KindDoc is a document the work produced.
	KindDoc Kind = "doc"
	// KindIssue is an issue the work *filed* — a record of work moving
	// elsewhere, which is why it never counts as this task's evidence.
	KindIssue Kind = "issue"
	// KindReview is a review pane opened over this task.
	KindReview Kind = "review"
	// KindTask is another task: a NEXT follow-on, or its parent.
	KindTask Kind = "task"
)

// BindingState is one binding's own lifecycle — never the task's. A task's
// progress is WorkState (see task.go); these say whether the thing a
// binding points at is still there and still current.
type BindingState string

const (
	// BindingUnknown is the zero value: never checked, or not applicable.
	BindingUnknown BindingState = ""
	// BindingLive means the referent exists and is current.
	BindingLive BindingState = "live"
	// BindingDisposed means it was deliberately torn down (a worktree
	// removed after a clean close).
	BindingDisposed BindingState = "disposed"
	// BindingSuperseded means a later run replaced it. The referent may
	// still exist; it is simply no longer the one to look at.
	BindingSuperseded BindingState = "superseded"
	// BindingMerged applies to a pull request that landed.
	BindingMerged BindingState = "merged"
	// BindingClosed applies to a PR or issue closed without landing.
	BindingClosed BindingState = "closed"
	// BindingMissing means the referent was expected and is gone — a
	// worktree deleted behind wf's back. Distinct from Disposed, which wf
	// did on purpose.
	BindingMissing BindingState = "missing"
)

// Metadata keys carried in a Binding's Meta map. Kind-specific, flat, and
// all strings: a binding is a reference plus a lifecycle, never content.
const (
	// MetaBranch is the branch a workspace checked out. Recorded by the
	// run that created it rather than derived from the task's title,
	// which is a field a human is free to edit.
	MetaBranch = "branch"
	// MetaBase is the branch a workspace's diff is taken against.
	MetaBase = "base"
	// MetaRepo is the repository a workspace or PR belongs to.
	MetaRepo = "repo"
	// MetaRunner is which agent ran a session — "pi" is a value here.
	MetaRunner = "runner"
	// MetaBackend is which tracker a queue binding lives in — "kata" is a
	// value here.
	MetaBackend = "backend"
	// MetaSessionID is a session's own id, for display and the runner's
	// session browser. Ref holds the path, which is what reattaching uses.
	MetaSessionID = "session_id"
	// MetaCwd is the directory a session ran in.
	MetaCwd = "cwd"
	// MetaStore is where a document lives — "vault" is a value here.
	MetaStore = "store"
	// MetaPort is the port a review pane bound.
	MetaPort = "port"
	// MetaPID is the process id of a review pane.
	MetaPID = "pid"
	// MetaRelation is how a task binding relates: "parent" or "next".
	MetaRelation = "relation"
)

// Binding is one typed, stateful reference hanging off a task.
type Binding struct {
	Kind Kind `json:"kind"`
	// Ref is the thing itself: a path, a URL, a tracker id. Unique within
	// a kind for a given task.
	Ref string `json:"ref"`
	// Label is human-facing text — a PR title, a document title.
	Label string `json:"label,omitempty"`
	// State is the referent's lifecycle, refreshed rather than assumed.
	State BindingState `json:"state,omitempty"`
	// At is when this binding was recorded.
	At time.Time `json:"at"`
	// Via is the id of the run that produced it. Empty means the task's
	// own — an adopted PR, a note bound by hand — which is the honest
	// record rather than a missing one.
	Via string `json:"via,omitempty"`
	// Host is the actor that owns Ref, set only when the referent is
	// machine-local. A worktree path is meaningless on another machine,
	// and saying so beats handing over a path that will not resolve.
	Host string `json:"host,omitempty"`
	// Meta carries kind-specific detail. Flat and stringly-typed on
	// purpose: this is a reference, not a document.
	Meta map[string]string `json:"meta,omitempty"`
}

// Get reads one Meta value, tolerating a nil map.
func (b Binding) Get(key string) string {
	if b.Meta == nil {
		return ""
	}
	return b.Meta[key]
}

// IsLive reports whether the referent is current. Unknown counts as live:
// a binding nothing has checked yet should not be hidden, only unproven.
func (b Binding) IsLive() bool {
	return b.State == BindingLive || b.State == BindingUnknown
}

// Bindings is a task's whole set, newest last.
type Bindings []Binding

// ByKind returns every binding of a kind, in recorded order.
func (bs Bindings) ByKind(k Kind) Bindings {
	var out Bindings
	for _, b := range bs {
		if b.Kind == k {
			out = append(out, b)
		}
	}
	return out
}

// Live returns the bindings of a kind whose referent still stands.
func (bs Bindings) Live(k Kind) Bindings {
	var out Bindings
	for _, b := range bs.ByKind(k) {
		if b.IsLive() {
			out = append(out, b)
		}
	}
	return out
}

// Current returns the newest live binding of a kind — the one a caller
// asking for "the worktree" or "the session" means. This is the query that
// replaces the single-valued metadata keys: plural storage, singular read.
func (bs Bindings) Current(k Kind) (Binding, bool) {
	live := bs.Live(k)
	if len(live) == 0 {
		return Binding{}, false
	}
	newest := live[0]
	for _, b := range live[1:] {
		if !b.At.Before(newest.At) {
			newest = b
		}
	}
	return newest, true
}

// Refs returns the referents of a kind, live or not, in recorded order.
func (bs Bindings) Refs(k Kind) []string {
	var out []string
	for _, b := range bs.ByKind(k) {
		out = append(out, b.Ref)
	}
	return out
}

// From returns every binding a given run produced.
func (bs Bindings) From(runID string) Bindings {
	var out Bindings
	for _, b := range bs {
		if b.Via == runID {
			out = append(out, b)
		}
	}
	return out
}

// Upsert adds a binding, replacing any existing one with the same kind and
// ref. Recording the same PR twice is one binding with a refreshed state,
// not two rows.
func (bs Bindings) Upsert(b Binding) Bindings {
	for i, existing := range bs {
		if existing.Kind == b.Kind && existing.Ref == b.Ref {
			bs[i] = b
			return bs
		}
	}
	return append(bs, b)
}

// Supersede marks every live binding of a kind as replaced, except those
// produced by the named run. This is how a second run takes over a kind
// without destroying what the first one left: the old checkout stays on
// disk and stays findable, it just stops being current.
func (bs Bindings) Supersede(k Kind, exceptRun string) Bindings {
	for i, b := range bs {
		if b.Kind != k || !b.IsLive() {
			continue
		}
		if exceptRun != "" && b.Via == exceptRun {
			continue
		}
		bs[i].State = BindingSuperseded
	}
	return bs
}

// Run is one dispatch of a workflow against a task. Runs are the middle
// layer: a task holds runs, a run holds the bindings it produced, and that
// edge is what makes a workflow's output bind back to the task rather than
// into a flat pile.
type Run struct {
	ID string `json:"id"`
	// Workflow, Profile and Model are how this run was dispatched. They
	// live on the run rather than the task because a task re-run under a
	// different recipe or model has a history, not an overwritten field.
	Workflow string `json:"workflow,omitempty"`
	Profile  string `json:"profile,omitempty"`
	Model    string `json:"model,omitempty"`
	// Host is the actor that ran it.
	Host    string     `json:"host,omitempty"`
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended,omitempty"`
	// Outcome is how the run settled, reusing the session vocabulary.
	Outcome SessionOutcome `json:"outcome,omitempty"`
}

// Done reports whether the run has settled.
func (r Run) Done() bool { return r.Ended != nil }

// Record is wf's own task record: identity, runs, and bindings. The
// tracker still owns work state — title, priority, open/closed — and a
// Record never mirrors those; it holds what a tracker cannot, which is
// everything machine-local and everything with provenance.
type Record struct {
	// ID is wf's own durable id, minted by wf. A tracker row is a
	// KindQueue binding, not this.
	ID string `json:"id"`
	// Handle is the short human-facing ref.
	Handle   string    `json:"handle,omitempty"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated,omitempty"`
	Runs     []Run     `json:"runs,omitempty"`
	Bindings Bindings  `json:"bindings,omitempty"`
}

// Run returns one run by id.
func (r Record) Run(id string) (Run, bool) {
	for _, run := range r.Runs {
		if run.ID == id {
			return run, true
		}
	}
	return Run{}, false
}

// LatestRun returns the most recently started run.
func (r Record) LatestRun() (Run, bool) {
	if len(r.Runs) == 0 {
		return Run{}, false
	}
	newest := r.Runs[0]
	for _, run := range r.Runs[1:] {
		if !run.Started.Before(newest.Started) {
			newest = run
		}
	}
	return newest, true
}

// Produced returns the bindings a run created.
func (r Record) Produced(runID string) Bindings { return r.Bindings.From(runID) }

// QueueRef returns the tracker id this task is filed under, if it is.
func (r Record) QueueRef() (string, bool) {
	b, ok := r.Bindings.Current(KindQueue)
	if !ok {
		return "", false
	}
	return b.Ref, true
}
