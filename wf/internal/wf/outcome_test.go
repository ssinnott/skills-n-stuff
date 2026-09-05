package wf

import (
	"reflect"
	"testing"
)

func TestParseOutcomes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Outcome
	}{
		{
			name: "bare done",
			in:   "I finished the work.\n\nDONE\n",
			want: []Outcome{{Verb: VerbDone}},
		},
		{
			name: "done naming a review artifact",
			in:   "DONE — review: notes/plan.md\n",
			want: []Outcome{{Verb: VerbDone, Path: "notes/plan.md"}},
		},
		{
			name: "done with message and review",
			in:   "DONE Shipped the parser — review: notes/plan.md\n",
			want: []Outcome{{Verb: VerbDone, Message: "Shipped the parser", Path: "notes/plan.md"}},
		},
		{
			name: "pr and issue with titles",
			in:   "PR: https://example.com/pr/1 — Add the thing\nISSUE: https://example.com/i/2 — Flaky test\n",
			want: []Outcome{
				{Verb: VerbPR, URL: "https://example.com/pr/1", Title: "Add the thing"},
				{Verb: VerbIssue, URL: "https://example.com/i/2", Title: "Flaky test"},
			},
		},
		{
			name: "tolerant separators",
			in:   "PR: https://a/1 - hyphen\nDOC: notes/a.md -- double\nDOC: notes/b.md – en dash\n",
			want: []Outcome{
				{Verb: VerbPR, URL: "https://a/1", Title: "hyphen"},
				{Verb: VerbDoc, Path: "notes/a.md", Title: "double"},
				{Verb: VerbDoc, Path: "notes/b.md", Title: "en dash"},
			},
		},
		{
			name: "verbs wrapped in list markup",
			in:   "- **PR: https://a/1 — Bulleted**\n> DONE\n",
			want: []Outcome{
				{Verb: VerbPR, URL: "https://a/1", Title: "Bulleted"},
				{Verb: VerbDone},
			},
		},
		{
			name: "lowercase prose is not an outcome",
			in:   "Next: we should refactor this.\nThe pr: is not ready.\n",
			want: nil,
		},
		{
			name: "next and repo",
			in:   "NEXT: fix the flake via /plan-to-pr\nREPO: /home/me/code/app\n",
			want: []Outcome{
				{Verb: VerbNext, Text: "fix the flake via /plan-to-pr"},
				{Verb: VerbRepo, Path: "/home/me/code/app"},
			},
		},
		{
			name: "value-less verbs are dropped",
			in:   "PR:\nDOC:\nNEXT:\nREPO:\n",
			want: nil,
		},
		{
			name: "url without a title",
			in:   "PR: https://example.com/pr/9\n",
			want: []Outcome{{Verb: VerbPR, URL: "https://example.com/pr/9"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseOutcomes(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseOutcomes()\n got = %#v\nwant = %#v", got, tt.want)
			}
		})
	}
}

func TestParseOutcomesScansWholeTranscript(t *testing.T) {
	// A runner appending its own footer must not hide the agent's report.
	transcript := "PR: https://a/1 — Real work\nDONE\n\n[runner] exited with status 0\n"
	got := ParseOutcomes(transcript)
	if len(got) != 2 {
		t.Fatalf("expected verbs before the footer, got %#v", got)
	}
}

func TestIsComplete(t *testing.T) {
	if IsComplete(ParseOutcomes("PR: https://a/1 — no done line\n")) {
		t.Error("a run without DONE must not read as complete")
	}
	if !IsComplete(ParseOutcomes("DONE\n")) {
		t.Error("DONE must read as complete")
	}
}

func TestToCloseResult(t *testing.T) {
	outcomes := ParseOutcomes(
		"PR: https://a/1 — First\n" +
			"PR: https://a/2 — Second\n" +
			"DOC: notes/plan.md — Plan\n" +
			"DONE Wrote it up — review: notes/plan.md\n",
	)
	got := ToCloseResult(outcomes)

	if got.Message != "Wrote it up" {
		t.Errorf("Message = %q, want %q", got.Message, "Wrote it up")
	}
	if want := []string{"https://a/1", "https://a/2"}; !reflect.DeepEqual(got.PRs, want) {
		t.Errorf("PRs = %v, want %v", got.PRs, want)
	}
	// A document named by both DOC and DONE is one artifact, not two.
	if want := []string{"notes/plan.md"}; !reflect.DeepEqual(got.Docs, want) {
		t.Errorf("Docs = %v, want %v", got.Docs, want)
	}
}

func TestToCloseResultDefaultMessage(t *testing.T) {
	got := ToCloseResult(ParseOutcomes("DONE\n"))
	if got.Message == "" {
		t.Error("close message must never be empty — kata requires one")
	}
}

func TestSpawnedAndReportedRepo(t *testing.T) {
	outcomes := ParseOutcomes(
		"ISSUE: https://a/i1 — Filed\nNEXT: do the follow-up\nREPO: /src/app\nDONE\n",
	)
	issues, next := Spawned(outcomes)
	if len(issues) != 1 || len(next) != 1 {
		t.Fatalf("Spawned() = %d issues, %d next; want 1, 1", len(issues), len(next))
	}
	if got := ReportedRepo(outcomes); got != "/src/app" {
		t.Errorf("ReportedRepo() = %q, want %q", got, "/src/app")
	}
}
