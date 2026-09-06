package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// taskWithMeta builds a minimal task for BuildLadder tests. ShortID and
// Title are fixed so the derived worktree branch name is deterministic
// across cases.
func taskWithMeta(meta map[string]any) wf.Task {
	if meta == nil {
		meta = map[string]any{}
	}
	return wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser", Meta: meta}
}

func bind(kind wf.Kind, ref string) wf.Binding {
	return wf.Binding{Kind: kind, Ref: ref}
}

func TestResolveLadder(t *testing.T) {
	tests := []struct {
		name string
		bs   wf.Bindings
		ex   Externals
		want Kind
	}{
		{
			name: "PR evidence wins outright",
			bs: wf.Bindings{
				bind(wf.KindPR, "https://example.com/pr/12"),
				bind(wf.KindWorkspace, mustExistingDir(t)),
				bind(wf.KindDoc, "n.md"),
			},
			ex:   Externals{Branch: "wf/x", BranchExists: true},
			want: KindPR,
		},
		{
			name: "workspace still on disk, no PR",
			bs: wf.Bindings{
				bind(wf.KindWorkspace, mustExistingDir(t)),
				bind(wf.KindDoc, "n.md"),
			},
			ex:   Externals{Branch: "wf/x", BranchExists: true},
			want: KindWorktree,
		},
		{
			name: "disposed workspace falls through to a surviving branch",
			bs: wf.Bindings{
				bind(wf.KindWorkspace, mustMissingDir(t)),
				bind(wf.KindDoc, "n.md"),
			},
			ex:   Externals{Branch: "wf/x", BranchExists: true},
			want: KindBranch,
		},
		{
			name: "no workspace binding at all falls through",
			bs:   wf.Bindings{bind(wf.KindDoc, "n.md")},
			ex:   Externals{Branch: "wf/x", BranchExists: true},
			want: KindBranch,
		},
		{
			// A binding a later run replaced is still recorded and its
			// checkout may still be on disk — it is simply no longer the
			// one to open.
			name: "a superseded workspace is not the one to open",
			bs: wf.Bindings{
				wf.Binding{Kind: wf.KindWorkspace, Ref: mustExistingDir(t), State: wf.BindingSuperseded},
				bind(wf.KindDoc, "n.md"),
			},
			ex:   Externals{Branch: "wf/x", BranchExists: true},
			want: KindBranch,
		},
		{
			name: "branch gone too, only a produced document remains",
			bs: wf.Bindings{
				bind(wf.KindWorkspace, mustMissingDir(t)),
				bind(wf.KindDoc, "n.md"),
			},
			ex:   Externals{Branch: "wf/x"},
			want: KindDoc,
		},
		{
			name: "nothing at all",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.bs, tt.ex)
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
	bs := wf.Bindings{bind(wf.KindRepo, "/repo"), bind(wf.KindPR, "https://example.com/pr/12")}

	got, err := Resolve(bs, Externals{Branch: "wf/x", Base: "main"})
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

// The PR array is recorded in report order and carries no timestamps, so
// the first one reported is the one to open.
func TestResolveTakesTheFirstRecordedPR(t *testing.T) {
	bs := wf.Bindings{bind(wf.KindPR, "https://a/1"), bind(wf.KindPR, "https://a/2")}

	got, err := Resolve(bs, Externals{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.PR != "https://a/1" {
		t.Errorf("PR = %q, want the first recorded one", got.PR)
	}
}

func TestResolveWorktreeTargetArgs(t *testing.T) {
	dir := mustExistingDir(t)

	got, err := Resolve(wf.Bindings{bind(wf.KindWorkspace, dir)}, Externals{})
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
	got, err := Resolve(nil, Externals{Repo: "/repo", Branch: "wf/task-1", BranchExists: true, Base: "main"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := []string{"wf/task-1", "main", "--merge-base"}
	if len(got.Args) != 3 || got.Args[0] != want[0] || got.Args[1] != want[1] || got.Args[2] != want[2] {
		t.Errorf("Args = %v, want %v", got.Args, want)
	}
}

func TestResolveDocTarget(t *testing.T) {
	got, err := Resolve(wf.Bindings{bind(wf.KindDoc, "Research/plan.md")}, Externals{})
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

func TestBuildLadderPrefersTaskRepoOverConfig(t *testing.T) {
	task := taskWithMeta(map[string]any{
		"wf.repo": "/from/task",
	})
	cfg := &config.Config{Repo: "/from/config"}

	bs, _ := BuildLadder(context.Background(), task, nil, cfg, nil)
	got, err := Resolve(bs, Externals{Repo: "/from/config", Branch: "wf/x", BranchExists: true})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Repo != "/from/task" {
		t.Errorf("Repo = %q, want the task's own repo binding to win", got.Repo)
	}
}

func TestBuildLadderFallsBackToConfigRepo(t *testing.T) {
	cfg := &config.Config{Repo: "/from/config"}

	bs, ex := BuildLadder(context.Background(), taskWithMeta(nil), nil, cfg, nil)
	if ex.Repo != "/from/config" {
		t.Errorf("Externals.Repo = %q, want config.Repo as the fallback", ex.Repo)
	}
	ex.BranchExists = true

	got, err := Resolve(bs, ex)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Repo != "/from/config" {
		t.Errorf("Repo = %q, want the config fallback when no repo binding exists", got.Repo)
	}
}

func TestBuildLadderUsesBranchCheckerAgainstDerivedBranch(t *testing.T) {
	cfg := &config.Config{Repo: "/repo"}

	var gotRepo, gotBranch string
	checker := func(_ context.Context, repo, branch string) bool {
		gotRepo, gotBranch = repo, branch
		return true
	}

	_, ex := BuildLadder(context.Background(), taskWithMeta(nil), nil, cfg, checker)
	if !ex.BranchExists {
		t.Error("BranchExists = false, want the checker's true to carry through")
	}
	if gotRepo != "/repo" {
		t.Errorf("checker got repo = %q, want config.Repo", gotRepo)
	}
	if gotBranch != ex.Branch {
		t.Errorf("checker got branch = %q, want the derived %q", gotBranch, ex.Branch)
	}
}

// A workspace is machine-local, so it comes off the ledger's own bindings
// rather than the task's tracker metadata — the ladder still resolves it
// end to end once the caller passes those in.
func TestBuildLadderResolvesFromLedgerWorkspace(t *testing.T) {
	dir := mustExistingDir(t)
	task := taskWithMeta(nil)
	local := wf.Bindings{{Kind: wf.KindWorkspace, Ref: dir, State: wf.BindingLive}}

	bs, ex := BuildLadder(context.Background(), task, local, &config.Config{Repo: "/repo"}, nil)
	got, err := Resolve(bs, ex)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Kind != KindWorktree || got.Repo != dir {
		t.Errorf("Resolve() = %+v, want the recorded workspace", got)
	}
}

// A superseded workspace must not be pulled in — only the live one is.
func TestBuildLadderIgnoresSupersededLedgerWorkspace(t *testing.T) {
	dir := mustExistingDir(t)
	task := taskWithMeta(nil)
	local := wf.Bindings{{Kind: wf.KindWorkspace, Ref: dir, State: wf.BindingSuperseded}}

	bs, ex := BuildLadder(context.Background(), task, local, &config.Config{Repo: "/repo", Base: "main"}, nil)
	if _, err := Resolve(bs, ex); err == nil {
		t.Error("Resolve() succeeded, want ErrNoTarget — a superseded workspace must not be current")
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
