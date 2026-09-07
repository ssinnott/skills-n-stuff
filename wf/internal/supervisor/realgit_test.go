package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/runner"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workspace"
)

// The same properties integration_test.go proves against a real kata daemon,
// against real git worktrees and a real stub agent process but a fake queue.
//
// The split is deliberate rather than duplication for its own sake. Every
// claim about a checkout — that a later run continues on the branch an
// earlier one left, that the branch on the binding is a ref that actually
// exists, that an escalated run's evidence is still there afterwards — is a
// claim about git, and git is installed everywhere the tests run while kata
// is not. What only the kata suite can prove is that the tracker
// publication still lands, which is what it checks.

func realGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no shell available")
	}
}

// gitHarness is one dispatch with everything real except the queue.
type gitHarness struct {
	sup    *Supervisor
	ledger store.Store
	repo   string
	root   string
	agent  string
	queue  *fakeQueue
}

func newGitHarness(t *testing.T, task wf.Task, script string) *gitHarness {
	t.Helper()
	realGit(t)

	repo := seedRepo(t)
	agent := filepath.Join(t.TempDir(), "stub-agent")
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	q := newQueue(task)
	root := filepath.Join(t.TempDir(), "worktrees")
	cfg := &config.Config{
		Actor:           "wf-laptop",
		SessionRoot:     filepath.Join(t.TempDir(), "sessions"),
		LeaseTTLSeconds: 900,
	}
	ledger := store.New(t.TempDir())

	return &gitHarness{
		queue:  q,
		repo:   repo,
		root:   root,
		agent:  agent,
		ledger: ledger,
		sup: &Supervisor{
			Queue:     q,
			Runner:    &runner.Pi{Bin: agent, SessionRoot: cfg.SessionRoot},
			Workflows: basicFlows(t),
			Config:    cfg,
			Store:     ledger,
			Workspaces: func(workflow.Workflow) wf.WorkspaceProvider {
				return &workspace.Provider{Repo: repo, Root: root}
			},
		},
	}
}

