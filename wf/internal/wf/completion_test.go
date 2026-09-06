package wf

import "testing"

func prs(states ...BindingState) Record {
	rec := Record{ID: "t1"}
	for i, s := range states {
		rec.Bindings = append(rec.Bindings, Binding{
			Kind: KindPR, Ref: string(rune('a'+i)) + "-url", State: s,
		})
	}
	return rec
}

func TestPRCompletion(t *testing.T) {
	tests := []struct {
		name        string
		rec         Record
		want        Completion
		completable bool
		blocker     string
	}{
		{
			name:        "every pull request merged",
			rec:         prs(BindingMerged, BindingMerged),
			want:        Completion{Total: 2, Merged: 2},
			completable: true,
		},
		{
			name:    "one still open",
			rec:     prs(BindingMerged, BindingLive),
			want:    Completion{Total: 2, Merged: 1, Open: 1},
			blocker: "pull requests still open",
		},
		{
			name: "one never checked — an unverified pull request is not a merged one",
			rec:  prs(BindingMerged, BindingUnknown),
			want: Completion{Total: 2, Merged: 1, Unknown: 1},
			// The blocker names the state rather than the PR, because the
			// fix is to run a refresh.
			blocker: "pull request state unknown",
		},
		{
			name:    "closed without merging",
			rec:     prs(BindingMerged, BindingClosed),
			want:    Completion{Total: 2, Merged: 1, Closed: 1},
			blocker: "a pull request was closed without merging",
		},
		{
			name: "no pull requests at all is not completable by this rule",
			rec:  Record{ID: "t1", Bindings: Bindings{{Kind: KindDoc, Ref: "Notes/plan.md"}}},
			want: Completion{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rec.PRCompletion()
			if got != tt.want {
				t.Errorf("PRCompletion() = %+v, want %+v", got, tt.want)
			}
			if got.Complete() != tt.completable {
				t.Errorf("Complete() = %v, want %v", got.Complete(), tt.completable)
			}
			if got.Blocker() != tt.blocker {
				t.Errorf("Blocker() = %q, want %q", got.Blocker(), tt.blocker)
			}
		})
	}
}

func TestCompletionIgnoresOtherKinds(t *testing.T) {
	// A live worktree or an open tracker row says nothing about whether the
	// pull requests landed, and the tracker owns work state anyway.
	rec := prs(BindingMerged)
	rec.Bindings = append(rec.Bindings,
		Binding{Kind: KindWorkspace, Ref: "/tmp/wt", State: BindingLive},
		Binding{Kind: KindQueue, Ref: "01M1S", State: BindingLive},
		Binding{Kind: KindIssue, Ref: "https://example/1", State: BindingLive},
	)
	if !rec.PRCompletion().Complete() {
		t.Error("Complete() = false; only pull request bindings count toward it")
	}
}
