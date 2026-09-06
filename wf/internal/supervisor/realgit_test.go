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
// claim stage 3 makes about a checkout — that a re-run gets its own, that
// the branch on the binding is a ref that actually exists, that an escalated
// run's evidence is still there afterwards — is a claim about git, and git
// is installed everywhere the tests run while kata is not. What only the
// kata suite can prove is that the tracker publication still lands, which is
// what it checks.

func realGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no shell available")
	}
}

// gitHarness is the dispatch loop with everything real except the queue.
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
			Queue:  q,
			Runner: &runner.Pi{Bin: agent, SessionRoot: cfg.SessionRoot},
			Config: cfg,
			Store:  ledger,
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

func TestRealGitRerunKeepsTheEscalatedCheckout(t *testing.T) {
	// The bug the whole design exists to fix, against real worktrees. The
	// first run escalates and its checkout is kept because that checkout is
	// the evidence; the re-run must get its own and leave that one alone.
	h := newGitHarness(t, wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Ambiguous work"}, stuckStub)
	ctx := context.Background()

	first, err := h.sup.RunOnce(ctx, "")
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if !first.Applied.Escalated {
		t.Fatalf("Applied = %+v, want escalated", first.Applied)
	}

	h.setAgent(t, shippingStub)
	// By ref, the way `wf run --ref` does: the task is flagged for a human,
	// so the ready queue will not offer it up again on its own.
	second, err := h.sup.RunOnce(ctx, "01HZ")
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if !second.Applied.Closed {
		t.Fatalf("Applied = %+v, want the re-run to close", second.Applied)
	}

	rec, err := h.ledger.Load("01HZ")
	if err != nil {
		t.Fatalf("no ledger record: %v", err)
	}
	if len(rec.Runs) != 2 {
		t.Fatalf("runs = %+v, want two", rec.Runs)
	}

	byRun := map[string]wf.Binding{}
	for _, b := range rec.Bindings.ByKind(wf.KindWorkspace) {
		byRun[b.Via] = b
	}
	if len(byRun) != 2 {
		t.Fatalf("workspace bindings = %+v, want one per run", rec.Bindings.ByKind(wf.KindWorkspace))
	}

	kept := byRun[first.Run]
	if kept.State != wf.BindingSuperseded {
		t.Errorf("first checkout state = %q, want superseded", kept.State)
	}
	// Superseded, and every word of that: still on disk, still on its branch,
	// still reachable from the run that made it.
	if _, err := os.Stat(kept.Ref); err != nil {
		t.Errorf("the escalated run's checkout was destroyed: %v", err)
	}
	branch := kept.Get(wf.MetaBranch)
	if branch == "" {
		t.Fatal("the first checkout recorded no branch")
	}
	if !workspace.BranchExists(ctx, "", h.repo, branch) {
		t.Errorf("branch %q, recorded by the first run, is not a ref in the repo", branch)
	}
	if kept.Get(wf.MetaRepo) != h.repo {
		t.Errorf("first checkout repo = %q, want %q", kept.Get(wf.MetaRepo), h.repo)
	}
	if kept.Host != "wf-laptop" {
		t.Errorf("first checkout host = %q", kept.Host)
	}

	fresh := byRun[second.Run]
	if fresh.Ref == kept.Ref {
		t.Fatal("the re-run reused the escalated run's checkout")
	}
	if fresh.Get(wf.MetaBranch) == branch {
		t.Fatal("the re-run reused the escalated run's branch")
	}
	// The re-run closed cleanly, so it removed its own checkout and said so.
	if fresh.State != wf.BindingDisposed {
		t.Errorf("re-run checkout state = %q, want disposed", fresh.State)
	}
	if _, err := os.Stat(fresh.Ref); !os.IsNotExist(err) {
		t.Errorf("a cleanly closed run left its checkout behind: %v", err)
	}
}

func TestRealGitRunSurvivesADeadAgent(t *testing.T) {
	// This agent produced nothing at all, so the record is the only thing
	// that knows the run happened — which is the argument for writing it at
	// spawn rather than at completion.
	h := newGitHarness(t, wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Work that dies"}, dyingStub)
	ctx := context.Background()

	result, err := h.sup.RunOnce(ctx, "")
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
	if run.ID != result.Run || !run.Done() || run.Outcome != wf.SessionEscalated {
		t.Errorf("run = %+v, want %s settled as escalated", run, result.Run)
	}

	// The session file it never wrote anything useful into is still bound
	// and still openable.
	session, ok := rec.Bindings.Current(wf.KindSession)
	if !ok || session.Via != run.ID {
		t.Fatalf("session binding = %+v", session)
	}
	if session.Get(wf.MetaRunner) != "pi" {
		t.Errorf("runner = %q, want the runner as a value on the binding", session.Get(wf.MetaRunner))
	}
	if session.Host != "wf-laptop" {
		t.Errorf("session host = %q", session.Host)
	}

	// And the checkout is live, on a branch that exists.
	space, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok || space.State != wf.BindingLive {
		t.Fatalf("workspace binding = %+v, want it live", space)
	}
	if _, err := os.Stat(space.Ref); err != nil {
		t.Errorf("kept checkout is not on disk: %v", err)
	}
	if !workspace.BranchExists(ctx, "", h.repo, space.Get(wf.MetaBranch)) {
		t.Errorf("recorded branch %q is not a ref", space.Get(wf.MetaBranch))
	}
}

func TestRealGitBranchSurvivesARenamedTask(t *testing.T) {
	// workspace.WorktreeName derives its name from the task's *title*, so
	// before the branch was recorded a rename left the real branch sitting
	// in the repo unreferenced. The binding is what closes that.
	h := newGitHarness(t, wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Original title"}, stuckStub)
	ctx := context.Background()

	result, err := h.sup.RunOnce(ctx, "")
	if err != nil {
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
	if workspace.BranchExists(ctx, "", h.repo, derived) {
		t.Errorf("derived branch %q should not exist after a rename", derived)
	}
	if !workspace.BranchExists(ctx, "", h.repo, recorded) {
		t.Errorf("branch %q recorded by run %s is gone", recorded, result.Run)
	}
}