// setAgent rewrites the stub in place, so a re-run can behave differently.
func (h *gitHarness) setAgent(t *testing.T, script string) {
	t.Helper()
	if err := os.WriteFile(h.agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func seedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runs := [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "wf@example.com"},
		{"config", "user.name", "wf"},
	}
	for _, args := range runs {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

const stuckStub = `#!/bin/sh
echo "I need a human decision about which schema to use."
`

const shippingStub = `#!/bin/sh
echo "PR: https://example.com/pr/1 — Add the parser"
echo "DONE Implemented the tolerant separator parser and covered it with tests."
`

const dyingStub = `#!/bin/sh
echo "starting on the parser"
exit 3
`

// leavingStub leaves a file behind, so a later run can prove it continued
// in the same checkout rather than a fresh one.
const leavingStub = `#!/bin/sh
echo "wip" > half-done.txt
echo "I need a human decision about which schema to use."
`

func TestRealGitRerunContinuesInTheEscalatedCheckout(t *testing.T) {
	// The first run escalates and its checkout is kept, because the state in
	// it is what a human, or the re-run, picks up from. The re-run must land
	// in that checkout, finish the work, and dispose of it.
	h := newGitHarness(t, wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Ambiguous work"}, leavingStub)
	ctx := context.Background()

	first, err := run(h.sup, "01HZ")
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if !first.Applied.Escalated {
		t.Fatalf("Applied = %+v, want escalated", first.Applied)
	}
	rec, err := h.ledger.Load("01HZ")
	if err != nil {
		t.Fatalf("no ledger record: %v", err)
	}
	kept, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok || kept.State != wf.BindingLive {
		t.Fatalf("workspace binding = %+v, want it kept live", kept)
	}
	if _, err := os.Stat(filepath.Join(kept.Ref, "half-done.txt")); err != nil {
		t.Fatalf("the escalated run's state is not in its checkout: %v", err)
	}

	h.setAgent(t, "#!/bin/sh\ntest -f half-done.txt || { echo 'lost the earlier state'; exit 1; }\n"+
		"echo 'PR: https://example.com/pr/1 — Add the parser'\n"+
		"echo 'DONE Implemented the tolerant separator parser and covered it with tests.'\n")
	second, err := run(h.sup, "01HZ")
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if !second.Applied.Completed {
		t.Fatalf("Applied = %+v, want the re-run to complete in the kept checkout", second.Applied)
	}

	rec, err = h.ledger.Load("01HZ")
	if err != nil {
		t.Fatalf("no ledger record: %v", err)
	}
	if len(rec.Runs) != 2 {
		t.Fatalf("runs = %+v, want two", rec.Runs)
	}
	spaces := rec.Bindings.ByKind(wf.KindWorkspace)
	if len(spaces) != 1 || spaces[0].Ref != kept.Ref {
		t.Fatalf("workspace bindings = %+v, want the one continued checkout", spaces)
	}
	if spaces[0].Via != rec.Runs[1].ID || spaces[0].State != wf.BindingDisposed {
		t.Errorf("checkout = %+v, want it disposed by the re-run", spaces[0])
	}
	if _, err := os.Stat(kept.Ref); !os.IsNotExist(err) {
		t.Errorf("a completed run should have removed its checkout: %v", err)
	}
	// The branch stays, for whatever the task's next workflow is.
	if !workspace.BranchExists(ctx, h.repo, kept.Get(wf.MetaBranch)) {
		t.Errorf("branch %q was deleted with the checkout", kept.Get(wf.MetaBranch))
	}
}

func TestRealGitLaterRunPicksUpTheBranch(t *testing.T) {
	// The address-comments case: an earlier run completed, pushed a PR from
	// its branch and disposed its checkout. The next run on the task gets a
	// fresh checkout of that same branch.
	h := newGitHarness(t, wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Fix the bug"}, shippingStub)
	ctx := context.Background()

	if _, err := run(h.sup, "01HZ"); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	h.setAgent(t, "#!/bin/sh\ngit rev-parse --abbrev-ref HEAD > branch.txt\n"+
		"echo 'PR: https://example.com/pr/1 — Add the parser'\n"+
		"echo 'DONE Addressed the review comments.'\n")
	h.sup.Workspaces = func(workflow.Workflow) wf.WorkspaceProvider {
		return &workspace.Provider{Repo: h.repo, Root: h.root}
	}
	if _, err := run(h.sup, "01HZ"); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	rec, err := h.ledger.Load("01HZ")
	if err != nil {
		t.Fatal(err)
	}
	// One binding per run, each disposed by its own run. The directory name
	// may well be the same — the first was freed — which is why the record
	// keeps both rather than overwriting one with the other.
	spaces := rec.Bindings.ByKind(wf.KindWorkspace)
	if len(spaces) != 2 {
		t.Fatalf("workspace bindings = %+v, want one per run", spaces)
	}
	for i, b := range spaces {
		if b.Via != rec.Runs[i].ID {
			t.Errorf("checkout %d Via = %q, want run %q", i, b.Via, rec.Runs[i].ID)
		}
	}
	branch := spaces[0].Get(wf.MetaBranch)
	if spaces[1].Get(wf.MetaBranch) != branch {
		t.Errorf("second run's branch = %q, want the first's %q", spaces[1].Get(wf.MetaBranch), branch)
	}
	if !workspace.BranchExists(ctx, h.repo, branch) {
		t.Errorf("branch %q is not a ref", branch)
	}
	for _, b := range spaces {
		if b.State != wf.BindingDisposed {
			t.Errorf("checkout %q state = %q, want disposed", b.Ref, b.State)
		}
	}
}

func TestRealGitRunSurvivesADeadAgent(t *testing.T) {
	// This agent produced nothing at all, so the record is the only thing
	// that knows the run happened — which is the argument for writing it at
	// spawn rather than at completion.
	h := newGitHarness(t, wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Work that dies"}, dyingStub)
	ctx := context.Background()

	result, err := run(h.sup, "01HZ")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Escalated {
		t.Fatalf("Applied = %+v, want escalated", result.Applied)
	}

	rec, err := h.ledger.Load("01HZ")
	if err != nil {
		t.Fatalf("no ledger record: %v", err)
	}
	if len(rec.Runs) != 1 {
		t.Fatalf("runs = %+v, want the dead run recorded", rec.Runs)
	}
	run := rec.Runs[0]
	if !run.Done() || run.Outcome != wf.SessionEscalated {
		t.Errorf("run = %+v, want settled as escalated", run)
	}

	// The session file it never wrote anything useful into is still bound
	// and still openable.
	session, ok := rec.Bindings.Current(wf.KindSession)
	if !ok || session.Via != run.ID {
		t.Fatalf("session binding = %+v", session)
	}

	// And the checkout is live, on a branch that exists.
	space, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok || space.State != wf.BindingLive {
		t.Fatalf("workspace binding = %+v, want it live", space)
	}
	if _, err := os.Stat(space.Ref); err != nil {
		t.Errorf("kept checkout is not on disk: %v", err)
	}
	if !workspace.BranchExists(ctx, h.repo, space.Get(wf.MetaBranch)) {
		t.Errorf("recorded branch %q is not a ref", space.Get(wf.MetaBranch))
	}
}

func TestRealGitBranchSurvivesARenamedTask(t *testing.T) {
	// workspace.WorktreeName derives its name from the task's *title*, so
	// before the branch was recorded a rename left the real branch sitting
	// in the repo unreferenced. The binding is what closes that.
	h := newGitHarness(t, wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Original title"}, stuckStub)
	ctx := context.Background()

	if _, err := run(h.sup, "01HZ"); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	rec, err := h.ledger.Load("01HZ")
	if err != nil {
		t.Fatal(err)
	}
	space, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok {
		t.Fatal("no workspace binding")
	}
	recorded := space.Get(wf.MetaBranch)

	// A human renames the task in the tracker.
	renamed := wf.Task{ID: "01HZ", ShortID: "abc4", Title: "A completely different title"}
	h.queue.tasks["01HZ"].Title = renamed.Title

	// Re-deriving the branch now finds nothing; the recorded one still
	// answers, which is the entire point.
	derived := "wf/" + workspace.WorktreeName(renamed)
	if derived == recorded {
		t.Fatal("the rename did not change what the name rule derives — the test proves nothing")
	}
	if workspace.BranchExists(ctx, h.repo, derived) {
		t.Errorf("derived branch %q should not exist after a rename", derived)
	}
	if !workspace.BranchExists(ctx, h.repo, recorded) {
		t.Errorf("branch %q recorded by run %s is gone", recorded, rec.Runs[0].ID)
	}
}

func TestRealGitNamedWorkflowRunsAndIsRecordedOnTheRun(t *testing.T) {
	// Explicit dispatch against real worktrees. The task's labels route it to
	// `quick`; naming `deep` has to run that recipe instead — prompt, profile
	// and all — and the run has to say which one it was, because "was the
	// other recipe better here" is a question with two rows to compare.
	h := newGitHarness(t, wf.Task{
		ID: "01HZ", ShortID: "abc4", Title: "Ambiguous work", Labels: []string{"code"},
	}, stuckStub)
	h.sup.Workflows = loadFlows(t, map[string]string{
		"quick.md": "---\nname: quick\nprofile: fast\nlabels: code\n---\nQuickly handle {{TASK_TITLE}}\n",
		"deep.md":  "---\nname: deep\nprofile: careful\n---\nThink hard about {{TASK_TITLE}}\n",
	})
	ctx := context.Background()

	if _, err := h.sup.RunOnce(ctx, "01HZ", Dispatch{Workflow: "deep"}); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	rec, err := h.ledger.Load("01HZ")
	if err != nil {
		t.Fatalf("no ledger record: %v", err)
	}
	// kata's ULID is the task's only id; the record is filed under it.
	if rec.ID != "01HZ" {
		t.Error("the record must be keyed by the tracker's own id")
	}
	if len(rec.Runs) != 1 {
		t.Fatalf("runs = %+v, want one", rec.Runs)
	}
	run := rec.Runs[0]
	if run.Workflow != "deep" || run.Profile != "careful" {
		t.Errorf("run = %+v, want the named workflow recorded on it", run)
	}

	// It really ran that recipe, against a checkout git actually made. The
	// stub escalates, so the checkout is kept and the branch is still a ref
	// — the same evidence any other escalated run leaves.
	space, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok || space.Via != run.ID {
		t.Fatalf("workspace binding = %+v", space)
	}
	if _, err := os.Stat(space.Ref); err != nil {
		t.Errorf("the named run's checkout is not on disk: %v", err)
	}
	if !workspace.BranchExists(ctx, h.repo, space.Get(wf.MetaBranch)) {
		t.Errorf("branch %q recorded by the named run is not a ref", space.Get(wf.MetaBranch))
	}
}
