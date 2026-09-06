package wf

// Reading a task's bindings out of tracker metadata.
//
// This is the one reader of the flat keys — metadata in, typed bindings out
// — so which key holds what is an implementation detail of this file rather
// than shared knowledge. Before it existed every consumer re-derived the
// join: `wf show`, `--json`, the review ladder and the escalation comment
// each read the keys they cared about with their own tolerance for a
// malformed value.
//
// It is no longer the authoritative source, and being clear about that
// matters more than the code here. The ledger in internal/store is
// authoritative for *bindings*; the tracker is authoritative for *work
// state* — title, priority, labels, open or closed, queue order — and those
// two sets are disjoint. What a run writes to the tracker is publication:
// one direction, derived output, never read back to overrule a binding. So
// this reader is what answers for a task the ledger has never seen — one
// bound by an earlier release, or filed on a host whose ledger is not this
// one — and its answers carry no run and no host, because flat keys have
// nowhere to put either.
//
// Nothing here returns an error. A garbled history, a note key holding a
// number, a PR array that is not an array: each reads as no binding at all.
// That is the contract HistoryFromMeta and PRsFromMeta already had and it
// is deliberate — a binding is how work is found again, never a lifecycle
// input, so bad metadata must cost you a link, never a run.

import "time"

// The only runner and the only store that ship. They are values on a
// binding rather than halves of a key name, which is the whole content of
// "standalone" — but flat metadata has nowhere per-binding to record them,
// so reading a session out of it has to assert the runner. A binding written
// through the ledger carries what actually ran (see the supervisor's
// recordSession); these constants are what is left for tasks the ledger
// never saw.
const (
	runnerPi   = "pi"
	storeVault = "vault"
)

// LoadBindings reads every binding recorded on a task.
//
// States are left unknown rather than asserted: nothing in metadata says a
// worktree still exists or a PR is still open, and Binding.IsLive treats
// unknown as live — unproven, not hidden. The one exception is a session
// replaced by a later one, which the record does say.
func LoadBindings(task Task) Bindings {
	meta := task.Meta
	var bs Bindings

	repo := metaString(meta, RepoKey)
	if repo != "" {
		bs = bs.Upsert(Binding{Kind: KindRepo, Ref: repo})
	}

	// One workspace key, so one workspace binding: a re-run overwrites it,
	// which is exactly the clobber the ledger fixes by keeping a binding
	// per run and superseding rather than replacing. Each session carries
	// the directory it ran in, so nothing is lost here that the metadata
	// still holds. There is no branch to recover: the flat keys never had
	// one, which is why it had to be re-derived from the task's title.
	if dir := metaString(meta, SessionWorkspaceKey, LegacySessionWorkspaceKey); dir != "" {
		b := Binding{Kind: KindWorkspace, Ref: dir}
		if repo != "" {
			b.Meta = map[string]string{MetaRepo: repo}
		}
		bs = bs.Upsert(b)
	}

	bs = append(bs, sessionBindings(meta)...)

	for _, url := range PRsFromMeta(meta) {
		if url == "" {
			continue
		}
		bs = bs.Upsert(Binding{Kind: KindPR, Ref: url})
	}

	for _, issue := range IssuesFromMeta(meta) {
		if issue.URL == "" {
			continue
		}
		bs = bs.Upsert(Binding{Kind: KindIssue, Ref: issue.URL, Label: issue.Title})
	}

	if doc := metaString(meta, DocKey, ObsidianNoteKey); doc != "" {
		bs = bs.Upsert(Binding{
			Kind: KindDoc,
			Ref:  doc,
			Meta: map[string]string{MetaStore: storeVault},
		})
	}

	return bs
}

// sessionBindings folds the run history and the current-session keys into
// one list. Session file paths are minted per spawn, so the current keys
// always name the newest history entry rather than a second session.
//
// Which one is current is recorded as state, not inferred from a clock: the
// current keys carry no timestamp, and a task whose history failed to
// decode would otherwise have nothing to sort on. Ending a run does *not*
// retire its session — the file is still there and `wf attach` still opens
// it, which is the entire reason the binding is written at spawn rather
// than at completion. What retires a session is a later one taking over.
func sessionBindings(meta map[string]any) Bindings {
	var bs Bindings
	for _, h := range HistoryFromMeta(meta) {
		bs = bs.Upsert(sessionBinding(h.Path, h.ID, h.Cwd, h.Started))
	}

	current, ok := BindingFromMeta(meta)
	if !ok {
		// No current keys: the newest history entry stands on its own
		// Started time, which is what Current() sorts on.
		return bs
	}

	recorded := false
	for i := range bs {
		if bs[i].Ref != current.Path {
			bs[i].State = BindingSuperseded
			continue
		}
		recorded = true
		// The history entry is the richer record — it has a timestamp —
		// so the current keys only fill what it left blank.
		fillMissing(&bs[i], MetaSessionID, current.ID)
		fillMissing(&bs[i], MetaCwd, current.Cwd)
	}
	if !recorded {
		bs = append(bs, sessionBinding(current.Path, current.ID, current.Cwd, time.Time{}))
	}
	return bs
}

func sessionBinding(path, id, cwd string, at time.Time) Binding {
	b := Binding{
		Kind: KindSession,
		Ref:  path,
		At:   at,
		Meta: map[string]string{MetaRunner: runnerPi},
	}
	if id != "" {
		b.Meta[MetaSessionID] = id
	}
	if cwd != "" {
		b.Meta[MetaCwd] = cwd
	}
	return b
}

func fillMissing(b *Binding, key, value string) {
	if value == "" || b.Meta[key] != "" {
		return
	}
	if b.Meta == nil {
		b.Meta = map[string]string{}
	}
	b.Meta[key] = value
}

// metaString reads the first key holding a non-empty string. The fallbacks
// are how the runner-namespaced keys keep resolving for one release after
// the rename: read both, write new.
func metaString(meta map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := meta[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
