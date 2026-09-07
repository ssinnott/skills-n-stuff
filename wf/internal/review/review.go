// Package review resolves a task to a PR, workspace, branch, or document to
// look at, and drives difit as the viewer. See DESIGN.md.
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

// ErrNoTarget is returned when no rung matches.
var ErrNoTarget = errors.New("review: nothing to show — no PR, worktree, branch, or bound note")

// Target is what the ladder resolved to review, before a viewer is spawned.
type Target struct {
	Kind Kind
	// Repo is the directory difit runs in; empty for KindDoc.
	Repo string
	// Args is difit's target argv: the rung's target plus its flags.
	Args []string
	// Branch and Base are populated whenever known, regardless of rung.
	Branch string
	Base   string
	// PR is set only for KindPR.
	PR string
	// Note is set only for KindDoc: a vault-relative path, not a diff.
	Note string
}

// Externals is what the ladder needs that no binding records.
type Externals struct {
	// Repo is config.repo, used only when no repo binding exists.
	Repo string
	// Branch is the worktree branch this task would have run under.
	Branch string
	// BranchExists is computed by the caller so Resolve never shells out.
	BranchExists bool
	// Base is the branch a surviving branch would diff against.
	Base string
}

// Resolve picks the first matching rung; order is significant, see DESIGN.md.
func Resolve(bs wf.Bindings, ex Externals) (Target, error) {
	repo := ex.Repo
	if b, ok := bs.Current(wf.KindRepo); ok {
		repo = b.Ref
	}

	// The first PR reported, not newest: PR bindings carry no timestamps.
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

// liveWorkspace returns the workspace directory if it's still on disk.
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

// BranchChecker reports whether branch is a ref in repo (fakeable in tests).
type BranchChecker func(ctx context.Context, repo, branch string) bool

// GitBranchChecker is the production BranchChecker.
func GitBranchChecker(ctx context.Context, repo, branch string) bool {
	return workspace.BranchExists(ctx, repo, branch)
}

// BuildLadder assembles the task's bindings and the externals no binding
// holds. The branch is the one the task's last checkout recorded, since a
// title may have changed since; only a task with no checkout on record gets
// the name a fresh run would cut.
func BuildLadder(
	ctx context.Context,
	task wf.Task,
	local wf.Bindings,
	cfg *config.Config,
	branchExists BranchChecker,
) (wf.Bindings, Externals) {
	bs := append(wf.LoadBindings(task), local.Live(wf.KindWorkspace)...)

	ex := Externals{Branch: "wf/" + workspace.WorktreeName(task)}
	if last, ok := local.Last(wf.KindWorkspace); ok && last.Get(wf.MetaBranch) != "" {
		ex.Branch = last.Get(wf.MetaBranch)
	}
	if cfg != nil {
		ex.Repo = cfg.Repo
		ex.Base = cfg.Base
	}
	if ex.Base == "" {
		ex.Base = "main"
	}

	repo := ex.Repo
	if b, ok := bs.Current(wf.KindRepo); ok {
		repo = b.Ref
	}
	if branchExists != nil && repo != "" {
		ex.BranchExists = branchExists(ctx, repo, ex.Branch)
	}

	return bs, ex
}
