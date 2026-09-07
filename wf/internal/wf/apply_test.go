package wf

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
)

func task() Task {
	return Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser", Meta: map[string]any{}}
}

func TestApplyEscalatesWithoutDone(t *testing.T) {
	// Silence is never success: a run that stopped talking is not complete.
	cases := map[string]string{
		"no outcomes at all": "the agent said nothing useful\n",
		"work but no DONE":   "PR: https://a/1 — Opened a PR\n",
	}

	for name, transcript := range cases {
		t.Run(name, func(t *testing.T) {
			q := newFakeQueue()
			got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if !got.Escalated || got.Completed {
				t.Errorf("Apply() = %+v, want escalated and not completed", got)
			}
			if len(q.closes) != 0 {
				t.Error("a run never closes the task")
			}
			if q.meta[AttentionKey] != "needs-human" {
				t.Errorf("attention key = %v, want needs-human", q.meta[AttentionKey])
			}
			if len(q.comments) == 0 || !strings.Contains(q.comments[0], "Needs a human") {
				t.Errorf("escalation must be explained on the task: %v", q.comments)
			}
		})
	}
}

func TestEscalationIncludesTranscript(t *testing.T) {
	q := newFakeQueue()
	transcript := "step one\nstep two\nfatal: could not resolve host\n"

	if _, err := Escalate(context.Background(), q, task(), "the agent process failed", transcript); err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	// An escalation a human cannot diagnose from the tracker is one they
	// have to go hunting for.
	if !strings.Contains(q.comments[0], "could not resolve host") {
		t.Errorf("escalation comment lacks the failure output:\n%s", q.comments[0])
	}
}

// A run that reports DONE with something to show completes, and that is
// all it does to the task's lifecycle: the task stays open, in review, for
// a human to run the next workflow or close it.
func TestApplyCompletesAndLeavesTheTaskOpen(t *testing.T) {
	q := newFakeQueue()
	transcript := "PR: https://a/1 — Add it\nDOC: notes/plan.md — The plan\nDONE Shipped\n"

	got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !got.Completed || got.Escalated {
		t.Errorf("Apply() = %+v, want completed", got)
	}
	if len(q.closes) != 0 {
		t.Fatalf("closes = %d, want 0 — a run never closes the task", len(q.closes))
	}
	if q.meta[StateKey] != string(StateReview) {
		t.Errorf("state = %v, want review", q.meta[StateKey])
	}
	if _, flagged := q.meta[AttentionKey]; flagged {
		t.Error("a completed run is not an escalation")
	}
	if prs := PRsFromMeta(q.meta); len(prs) != 1 || prs[0] != "https://a/1" {
		t.Errorf("PRsFromMeta() = %v, want the PR recorded for the close", prs)
	}
	if len(q.comments) != 1 || !strings.Contains(q.comments[0], "https://a/1") {
		t.Errorf("run summary not commented: %v", q.comments)
	}
}

