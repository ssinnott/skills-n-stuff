package wf

// Reading a task's shareable bindings out of tracker metadata.
//
// This is the one reader of these flat keys — metadata in, typed bindings
// out — so which key holds what is an implementation detail of this file
// rather than shared knowledge. Nothing else writes these keys (see
// recordRunFacts and BindArtifacts), so reading them back here is not the
// sync-back the design refuses. Sessions and workspaces are machine-local
// and live only in the local ledger; this file has nothing to say about
// either.
//
// Nothing here returns an error: a garbled PR array or a doc key holding a
// number reads as no binding at all, since a binding is how work is found
// again, never a lifecycle input.

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
