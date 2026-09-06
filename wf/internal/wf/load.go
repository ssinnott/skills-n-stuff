package wf

// Reading a task's shareable bindings out of tracker metadata.
//
// This is the one reader of the flat keys — metadata in, typed bindings out
// — so which key holds what is an implementation detail of this file rather
// than shared knowledge. Before it existed every consumer re-derived the
// join: `wf show`, `--json`, the review ladder and the escalation comment
// each read the keys they cared about with their own tolerance for a
// malformed value.
//
// kata is the only home for what this file reads: the repo a run named, the
// PRs and issues it reported, and the note a task is bound to. Publishing
// them here is one direction and reading them back is not the sync-back the
// design refuses, because nothing else writes these keys — see
// recordRunFacts and BindArtifacts in apply.go and artifact.go. A session
// and its workspace are a different kind of fact — machine-local — and live
// only in the local ledger; this file has nothing to say about either.
//
// Nothing here returns an error. A garbled PR array, a doc key holding a
// number: each reads as no binding at all. That is deliberate — a binding
// is how work is found again, never a lifecycle input, so bad metadata must
// cost you a link, never a run.

// LoadBindings reads every shareable binding recorded on a task's tracker
// row.
//
// States are left unknown rather than asserted: nothing in metadata says a
// PR is still open, and Binding.IsLive treats unknown as live — unproven,
// not hidden.
func LoadBindings(task Task) Bindings {
	meta := task.Meta
	var bs Bindings

	if repo := metaString(meta, RepoKey); repo != "" {
		bs = bs.Upsert(Binding{Kind: KindRepo, Ref: repo})
	}

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

	if doc := metaString(meta, DocKey); doc != "" {
		bs = bs.Upsert(Binding{
			Kind: KindDoc,
			Ref:  doc,
			Meta: map[string]string{MetaStore: StoreVault},
		})
	}

	return bs
}

// metaString reads a metadata key as a non-empty string, or "" if it is
// absent or holds something else.
func metaString(meta map[string]any, key string) string {
	if v, ok := meta[key].(string); ok {
		return v
	}
	return ""
}
