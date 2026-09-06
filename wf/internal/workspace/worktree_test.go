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

func TestProviderRefusesExistingDirectory(t *testing.T) {
	repo := initRepo(t)
	root := t.TempDir()
	ctx := context.Background()

	p := &Provider{Repo: repo, Root: root}
	task := wf.Task{ShortID: "abc4", Title: "Add the parser"}

	if err := os.MkdirAll(filepath.Join(root, WorktreeName(task)), 0o755); err != nil {
		t.Fatal(err)
	}

	// Handing an agent someone else's checkout is worse than refusing.
	if _, err := p.Create(ctx, task); err == nil {
		t.Error("Create() reused an existing directory")
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

func TestIsRepo(t *testing.T) {
	if !IsRepo(context.Background(), "git", initRepo(t)) {
		t.Error("IsRepo() = false for a real repository")
	}
	if IsRepo(context.Background(), "git", t.TempDir()) {
		t.Error("IsRepo() = true for a plain directory")
	}
}