// A task accumulates output across every run against it: a second run's PR
// joins the first's rather than replacing it.
func TestRunFactsMergeAcrossRuns(t *testing.T) {
	q := newFakeQueue()
	first := task()
	first.Meta = map[string]any{
		PRsKey:    `["https://a/1"]`,
		IssuesKey: `[{"url":"https://a/i1","title":"first"}]`,
	}
	transcript := "PR: https://a/2 — Second\nPR: https://a/1 — Same again\nISSUE: https://a/i2 — second\nDONE\n"

	if _, err := Apply(context.Background(), q, first, ParseOutcomes(transcript), ApplyOptions{Transcript: transcript}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if prs := PRsFromMeta(q.meta); !reflect.DeepEqual(prs, []string{"https://a/1", "https://a/2"}) {
		t.Errorf("PRsFromMeta() = %v, want the first run's PR kept and the new one appended once", prs)
	}
	issues := IssuesFromMeta(q.meta)
	if len(issues) != 2 || issues[0].URL != "https://a/i1" || issues[1].URL != "https://a/i2" {
		t.Errorf("IssuesFromMeta() = %+v, want both runs' issues", issues)
	}
}

// --- closing --------------------------------------------------------------

func TestCloseUsesTheEvidenceRunsRecorded(t *testing.T) {
	q := newFakeQueue()
	tk := task()
	tk.Meta = map[string]any{
		PRsKey:  `["https://a/1"]`,
		DocsKey: `["Research/plan.md"]`,
	}

	got, err := Close(context.Background(), q, tk, "")
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(q.closes) != 1 {
		t.Fatalf("closes = %d, want 1", len(q.closes))
	}
	closed := q.closes[0]
	if !reflect.DeepEqual(closed.PRs, []string{"https://a/1"}) || !reflect.DeepEqual(closed.Docs, []string{"Research/plan.md"}) {
		t.Errorf("closed with %+v, want the recorded PR and document", closed)
	}
	// Kata refuses a message under MinCloseMessage; wf composes one from
	// what it knows rather than padding with filler.
	if len(got.Message) < MinCloseMessage {
		t.Errorf("Message = %q, too short for a tracker that demands substance", got.Message)
	}
	if !strings.Contains(got.Message, "Add the parser") || !strings.Contains(got.Message, "https://a/1") {
		t.Errorf("Message = %q, want the task and its evidence named", got.Message)
	}
	if q.meta[StateKey] != string(StateDone) {
		t.Errorf("state = %v, want done", q.meta[StateKey])
	}
}

func TestCloseKeepsAHumansMessage(t *testing.T) {
	q := newFakeQueue()
	tk := task()
	tk.Meta = map[string]any{PRsKey: `["https://a/1"]`}
	message := "Merged after review; the flake it fixed has not recurred."

	got, err := Close(context.Background(), q, tk, message)
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got.Message != message {
		t.Errorf("Message = %q, want the human's own words untouched", got.Message)
	}
}

func TestCloseRefusesWithoutEvidence(t *testing.T) {
	// A filed issue says work moved elsewhere, not that this task's work
	// exists, so it is not evidence for a close.
	q := newFakeQueue()
	tk := task()
	tk.Meta = map[string]any{IssuesKey: `[{"url":"https://a/i1","title":"moved"}]`}

	if _, err := Close(context.Background(), q, tk, ""); err == nil {
		t.Fatal("Close() with no PR or document must refuse")
	}
	if len(q.closes) != 0 {
		t.Error("nothing should have been closed")
	}
	if _, set := q.meta[StateKey]; set {
		t.Error("a refused close must not touch the task's state")
	}
}

func TestApplyMaterializesNextAsSibling(t *testing.T) {
	q := newFakeQueue()
	transcript := "PR: https://a/1 — The change\nNEXT: fix the flake via /plan-to-pr\nDONE\n"

	got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(q.created) != 1 {
		t.Fatalf("created = %d follow-on tasks, want 1", len(q.created))
	}
	created := q.created[0]
	if created.Title != "fix the flake via /plan-to-pr" {
		t.Errorf("Title = %q", created.Title)
	}
	if created.RelatedTo != "01HZ" {
		t.Errorf("RelatedTo = %q, want the parent", created.RelatedTo)
	}
	if created.IdempotencyKey == "" {
		t.Error("follow-on creates need an idempotency key, or a retry files twice")
	}
	if len(got.Created) != 1 {
		t.Errorf("ApplyResult.Created = %v", got.Created)
	}
}

func TestApplyRecordsFiledIssuesWithoutGating(t *testing.T) {
	q := newFakeQueue()
	transcript := "DOC: triage.md — Triage writeup\nISSUE: https://a/i1 — Found a bug\nDONE\n"

	got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	// A filed issue is recorded, never waited on: creating it was the work,
	// so the run completes without regard to whether the issue is resolved.
	if !got.Completed {
		t.Error("a filed ISSUE must not gate completion")
	}
	if len(q.created) != 0 {
		t.Error("ISSUE records an already-filed issue; it must not create one")
	}
	if len(q.comments) == 0 || !strings.Contains(q.comments[0], "https://a/i1") {
		t.Errorf("filed issue not recorded on the task: %v", q.comments)
	}
}

func TestApplyEscalatesDoneWithoutOutput(t *testing.T) {
	// A run's bar is lower than a close's: an ISSUE alone completes a run
	// whose whole job was to file it (see TestApplyRecordsFiledIssues…),
	// but a DONE with nothing reported at all is unfinished work.
	cases := map[string]string{
		"bare done":        "DONE\n",
		"only a repo":      "REPO: /src/app\nDONE Looked around.\n",
		"only a follow-up": "NEXT: do the real work\nDONE\n",
	}

	for name, transcript := range cases {
		t.Run(name, func(t *testing.T) {
			q := newFakeQueue()
			got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if got.Completed {
				t.Error("a DONE with nothing to show for it is not complete")
			}
			if !got.Escalated {
				t.Errorf("Apply() = %+v, want escalated", got)
			}
			if len(q.created) != 0 {
				t.Error("follow-ons are filed only for a completed run")
			}
			if !strings.Contains(strings.Join(q.comments, "\n"), "produced nothing") {
				t.Errorf("the escalation should say why: %v", q.comments)
			}
		})
	}
}

func TestApplyCompletesOnAFiledIssueAlone(t *testing.T) {
	q := newFakeQueue()
	transcript := "ISSUE: https://a/i1 — Found a bug\nDONE\n"

	got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !got.Completed || got.Escalated {
		t.Errorf("Apply() = %+v, want completed — filing the issue was the run's whole job", got)
	}
	// ...but it is no evidence that the task's work exists.
	if Evidence(Task{Meta: q.meta}).HasEvidence() {
		t.Error("a filed issue must not count as evidence for a close")
	}
}

func TestApplyRecordsReportedRepo(t *testing.T) {
	q := newFakeQueue()
	transcript := "REPO: /src/app\nPR: https://a/1 — Add it\nDONE Shipped\n"

	if _, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if q.meta[RepoKey] != "/src/app" {
		t.Errorf("repo metadata = %v, want the reported repo", q.meta[RepoKey])
	}
}

func TestApplyRecordsRunFactsEvenWhenEscalated(t *testing.T) {
	// An escalated run that still opened a PR or named its repo must not
	// lose that trace — those are exactly the runs `wf review` needs to
	// find later.
	q := newFakeQueue()
	transcript := "REPO: /src/app\nPR: https://a/1 — Opened a PR\n" // no DONE: escalates

	got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !got.Escalated {
		t.Fatalf("Apply() = %+v, want escalated", got)
	}
	if q.meta[RepoKey] != "/src/app" {
		t.Errorf("repo metadata = %v, want it recorded despite escalation", q.meta[RepoKey])
	}
	if prs := PRsFromMeta(q.meta); len(prs) != 1 || prs[0] != "https://a/1" {
		t.Errorf("PRsFromMeta() = %v, want the reported PR despite escalation", prs)
	}
}

func TestApplyRecordsPRsAndFiledIssues(t *testing.T) {
	q := newFakeQueue()
	transcript := "PR: https://a/1 — First\nPR: https://a/2 — Second\n" +
		"ISSUE: https://a/i1 — internal/foo.go:42 nil check\nDONE Shipped\n"

	if _, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if prs := PRsFromMeta(q.meta); !reflect.DeepEqual(prs, []string{"https://a/1", "https://a/2"}) {
		t.Errorf("PRsFromMeta() = %v", prs)
	}
	issues := IssuesFromMeta(q.meta)
	if len(issues) != 1 || issues[0].URL != "https://a/i1" || issues[0].Title != "internal/foo.go:42 nil check" {
		t.Errorf("IssuesFromMeta() = %+v", issues)
	}
}

func TestPRsAndIssuesFromMetaTolerance(t *testing.T) {
	for _, value := range []any{nil, "", "not json", 42} {
		if got := PRsFromMeta(map[string]any{PRsKey: value}); len(got) != 0 {
			t.Errorf("PRsFromMeta(%#v) = %v, want empty", value, got)
		}
		if got := IssuesFromMeta(map[string]any{IssuesKey: value}); len(got) != 0 {
			t.Errorf("IssuesFromMeta(%#v) = %v, want empty", value, got)
		}
	}
}

func TestCloseResultHasEvidence(t *testing.T) {
	if (CloseResult{Message: "words alone"}).HasEvidence() {
		t.Error("a message is not evidence")
	}
	for _, r := range []CloseResult{
		{PRs: []string{"https://a/1"}},
		{Docs: []string{"notes/plan.md"}},
	} {
		if !r.HasEvidence() {
			t.Errorf("%+v should count as evidence", r)
		}
	}
}

// --- artifact binding ----------------------------------------------------

func TestBindArtifactsMovesDocIntoVault(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue()

	vault := t.TempDir()
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "plan.md"), []byte("# Plan\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcomes := ParseOutcomes("DOC: plan.md — The plan\nDONE\n")
	bound, err := BindArtifacts(ctx, q, task(), outcomes, BindOptions{
		Vault: vault, VaultDir: "Research", WorkspaceDir: work,
	})
	if err != nil {
		t.Fatalf("BindArtifacts() error = %v", err)
	}
	if len(bound) != 1 {
		t.Fatalf("bound = %d docs, want 1", len(bound))
	}
	if bound[0].VaultPath != filepath.Join("Research", "plan.md") {
		t.Errorf("VaultPath = %q", bound[0].VaultPath)
	}

	// The note now carries the task's durable ref...
	moved := filepath.Join(vault, bound[0].VaultPath)
	text, err := os.ReadFile(moved)
	if err != nil {
		t.Fatalf("read moved note: %v", err)
	}
	if got := note.GetField(string(text), note.IssueKey); got != "01HZ" {
		t.Errorf("note frontmatter = %q, want the task ULID", got)
	}
	if !strings.Contains(string(text), "# Plan") {
		t.Error("note body was lost in the move")
	}

	// ...and the task, having no note yet, adopts this one as its own.
	if q.meta[DocKey] != bound[0].VaultPath {
		t.Errorf("task metadata = %v, want the vault path", q.meta[DocKey])
	}
	// The worktree copy is gone, so the binding cannot point at a disposed path.
	if _, err := os.Stat(filepath.Join(work, "plan.md")); !os.IsNotExist(err) {
		t.Error("source document should have been moved, not copied")
	}
}

func TestBindArtifactsLeavesVaultNotesInPlace(t *testing.T) {
	ctx := context.Background()
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, "Notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(vault, "Notes", "existing.md")
	if err := os.WriteFile(existing, []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	outcomes := ParseOutcomes("DOC: " + existing + " — Already home\nDONE\n")
	bound, err := BindArtifacts(ctx, newFakeQueue(), task(), outcomes, BindOptions{Vault: vault, VaultDir: "Research"})
	if err != nil {
		t.Fatalf("BindArtifacts() error = %v", err)
	}
	if len(bound) != 1 {
		t.Fatalf("bound = %+v, want an in-place binding", bound)
	}
	if bound[0].VaultPath != filepath.Join("Notes", "existing.md") {
		t.Errorf("VaultPath = %q, want the original location", bound[0].VaultPath)
	}
	if _, err := os.Stat(existing); err != nil {
		t.Error("a note already in the vault must stay where it is")
	}
}

// The task's note is the first document it was ever bound to; a later
// run's document is recorded as a document, not swapped in as the note.
func TestBindArtifactsKeepsAnExistingNote(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue()
	vault := t.TempDir()
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "review.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tk := task()
	tk.Meta = map[string]any{DocKey: "Bugs/the-bug.md", DocsKey: `["Bugs/the-bug.md"]`}

	transcript := "DOC: review.md — Review writeup\nDONE\n"
	got, err := Apply(ctx, q, tk, ParseOutcomes(transcript), ApplyOptions{
		Transcript: transcript,
		Bind:       BindOptions{Vault: vault, VaultDir: "Research", WorkspaceDir: work},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(got.Bound) != 1 {
		t.Fatalf("Bound = %v, want one document", got.Bound)
	}
	if _, overwritten := q.meta[DocKey]; overwritten {
		t.Error("wf.doc was rewritten; the task's note must survive later runs")
	}
	want := []string{"Bugs/the-bug.md", filepath.Join("Research", "review.md")}
	if docs := DocsFromMeta(q.meta); !reflect.DeepEqual(docs, want) {
		t.Errorf("DocsFromMeta() = %v, want %v", docs, want)
	}
}

func TestBindArtifactsSkipsMissingFiles(t *testing.T) {
	// An agent that named a file it did not write should not cost the close.
	bound, err := BindArtifacts(context.Background(), newFakeQueue(), task(),
		ParseOutcomes("DOC: nope.md — Never written\nDONE\n"),
		BindOptions{Vault: t.TempDir(), VaultDir: "Research", WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatalf("BindArtifacts() error = %v", err)
	}
	if len(bound) != 0 {
		t.Errorf("bound = %v, want nothing", bound)
	}
}

func TestBindArtifactsDisabledWithoutVault(t *testing.T) {
	bound, err := BindArtifacts(context.Background(), newFakeQueue(), task(),
		ParseOutcomes("DOC: plan.md — Plan\nDONE\n"), BindOptions{})
	if err != nil || bound != nil {
		t.Errorf("BindArtifacts() = %v, %v; want disabled", bound, err)
	}
}

func TestBindArtifactsAvoidsClobbering(t *testing.T) {
	ctx := context.Background()
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, "Research"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "Research", "plan.md"), []byte("first run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "plan.md"), []byte("second run\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bound, err := BindArtifacts(ctx, newFakeQueue(), task(),
		ParseOutcomes("DOC: plan.md — Plan\nDONE\n"),
		BindOptions{Vault: vault, VaultDir: "Research", WorkspaceDir: work})
	if err != nil {
		t.Fatalf("BindArtifacts() error = %v", err)
	}
	if bound[0].VaultPath == filepath.Join("Research", "plan.md") {
		t.Error("second run overwrote the first run's note")
	}

	original, _ := os.ReadFile(filepath.Join(vault, "Research", "plan.md"))
	if !strings.Contains(string(original), "first run") {
		t.Error("the existing note was clobbered")
	}
}

func TestApplyCitesFinalDocumentLocation(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue()

	vault := t.TempDir()
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "plan.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	transcript := "DOC: plan.md — The plan\nDONE — review: plan.md\n"
	got, err := Apply(ctx, q, task(), ParseOutcomes(transcript), ApplyOptions{
		Transcript: transcript,
		Bind:       BindOptions{Vault: vault, VaultDir: "Research", WorkspaceDir: work},
	})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(got.Bound) != 1 {
		t.Fatalf("Bound = %v, want one document", got.Bound)
	}

	// The record a later close cites must not be a path inside a disposed
	// worktree.
	docs := DocsFromMeta(q.meta)
	if len(docs) != 1 {
		t.Fatalf("Docs = %v, want one", docs)
	}
	if !strings.HasPrefix(docs[0], "Research") {
		t.Errorf("recorded document = %q, want the vault path", docs[0])
	}
	if !strings.Contains(q.comments[0], "Research") {
		t.Errorf("summary comment should name the final location:\n%s", q.comments[0])
	}
}
