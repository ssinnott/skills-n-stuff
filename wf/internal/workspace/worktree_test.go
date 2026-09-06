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
		if _, err := runGit(ctx, "git", dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, "git", dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, "git", dir, "commit", "-m", "seed"); err != nil {
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

	space, err := p.Create(ctx, task)
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
	space, err := p.Create(ctx, task)
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

	first, err := p.Create(ctx, task)
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	second, err := p.Create(ctx, task)
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
	if !BranchExists(ctx, "", repo, first.Branch()) {
		t.Errorf("the first run's branch %q is gone", first.Branch())
	}
	if !BranchExists(ctx, "", repo, second.Branch()) {
		t.Errorf("the re-run's branch %q was never created", second.Branch())
	}
}

func TestProviderSkipsANameWhoseBranchOutlivedItsCheckout(t *testing.T) {
	// Dispose removes the branch best effort, so a branch can outlive the
	// directory. `git worktree add -b` refuses a name that is already a ref,
	// so a free directory is not on its own a free name.
	repo := initRepo(t)
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: t.TempDir()}
	task := wf.Task{ShortID: "abc4", Title: "Work"}

	first, err := p.Create(ctx, task)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	branch := first.Branch()
	// Remove the checkout but leave the branch, which is what a failed
	// `branch -D` during Dispose leaves behind.
	if _, err := runGit(ctx, "git", repo, "worktree", "remove", "--force", first.Path()); err != nil {
		t.Fatal(err)
	}

	second, err := p.Create(ctx, task)
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
	space, err := p.Create(ctx, wf.Task{ShortID: "abc4", Title: "Escalated work"})
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
	if _, err := (&Provider{Root: t.TempDir()}).Create(ctx, wf.Task{ShortID: "a"}); err == nil {
		t.Error("Create() without a repo must fail")
	}
	if _, err := (&Provider{Repo: t.TempDir()}).Create(ctx, wf.Task{ShortID: "a"}); err == nil {
		t.Error("Create() without a root must fail")
	}
}

func TestBranchExists(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	if BranchExists(ctx, "git", repo, "no-such-branch") {
		t.Error("BranchExists() = true for a branch that was never created")
	}
	if _, err := runGit(ctx, "git", repo, "branch", "wf/task-1"); err != nil {
		t.Fatal(err)
	}
	if !BranchExists(ctx, "git", repo, "wf/task-1") {
		t.Error("BranchExists() = false for a branch that exists")
	}
}

func TestIsRepo(t *testing.T) {
	if !IsRepo(context.Background(), "git", initRepo(t)) {
		t.Error("IsRepo() = false for a real repository")
	}
	if IsRepo(context.Background(), "git", t.TempDir()) {
		t.Error("IsRepo() = true for a plain directory")
	}
}
