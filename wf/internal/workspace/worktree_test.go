package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func TestWorktreeName(t *testing.T) {
	tests := []struct {
		name string
		task wf.Task
		want string
	}{
		{"short id and title", wf.Task{ShortID: "abc4", Title: "Add the parser"}, "abc4-add-the-parser"},
		{"punctuation stripped", wf.Task{ShortID: "abc4", Title: "Fix: it's broken!"}, "abc4-fix-it-s-broken"},
		{"no title", wf.Task{ShortID: "abc4"}, "abc4"},
		{"falls back to id", wf.Task{ID: "01HZ", Title: "Work"}, "01hz-work"},
		{"long title truncated", wf.Task{ShortID: "abc4", Title: strings.Repeat("verylongword ", 10)}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WorktreeName(tt.task)
			if tt.want != "" && got != tt.want {
				t.Errorf("WorktreeName() = %q, want %q", got, tt.want)
			}
			if len(got) > 50 {
				t.Errorf("WorktreeName() = %q, too long for a directory name", got)
			}
			if strings.ContainsAny(got, "/ :!'") {
				t.Errorf("WorktreeName() = %q contains unsafe characters", got)
			}
		})
	}
}

// initRepo builds a throwaway git repository with one commit.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "wf@example.com"},
		{"config", "user.name", "wf"},
	} {
		if _, err := runGit(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "commit", "-m", "seed"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestProviderCreateAndDispose(t *testing.T) {
	repo := initRepo(t)
	root := t.TempDir()
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: filepath.Join(root, "worktrees")}
	task := wf.Task{ShortID: "abc4", Title: "Add the parser"}

	space, err := p.Create(ctx, task, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(space.Path(), "README.md")); err != nil {
		t.Errorf("worktree does not contain the repo contents: %v", err)
	}

	// The branch is namespaced so worktree branches are obvious in the repo.
	wt, ok := space.(*Worktree)
	if !ok {
		t.Fatalf("Create() returned %T, want *Worktree", space)
	}
	if !strings.HasPrefix(wt.Branch(), "wf/") {
		t.Errorf("Branch() = %q, want a wf/ prefix", wt.Branch())
	}

	if err := space.Dispose(ctx); err != nil {
		t.Fatalf("Dispose() error = %v", err)
	}
	if _, err := os.Stat(space.Path()); !os.IsNotExist(err) {
		t.Error("Dispose() left the worktree on disk")
	}
	// The branch outlives the checkout: it is what the next run on the
	// task picks up, and what a pull request was pushed from.
	if !BranchExists(ctx, repo, wt.Branch()) {
		t.Errorf("Dispose() deleted branch %q", wt.Branch())
	}
}

// A task runs under several workflows over its life. A later run asked to
// continue an earlier run's branch checks that branch out rather than
// cutting a new one, in a directory of its own.
func TestProviderReusesTheBranchALaterRunIsHanded(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: t.TempDir()}
	task := wf.Task{ShortID: "abc4", Title: "Fix the bug"}

	first, err := p.Create(ctx, task, "")
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if err := first.Dispose(ctx); err != nil {
		t.Fatalf("Dispose() error = %v", err)
	}

	second, err := p.Create(ctx, task, first.Branch())
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if second.Branch() != first.Branch() {
		t.Errorf("Branch() = %q, want the first run's %q", second.Branch(), first.Branch())
	}
	if _, err := os.Stat(filepath.Join(second.Path(), "README.md")); err != nil {
		t.Errorf("the checkout is not real: %v", err)
	}
	out, err := runGit(ctx, second.Path(), "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || out != first.Branch() {
		t.Errorf("HEAD = %q (%v), want %q", out, err, first.Branch())
	}
}

// An escalated run keeps its checkout, still on the task's branch. The next
// run continues in that checkout rather than failing on git's refusal to
// check one branch out twice — the state left in it is the point.
func TestProviderContinuesInAKeptCheckout(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: t.TempDir()}
	task := wf.Task{ShortID: "abc4", Title: "Stuck work"}

	first, err := p.Create(ctx, task, "")
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	first.(*Worktree).Keep()
	if err := first.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(first.Path(), "half-done.txt")
	if err := os.WriteFile(leftover, []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := p.Create(ctx, task, first.Branch())
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if second.Path() != first.Path() || second.Branch() != first.Branch() {
		t.Fatalf("second = %s on %s, want the kept checkout %s on %s",
			second.Path(), second.Branch(), first.Path(), first.Branch())
	}
	if _, err := os.Stat(leftover); err != nil {
		t.Error("the kept checkout's state must survive being picked back up")
	}
	// And a clean finish this time disposes it as usual.
	if err := second.Dispose(ctx); err != nil {
		t.Fatalf("Dispose() error = %v", err)
	}
	if _, err := os.Stat(second.Path()); !os.IsNotExist(err) {
		t.Error("Dispose() left the continued checkout on disk")
	}
}

