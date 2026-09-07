package wf

// The task object: an identity, a history of runs, and the typed bindings
// those runs produced. See DESIGN-task.md.

import "time"

// Kind names what a binding points at.
type Kind string

const (
	// KindRepo is a repository the work happens in.
	KindRepo Kind = "repo"
	// KindWorkspace is an isolated checkout — a git worktree today.
	KindWorkspace Kind = "workspace"
	// KindSession is one agent session, bound at spawn for attach-ability.
	KindSession Kind = "session"
	// KindPR is a pull request the work opened or worked on.
	KindPR Kind = "pr"
	// KindDoc is a document the work produced, vault-relative.
	KindDoc Kind = "doc"
	// KindIssue is an issue the work *filed*; it never counts as this task's evidence.
	KindIssue Kind = "issue"
)

// BindingState is one binding's own lifecycle — never the task's (WorkState).
type BindingState string

const (
	// BindingUnknown is the zero value: never checked, or not applicable.
	BindingUnknown BindingState = ""
	// BindingLive means the referent exists and is current.
	BindingLive BindingState = "live"
	// BindingDisposed means it was deliberately torn down.
	BindingDisposed BindingState = "disposed"
	// BindingMissing means the referent is unexpectedly gone, unlike Disposed.
	BindingMissing BindingState = "missing"
)

// Metadata keys carried in a Binding's Meta map: kind-specific, flat strings.
const (
	// MetaBranch is the branch a workspace checked out, recorded by the run.
	MetaBranch = "branch"
	// MetaSessionID is a session's own id, for display; Ref holds the path.
	MetaSessionID = "session_id"
)

// Binding is one typed, stateful reference hanging off a task.
type Binding struct {
	Kind Kind `json:"kind"`
	// Ref is the thing itself, unique within a kind for a given task.
	Ref string `json:"ref"`
	// Label is human-facing text: an issue's title, say.
	Label string `json:"label,omitempty"`
	// State is the referent's lifecycle, refreshed rather than assumed.
	State BindingState `json:"state,omitempty"`
	// At is when this binding was recorded.
	At time.Time `json:"at"`
	// Via is the id of the run that produced it; empty means the task's own.
	Via string `json:"via,omitempty"`
	// Meta carries kind-specific detail, flat and stringly-typed.
	Meta map[string]string `json:"meta,omitempty"`
}

// Get reads one Meta value, tolerating a nil map.
func (b Binding) Get(key string) string {
	if b.Meta == nil {
		return ""
	}
	return b.Meta[key]
}

// IsLive reports whether the referent is current. Unknown counts as live.
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

// Current returns the newest live binding of a kind. Ties go to the last
// recorded: bindings recovered from flat metadata share a zero timestamp,
// so every one of them ties and Current returns whichever was read last.
// That is right for a worktree (a re-run's checkout is the one you want)
// and wrong for pull requests, where a flat array is in *report* order and
// the first is the one the run led with — a caller that wants report order
// should use Live, not Current.
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

// Last returns the most recently recorded binding of a kind, live or not.
func (bs Bindings) Last(k Kind) (Binding, bool) {
	all := bs.ByKind(k)
	if len(all) == 0 {
		return Binding{}, false
	}
	return all[len(all)-1], true
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

// Upsert adds a binding, replacing any existing one with the same kind and ref.
func (bs Bindings) Upsert(b Binding) Bindings {
	for i, existing := range bs {
		if existing.Kind == b.Kind && existing.Ref == b.Ref {
			bs[i] = b
			return bs
		}
	}
	return append(bs, b)
}

// Run is one workflow dispatched once against a task. See DESIGN-task.md.
type Run struct {
	ID string `json:"id"`
	// Workflow, Profile and Model are how this run was dispatched.
	Workflow string     `json:"workflow,omitempty"`
	Profile  string     `json:"profile,omitempty"`
	Model    string     `json:"model,omitempty"`
	Started  time.Time  `json:"started"`
	Ended    *time.Time `json:"ended,omitempty"`
	// Outcome is how the run settled.
	Outcome SessionOutcome `json:"outcome,omitempty"`
}

// Done reports whether the run has settled.
func (r Run) Done() bool { return r.Ended != nil }

// Record is wf's own ledger record: runs and machine-local bindings, keyed
// by the tracker's own id. See DESIGN-slim.md. It never mirrors work state.
type Record struct {
	// ID is the tracker's own id (a ULID on kata).
	ID       string    `json:"id"`
	Updated  time.Time `json:"updated,omitempty"`
	Runs     []Run     `json:"runs,omitempty"`
	Bindings Bindings  `json:"bindings,omitempty"`
}
