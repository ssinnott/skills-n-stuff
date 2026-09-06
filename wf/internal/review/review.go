// Package review resolves a task to something a human can look at, and
// drives difit as the surface that shows it.
//
// The seam this package owns is the target ladder: which of a task's
// bindings — a pull request, a live workspace, a surviving branch, a
// produced document — is the right thing to open, and in what order. The
// ladder is a query over what the task already records; nothing here
// invents state the rest of wf does not already keep.
package review

import (
	"context"
	"errors"
	"os"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workspace"
)

// Kind names which rung of the ladder a target resolved to.
type Kind string

const (
	KindPR       Kind = "pr"
	KindWorktree Kind = "worktree"
	KindBranch   Kind = "branch"
	KindDoc      Kind = "doc"
)

// ErrNoTarget is returned when none of the ladder's rungs match — a task
// with no repo, no worktree, no branch and no bound note has nothing wf
// review can show.
var ErrNoTarget = errors.New("review: nothing to show — no PR, worktree, branch, or bound note")

// Target is what the ladder resolved to review, before a viewer is spawned.
type Target struct {
	Kind Kind
	// Repo is the directory difit runs in. Empty for KindDoc, which opens
	// nothing.
	Repo string
	// Args is difit's target argv: positional target plus any flags the
	// rung requires (--pr, --include-untracked, --merge-base).
	Args []string
	// Branch and Base are populated whenever known, independent of which
	// rung matched — a PR resolution still names the branch it came from.
	Branch string
	Base   string
	// PR is set only for KindPR.
	PR string
	// Note is set only for KindDoc: a vault-relative path, not a diff.
	Note string
}

// Externals is what the ladder needs that no binding records. Everything
// else Resolve reads off the task's bindings; these three facts are here
// because the task genuinely does not hold them yet:
//
//   - the branch is derived from the task's title rather than recorded by
//     the run that created it, which is the bug DESIGN-task.md names;
//   - whether that branch survives is a git question, and Resolve stays
//     pure so its selection logic is table-testable;
//   - repo and base fall back to config when the task never named one.
type Externals struct {
	// Repo is config.repo, used only when no repo binding exists.
	Repo string
	// Branch is the worktree branch this task would have run under.
	Branch string
	// BranchExists reports whether Branch is still a ref in the repo.
	// Computed by the caller (real git, or a test double) rather than by
	// Resolve, so Resolve itself never shells out.
	BranchExists bool
	// Base is the branch a surviving branch would diff against.
	Base string
}

// Resolve picks the first matching rung. Order is significant and mirrors
// what a human actually wants to see: a shipped PR is the truth once one
// exists; a live workspace is the strongest evidence for a run that has not
// shipped, because it still holds the agent's untracked and uncommitted
// state that a branch alone would lose; a pushed branch is the fallback
// once that checkout is gone; a produced document is what is left when
// there was never a diff at all.
func Resolve(bs wf.Bindings, ex Externals) (Target, error) {
	repo := ex.Repo
	if b, ok := bs.Current(wf.KindRepo); ok {
		repo = b.Ref
	}

	// The first PR reported, not the newest: PR bindings come from a flat
	// array recorded in report order and carry no timestamps, so report
	// order is the only ordering that exists here.
	if prs := bs.Live(wf.KindPR); len(prs) > 0 {
		return Target{
			Kind: KindPR, Repo: repo, Branch: ex.Branch, Base: ex.Base, PR: prs[0].Ref,
			Args: []string{"--pr", prs[0].Ref},
		}, nil
	}

	if dir := liveWorkspace(bs); dir != "" {
		return Target{
			Kind: KindWorktree, Repo: dir, Branch: ex.Branch, Base: ex.Base,
			Args: []string{".", "--include-untracked"},
		}, nil
	}

	if ex.Branch != "" && ex.BranchExists {
		return Target{
			Kind: KindBranch, Repo: repo, Branch: ex.Branch, Base: ex.Base,
			Args: []string{ex.Branch, ex.Base, "--merge-base"},
		}, nil
	}

	if doc, ok := bs.Current(wf.KindDoc); ok {
		return Target{Kind: KindDoc, Note: doc.Ref}, nil
	}

	return Target{}, ErrNoTarget
}

// liveWorkspace returns the current workspace directory if it is still on
// disk. This is rung 2's own check: a disposed worktree must fall through
// to the next rung rather than handing difit a directory that is no longer
// there. Nothing records disposal today, so the stat is the record — which
// is the seam the ledger closes by refreshing the binding's state instead.
func liveWorkspace(bs wf.Bindings) string {
	b, ok := bs.Current(wf.KindWorkspace)
	if !ok || b.Ref == "" {
		return ""
	}
	info, err := os.Stat(b.Ref)
	if err != nil || !info.IsDir() {
		return ""
	}
	return b.Ref
}

// BranchChecker reports whether branch is still a ref in repo. The real
// implementation shells to git (workspace.BranchExists); tests fake it so
// the ladder never needs a repository on disk.
type BranchChecker func(ctx context.Context, repo, branch string) bool

// GitBranchChecker is the production BranchChecker.
func GitBranchChecker(ctx context.Context, repo, branch string) bool {
	return workspace.BranchExists(ctx, "", repo, branch)
}

// BuildLadder assembles both halves of a resolution: the task's bindings,
// and the few facts about the world that no binding holds. It invents
// nothing that is not already recorded somewhere.
//
// local is the task's ledger bindings: a workspace is machine-local and was
// never on the tracker to begin with, so LoadBindings(task) alone has no
// rung 2 to offer. Only the live workspace bindings are pulled in — the PR
// and doc rungs come from kata, which is their only home.
func BuildLadder(
	ctx context.Context,
	task wf.Task,
	local wf.Bindings,
	cfg *config.Config,
	branchExists BranchChecker,
) (wf.Bindings, Externals) {
	bs := append(wf.LoadBindings(task), local.Live(wf.KindWorkspace)...)

	ex := Externals{Branch: "wf/" + workspace.WorktreeName(task)}
	if cfg != nil {
		ex.Repo = cfg.Repo
		ex.Base = cfg.Base
	}
	if ex.Base == "" {
		ex.Base = "main"
	}

	// The checker needs the repo Resolve will pick, which is the task's
	// own binding where it has one.
	repo := ex.Repo
	if b, ok := bs.Current(wf.KindRepo); ok {
		repo = b.Ref
	}
	if branchExists != nil && repo != "" {
		ex.BranchExists = branchExists(ctx, repo, ex.Branch)
	}

	return bs, ex
}
