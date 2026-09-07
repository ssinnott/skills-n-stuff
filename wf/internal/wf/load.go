package wf

// LoadBindings reads every shareable binding recorded on a task's tracker
// row. Nothing here returns an error: garbled metadata reads as no binding at
// all. States are left unknown rather than asserted, and Binding.IsLive treats unknown as live — unproven, not hidden.
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

// metaString reads a metadata key as a non-empty string, or "" otherwise.
func metaString(meta map[string]any, key string) string {
	if v, ok := meta[key].(string); ok {
		return v
	}
	return ""
}
