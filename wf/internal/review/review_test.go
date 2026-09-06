package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// taskWithMeta builds a minimal task for BuildInputs tests. ShortID and
// Title are fixed so the derived worktree branch name is deterministic
// across cases.
func taskWithMeta(meta map[string]any) wf.Task {
	if meta == nil {
		meta = map[string]any{}
	}
	return wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser", Meta: meta}
}

func TestResolveLadder(t *testing.T) {
	tests := []struct {
		name string
		in   Inputs
		want Kind
	}{
		{
			name: "PR evidence wins outright",
			in:   Inputs{PR: "https://example.com/pr/12", WorktreeDir: mustExistingDir(t), Branch: "wf/x", BranchExists: true, Note: "n.md"},
			want: KindPR,
		},
		{
			name: "worktree still on disk, no PR",
			in:   Inputs{WorktreeDir: mustExistingDir(t), Branch: "wf/x", BranchExists: true, Note: "n.md"},
			want: KindWorktree,
		},
		{
			name: "disposed worktree falls through to a surviving branch",
			in:   Inputs{WorktreeDir: mustMissingDir(t), Branch: "wf/x", BranchExists: true, Note: "n.md"},
			want: KindBranch,
		},
		{
			name: "no worktree directory recorded at all falls through",
			in:   Inputs{Branch: "wf/x", BranchExists: true, Note: "n.md"},
			want: KindBranch,
		},
		{
			name: "branch gone too, only a bound note remains",
			in:   Inputs{WorktreeDir: mustMissingDir(t), Branch: "wf/x", BranchExists: false, Note: "n.md"},
			want: KindDoc,
		},
		{
			name: "nothing at all",
			in:   Inputs{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.in)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("Resolve() = %+v, want ErrNoTarget", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if got.Kind != tt.want {
				t.Errorf("Resolve() kind = %q, want %q", got.Kind, tt.want)
			}
		})
	}
}

func TestResolvePRTargetArgs(t *testing.T) {
	got, err := Resolve(Inputs{PR: "https://example.com/pr/12", Repo: "/repo", Branch: "wf/x", Base: "main"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(got.Args) != 2 || got.Args[0] != "--pr" || got.Args[1] != "https://example.com/pr/12" {
		t.Errorf("Args = %v, want --pr <url>", got.Args)
	}
	if got.Repo != "/repo" {
		t.Errorf("Repo = %q, want the repo cwd", got.Repo)
	}
	// Branch and base are informational even when a PR resolved the target.
	if got.Branch != "wf/x" || got.Base != "main" {
		t.Errorf("Branch/Base = %q/%q, want them carried through", got.Branch, got.Base)
	}
}

func TestResolveWorktreeTargetArgs(t *testing.T) {
	dir := mustExistingDir(t)
	got, err := Resolve(Inputs{WorktreeDir: dir})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Repo != dir {
		t.Errorf("Repo = %q, want the worktree directory", got.Repo)
	}
	if len(got.Args) != 2 || got.Args[0] != "." || got.Args[1] != "--include-untracked" {
		t.Errorf("Args = %v, want [. --include-untracked]", got.Args)
	}
}

func TestResolveBranchTargetArgs(t *testing.T) {
	got, err := Resolve(Inputs{Repo: "/repo", Branch: "wf/task-1", BranchExists: true, Base: "main"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := []string{"wf/task-1", "main", "--merge-base"}
	if len(got.Args) != 3 || got.Args[0] != want[0] || got.Args[1] != want[1] || got.Args[2] != want[2] {
		t.Errorf("Args = %v, want %v", got.Args, want)
	}
}

func TestResolveDocTarget(t *testing.T) {
	got, err := Resolve(Inputs{Note: "Research/plan.md"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Kind != KindDoc || got.Note != "Research/plan.md" {
		t.Errorf("Resolve() = %+v", got)
	}
	if got.Repo != "" || len(got.Args) != 0 {
		t.Errorf("a doc target must open nothing: %+v", got)
	}
}

func TestBuildInputsPrefersTaskRepoOverConfig(t *testing.T) {
	task := taskWithMeta(map[string]any{
		"wf.repo": "/from/task",
	})
	cfg := &config.Config{Repo: "/from/config"}

	in := BuildInputs(context.Background(), task, cfg, nil)
	if in.Repo != "/from/task" {
		t.Errorf("Repo = %q, want the task's own wf.repo to win", in.Repo)
	}
}

func TestBuildInputsFallsBackToConfigRepo(t *testing.T) {
	task := taskWithMeta(nil)
	cfg := &config.Config{Repo: "/from/config"}

	in := BuildInputs(context.Background(), task, cfg, nil)
	if in.Repo != "/from/config" {
		t.Errorf("Repo = %q, want config.Repo as the fallback", in.Repo)
	}
}

func TestBuildInputsUsesBranchCheckerAgainstDerivedBranch(t *testing.T) {
	task := taskWithMeta(nil)
	cfg := &config.Config{Repo: "/repo"}

	var gotRepo, gotBranch string
	checker := func(_ context.Context, repo, branch string) bool {
		gotRepo, gotBranch = repo, branch
		return true
	}

	in := BuildInputs(context.Background(), task, cfg, checker)
	if !in.BranchExists {
		t.Error("BranchExists = false, want the checker's true to carry through")
	}
	if gotRepo != "/repo" {
		t.Errorf("checker got repo = %q, want config.Repo", gotRepo)
	}
	if gotBranch != in.Branch {
		t.Errorf("checker got branch = %q, want the derived %q", gotBranch, in.Branch)
	}
}

func mustExistingDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func mustMissingDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}
