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

// MachineLocal reports whether a kind's Ref only resolves on the host that
// recorded it. A worktree path, a session file and a browser origin are
// facts about one machine; a pull request URL, a vault-relative document
// and a tracker id are facts anywhere.
//
// The distinction has one job: bindings of these kinds carry Host, so a
// laptop and a desktop against one queue stop silently overwriting each
// other's answer to "where is the checkout." Everything downstream — the
// task block's *(wf-laptop)* annotation, a future `wf gc` — reads that
// field rather than re-deciding which kinds deserve one.
func (k Kind) MachineLocal() bool {
	switch k {
	case KindWorkspace, KindSession, KindReview:
		return true
	}
	return false
}

// MachineLocal reports whether this particular binding's Ref only resolves
// on the host that recorded it. It is the per-binding answer, and it exists
// because Kind alone gets documents wrong.
//
// A document is portable when it landed in the vault, which is what
// MetaStore records — a vault-relative path resolves on any device holding
// the vault. A DOC: outcome a workflow did *not* bind stays wherever the
// agent wrote it, usually inside a worktree that is disposed moments later.
// Those two are the same Kind and are not the same fact, so asking the kind
// would host-stamp neither and leave the unbound one reading as a live path
// on a machine that never had it.
//
// Prefer this over Kind.MachineLocal when you hold a whole binding.
func (b Binding) MachineLocal() bool {
	if b.Kind == KindDoc {
		// Anything with a store behind it is that store's to resolve.
		return b.Get(MetaStore) == ""
	}
	return b.Kind.MachineLocal()
}

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
	// StoreVault is MetaStore's value for a document in the Obsidian vault.
	// A constant because three packages now compare against it, and a typo
	// in any of them would silently mean "not in the vault".
	StoreVault = "vault"
	// MetaPort is the port a review pane bound.
	MetaPort = "port"
	// MetaPID is the process id of a review pane.
	MetaPID = "pid"
	// MetaRelation is how a task binding relates: RelationParent or
	// RelationNext.
	MetaRelation = "relation"
	// MetaShortID is a tracker's own human-facing ref for a queue binding,
	// recorded because it cannot be derived. kata builds its short id from
	// the *last* four characters of the ULID, so no prefix rule finds it;
	// a resolver that guessed at that would be encoding one backend's
	// convention in the one place that is supposed to be backend-neutral.
	MetaShortID = "short_id"
)

// The values MetaRelation takes on a KindTask binding. Named rather than
// spelled out at each call site because both ends of a NEXT edge have to
// agree on the word, and they are written by different code paths.
const (
	// RelationNext is follow-on work this task spawned.
	RelationNext = "next"
	// RelationParent is the task this one was spawned from.
	RelationParent = "parent"
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

// StateLabel is the word a human should see for this binding's state.
//
// It exists because the vocabulary is shared across kinds but the natural
// word is not: a pull request that is live is "open", never "live", and a
// reader who sees the raw state on a PR reads it as jargon. Translating in
// the renderer would be worse than translating here — `wf show`, the
// Obsidian task block and `--json` consumers would each pick their own
// word for the same fact, and they would drift.
//
// An empty result means nothing is known and callers should render no
// state at all, rather than inventing one.
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
//
// Ties go to the last recorded, which matters more than it looks: bindings
// recovered from flat metadata share a zero timestamp, so every one of them
// ties and Current returns whichever was read last. That is right for a
// worktree (a re-run's checkout is the one you want) and wrong for pull
// requests, where a flat array is in *report* order and the first is the
// one the run led with. So a caller that means "in the order the run
// reported them" wants Live, not Current — `internal/review` takes exactly
// that route, and says why at the call site.
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

// Note returns the task's own note — the document that faces this task,
// as opposed to a document some run produced.
//
// Both are KindDoc bindings in a vault, which is why asking for the newest
// doc gets this wrong: a research note a run wrote is newer than the task
// note almost immediately, and reporting it as "the note" sends a reader —
// or the Obsidian plugin's own rendering — to the wrong file. The task's
// note is the one no run produced, so an empty Via is what separates them.
func (bs Bindings) Note() (Binding, bool) {
	var best Binding
	found := false
	for _, b := range bs {
		if b.Kind != KindDoc || b.Via != "" || !b.IsLive() {
			continue
		}
		if b.Get(MetaStore) != StoreVault {
			continue
		}
		if !found || !b.At.Before(best.At) {
			best, found = b, true
		}
	}
	return best, found
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
	Handle string `json:"handle,omitempty"`
	// Title is the name a task answers to before — or without — a tracker
	// row.
	//
	// This is the one place the ledger holds something the tracker would
	// otherwise own, and it is here because "a task exists whether or not a
	// tracker row does" leaves the title with nowhere else to live: `wf
	// task new "fix the parser"` has to record that string somewhere. It is
	// a *fallback*, not a mirror — see Name. Nothing refreshes it from the
	// tracker, so a renamed row does not make it stale; it simply stops
	// being what anyone reads.
	Title    string    `json:"title,omitempty"`
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

// Name is what a human should see for this task.
//
// The tracker wins when there is one: its row's Label is refreshed on every
// dispatch, so it is the live answer, while Title is whatever the task was
// called when wf minted it. That ordering is the disjointness rule applied
// to one field — the tracker owns work state, the ledger owns everything
// machine-local and everything with provenance, and a title recorded by
// `wf task new` is only the ledger's answer until a row exists to overrule
// it.
func (r Record) Name() string {
	if b, ok := r.Bindings.Current(KindQueue); ok && b.Label != "" {
		return b.Label
	}
	return r.Title
}

// Ref is the shortest thing that resolves this task: the handle when it has
// one, the id otherwise. What `wf task new` prints and what `wf show` leads
// with.
func (r Record) Ref() string {
	if r.Handle != "" {
		return r.Handle
	}
	return r.ID
}

// QueueRef returns the tracker id this task is filed under, if it is.
func (r Record) QueueRef() (string, bool) {
	b, ok := r.Bindings.Current(KindQueue)
	if !ok {
		return "", false
	}
	return b.Ref, true
}