// A branch hint that names nothing in the repo is not an error: the run
// starts fresh, as a first run would.
func TestProviderFallsBackWhenTheHandedBranchIsGone(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: t.TempDir()}
	space, err := p.Create(ctx, wf.Task{ShortID: "abc4", Title: "Work"}, "wf/nope")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if space.Branch() == "wf/nope" || !strings.HasPrefix(space.Branch(), "wf/") {
		t.Errorf("Branch() = %q, want a fresh wf/ branch", space.Branch())
	}
}

func TestProviderNeverReusesAnExistingDirectory(t *testing.T) {
	repo := initRepo(t)
	root := t.TempDir()
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: root}
	task := wf.Task{ShortID: "abc4", Title: "Add the parser"}

	taken := filepath.Join(root, WorktreeName(task))
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(taken, "someone-elses-work")
	if err := os.WriteFile(marker, []byte("do not touch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Handing an agent someone else's checkout is the failure this package
	// exists to prevent. Taking the next free name is how that is avoided
	// without also refusing to run.
	space, err := p.Create(ctx, task, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if space.Path() == taken {
		t.Fatal("Create() reused an existing directory")
	}
	if body, err := os.ReadFile(marker); err != nil || string(body) != "do not touch\n" {
		t.Errorf("Create() disturbed the existing checkout: %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(space.Path(), "README.md")); err != nil {
		t.Errorf("the new worktree is not a real checkout: %v", err)
	}
}

func TestProviderGivesARerunItsOwnCheckoutAndBranch(t *testing.T) {
	// A task whose escalated run left its checkout on disk still has to be
	// re-runnable: the name is derived from the task, so a re-run collides
	// with its own predecessor, and refusing would make the evidence a
	// deliberate keep produced into a reason the task can never run again.
	repo := initRepo(t)
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: t.TempDir()}
	task := wf.Task{ShortID: "abc4", Title: "Ambiguous work"}

	first, err := p.Create(ctx, task, "")
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	second, err := p.Create(ctx, task, "")
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}

	if first.Path() == second.Path() {
		t.Fatal("a re-run got the same checkout as its predecessor")
	}
	if first.Branch() == second.Branch() {
		t.Fatal("a re-run got the same branch as its predecessor")
	}
	// The first checkout is untouched and still on its own branch.
	if _, err := os.Stat(first.Path()); err != nil {
		t.Errorf("the first checkout was destroyed: %v", err)
	}
	if !BranchExists(ctx, repo, first.Branch()) {
		t.Errorf("the first run's branch %q is gone", first.Branch())
	}
	if !BranchExists(ctx, repo, second.Branch()) {
		t.Errorf("the re-run's branch %q was never created", second.Branch())
	}
}

func TestProviderSkipsANameWhoseBranchOutlivedItsCheckout(t *testing.T) {
	// A branch outlives its checkout by design. `git worktree add -b`
	// refuses a name that is already a ref, so when no branch is handed in,
	// a free directory is not on its own a free name.
	repo := initRepo(t)
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: t.TempDir()}
	task := wf.Task{ShortID: "abc4", Title: "Work"}

	first, err := p.Create(ctx, task, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	branch := first.Branch()
	if err := first.Dispose(ctx); err != nil {
		t.Fatal(err)
	}

	second, err := p.Create(ctx, task, "")
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if second.Branch() == branch {
		t.Errorf("Create() reused branch %q, which still exists", branch)
	}
}

func TestKeepSurvivesDispose(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: t.TempDir()}
	space, err := p.Create(ctx, wf.Task{ShortID: "abc4", Title: "Escalated work"}, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// An escalated run leaves its checkout for a human to open.
	space.(*Worktree).Keep()
	if err := space.Dispose(ctx); err != nil {
		t.Fatalf("Dispose() error = %v", err)
	}
	if _, err := os.Stat(space.Path()); err != nil {
		t.Error("a kept worktree must survive Dispose")
	}
}

func TestProviderRequiresConfiguration(t *testing.T) {
	ctx := context.Background()
	if _, err := (&Provider{Root: t.TempDir()}).Create(ctx, wf.Task{ShortID: "a"}, ""); err == nil {
		t.Error("Create() without a repo must fail")
	}
	if _, err := (&Provider{Repo: t.TempDir()}).Create(ctx, wf.Task{ShortID: "a"}, ""); err == nil {
		t.Error("Create() without a root must fail")
	}
}

func TestBranchExists(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	if BranchExists(ctx, repo, "no-such-branch") {
		t.Error("BranchExists() = true for a branch that was never created")
	}
	if _, err := runGit(ctx, repo, "branch", "wf/task-1"); err != nil {
		t.Fatal(err)
	}
	if !BranchExists(ctx, repo, "wf/task-1") {
		t.Error("BranchExists() = false for a branch that exists")
	}
}
