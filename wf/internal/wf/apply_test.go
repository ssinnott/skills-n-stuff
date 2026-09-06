package wf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
)

func task() Task {
	return Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser", Meta: map[string]any{}}
}

func TestApplyEscalatesWithoutDone(t *testing.T) {
	// Silence is never success: a run that stopped talking must not close.
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
			if !got.Escalated || got.Closed {
				t.Errorf("Apply() = %+v, want escalated and not closed", got)
			}
			if len(q.closes) != 0 {
				t.Error("an incomplete run must not close the task")
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

func TestApplyClosesWithEvidence(t *testing.T) {
	q := newFakeQueue()
	transcript := "PR: https://a/1 — Add it\nDOC: notes/plan.md — The plan\nDONE Shipped\n"

	got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !got.Closed || got.Escalated {
		t.Errorf("Apply() = %+v, want closed", got)
	}
	if len(q.closes) != 1 {
		t.Fatalf("closes = %d, want 1", len(q.closes))
	}

	closed := q.closes[0]
	// The agent's words lead; wf appends what it knows only because a close
	// message under MinCloseMessage is refused outright.
	if !strings.HasPrefix(closed.Message, "Shipped") {
		t.Errorf("Message = %q, want the agent's words first", closed.Message)
	}
	if len(closed.Message) < MinCloseMessage {
		t.Errorf("Message = %q, too short for a tracker that demands substance", closed.Message)
	}
	if !strings.Contains(closed.Message, "Add the parser") {
		t.Errorf("Message = %q, want the task named when padding was needed", closed.Message)
	}
	if len(closed.PRs) != 1 || closed.PRs[0] != "https://a/1" {
		t.Errorf("PRs = %v", closed.PRs)
	}
	if q.meta[StateKey] != string(StateDone) {
		t.Errorf("state = %v, want done", q.meta[StateKey])
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
	// so the task closes without regard to whether the issue is resolved.
	if !got.Closed {
		t.Error("a filed ISSUE must not gate completion")
	}
	if len(q.created) != 0 {
		t.Error("ISSUE records an already-filed issue; it must not create one")
	}
	if len(q.comments) == 0 || !strings.Contains(q.comments[0], "https://a/i1") {
		t.Errorf("filed issue not recorded on the task: %v", q.comments)
	}
}

func TestApplyEscalatesDoneWithoutEvidence(t *testing.T) {
	// ISSUE deliberately does not count: a filed issue says work was moved
	// elsewhere, not that this task's work exists. A triage run should also
	// leave a writeup, which its workflow prompt asks for.
	cases := map[string]string{
		"bare done":          "DONE\n",
		"only a filed issue": "ISSUE: https://a/i1 — Found a bug\nDONE\n",
		"only a repo":        "REPO: /src/app\nDONE Looked around.\n",
		"only a follow-up":   "NEXT: do the real work\nDONE\n",
	}

	for name, transcript := range cases {
		t.Run(name, func(t *testing.T) {
			q := newFakeQueue()
			got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript), ApplyOptions{Transcript: transcript})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if got.Closed {
				t.Error("a DONE with nothing to show for it must not close the task")
			}
			if !got.Escalated {
				t.Errorf("Apply() = %+v, want escalated", got)
			}
			if len(q.closes) != 0 {
				t.Error("nothing should have been closed")
			}
			if !strings.Contains(strings.Join(q.comments, "\n"), "no evidence") {
				t.Errorf("the escalation should say why: %v", q.comments)
			}
		})
	}
}

func TestCloseResultHasEvidence(t *testing.T) {
	if (CloseResult{Message: "words alone"}).HasEvidence() {
		t.Error("a message is not evidence")
	}
	for _, r := range []CloseResult{
		{PRs: []string{"https://a/1"}},
		{Commits: []string{"abc123"}},
		{Docs: []string{"notes/plan.md"}},
		{Tests: []string{"go test ./..."}},
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

	// ...and the task points back at the note.
	if q.meta[ObsidianNoteKey] != bound[0].VaultPath {
		t.Errorf("task metadata = %v, want the vault path", q.meta[ObsidianNoteKey])
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
	if len(bound) != 1 || bound[0].Moved {
		t.Fatalf("bound = %+v, want an in-place binding", bound)
	}
	if bound[0].VaultPath != filepath.Join("Notes", "existing.md") {
		t.Errorf("VaultPath = %q, want the original location", bound[0].VaultPath)
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

	// A closed task must not cite a path inside a disposed worktree.
	closed := q.closes[0]
	if len(closed.Docs) != 1 {
		t.Fatalf("Docs = %v, want one", closed.Docs)
	}
	if !strings.HasPrefix(closed.Docs[0], "Research") {
		t.Errorf("close evidence = %q, want the vault path", closed.Docs[0])
	}
	if !strings.Contains(q.comments[0], "Research") {
		t.Errorf("summary comment should name the final location:\n%s", q.comments[0])
	}
}
