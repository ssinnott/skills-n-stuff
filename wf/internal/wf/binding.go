package wf

// The task object: an identity, a history of runs, and the typed bindings those runs produced. See DESIGN-task.md.

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
	// KindPR is a pull request the work opened.
	KindPR Kind = "pr"
	// KindDoc is a document the work produced.
	KindDoc Kind = "doc"
	// KindIssue is an issue the work *filed*; it never counts as this task's evidence.
	KindIssue Kind = "issue"
	// KindTask is another task: a NEXT follow-on, or its parent.
	KindTask Kind = "task"
)

// MachineLocal reports whether a kind's Ref only resolves on the recording host.
func (k Kind) MachineLocal() bool {
	switch k {
	case KindWorkspace, KindSession:
		return true
	}
	return false
}

// MachineLocal reports whether this binding's Ref only resolves on this host; prefer it over Kind.MachineLocal since a bound document is vault-relative.
func (b Binding) MachineLocal() bool {
	if b.Kind == KindDoc {
		return b.Get(MetaStore) == ""
	}
	return b.Kind.MachineLocal()
}

// BindingState is one binding's own lifecycle — never the task's (WorkState).
type BindingState string

const (
	// BindingUnknown is the zero value: never checked, or not applicable.
	BindingUnknown BindingState = ""
	// BindingLive means the referent exists and is current.
	BindingLive BindingState = "live"
	// BindingDisposed means it was deliberately torn down.
	BindingDisposed BindingState = "disposed"
	// BindingSuperseded means a later run replaced it; the referent may still exist.
	BindingSuperseded BindingState = "superseded"
	// BindingMerged applies to a pull request that landed.
	BindingMerged BindingState = "merged"
	// BindingClosed applies to a PR or issue closed without landing.
	BindingClosed BindingState = "closed"
	// BindingMissing means the referent is unexpectedly gone, unlike Disposed.
	BindingMissing BindingState = "missing"
)

// Metadata keys carried in a Binding's Meta map: kind-specific, flat strings.
const (
	// MetaBranch is the branch a workspace checked out, recorded by the run.
	MetaBranch = "branch"
	// MetaBase is the branch a workspace's diff is taken against.
	MetaBase = "base"
	// MetaRepo is the repository a workspace or PR belongs to.
	MetaRepo = "repo"
	// MetaRunner is which agent ran a session.
	MetaRunner = "runner"
	// MetaSessionID is a session's own id, for display; Ref holds the path.
	MetaSessionID = "session_id"
	// MetaCwd is the directory a session ran in.
	MetaCwd = "cwd"
	// MetaStore is where a document lives.
	MetaStore = "store"
	// StoreVault is MetaStore's value for a vault document. A constant because three packages compare against it, and a typo would silently mean "not in the vault".
	StoreVault = "vault"
	// MetaPort is the port a review pane bound.
	MetaPort = "port"
	// MetaPID is the process id of a review pane.
	MetaPID = "pid"
	// MetaRelation is how a task binding relates: RelationParent or RelationNext.
	MetaRelation = "relation"
)

// The values MetaRelation takes on a KindTask binding.
const (
	// RelationNext is follow-on work this task spawned.
	RelationNext = "next"
	// RelationParent is the task this one was spawned from.
	RelationParent = "parent"
)

// Binding is one typed, stateful reference hanging off a task.
type Binding struct {
	Kind Kind `json:"kind"`
	// Ref is the thing itself, unique within a kind for a given task.
	Ref string `json:"ref"`
	// Label is human-facing text.
	Label string `json:"label,omitempty"`
	// State is the referent's lifecycle, refreshed rather than assumed.
	State BindingState `json:"state,omitempty"`
	// At is when this binding was recorded.
	At time.Time `json:"at"`
	// Via is the id of the run that produced it; empty means the task's own.
	Via string `json:"via,omitempty"`
	// Host is the actor that owns Ref, set only when the referent is machine-local.
	Host string `json:"host,omitempty"`
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

// StateLabel is the human word for this state (a live PR reads as "open").
func (b Binding) StateLabel() string {
	if b.State == BindingUnknown {
		return ""
	}
	if b.State == BindingLive {
		switch b.Kind {
		case KindPR, KindIssue:
			return "open"
		}
	}
	return string(b.State)
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

// Current returns the newest live binding of a kind.
// Ties go to the last recorded: bindings recovered from flat metadata share
// a zero timestamp, so every one of them ties and Current returns whichever
// was read last. That is right for a worktree (a re-run's checkout is the
// one you want) and wrong for pull requests, where a flat array is in
// *report* order and the first is the one the run led with — a caller that
// wants report order should use Live, not Current.
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

// Supersede marks every live binding of a kind as replaced, except those
// produced by the named run.
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

// Run is one dispatch of a workflow against a task. See DESIGN-task.md.
type Run struct {
	ID string `json:"id"`
	// Workflow, Profile and Model are how this run was dispatched.
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

// Record is wf's own ledger record: runs and machine-local bindings, keyed by the tracker's own id. See DESIGN-slim.md. It never mirrors work state.
type Record struct {
	// ID is the tracker's own id (a ULID on kata).
	ID       string    `json:"id"`
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
