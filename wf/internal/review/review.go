// Package review resolves a task to something a human can look at, and
// drives difit as the surface that shows it.
//
// The seam this package owns is the target ladder: which of a task's
// already-recorded facts — PR evidence, a live worktree, a surviving
// branch, a bound vault note — is the right thing to open, and in what
// order. Every input the ladder reads already exists on the task; nothing
// here invents state the rest of wf does not already keep.
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

// Inputs is everything the ladder needs, already read off the task or
// config by BuildInputs. Resolve is kept pure and synchronous so its
// selection logic is table-testable without a repository on disk.
type Inputs struct {
	// Repo is wf.repo metadata, falling back to config.repo.
	Repo string
	// WorktreeDir is pi.workspace metadata — the directory the task's run
	// used, which may or may not still exist.
	WorktreeDir string
	// Branch is the worktree branch this task would have run under,
	// derived deterministically from the task rather than stored anywhere
	// (see workspace.WorktreeName).
	Branch string
	// BranchExists reports whether Branch is still a ref in Repo. Computed
	// by the caller (real git, or a test double) rather than by Resolve,
	// so Resolve itself never shells out.
	BranchExists bool
	// Base is the branch a surviving branch would diff against.
	Base string
	// PR is the first PR URL recorded on the task, if any.
	PR string
	// Note is obsidian.note metadata.
	Note string
}

// Resolve picks the first matching rung. Order is significant and mirrors
// what a human actually wants to see: a shipped PR is the truth once one
// exists; a live worktree is the strongest evidence for a run that has not
// shipped, because it still holds the agent's untracked and uncommitted
// state that a branch alone would lose; a pushed branch is the fallback
// once that checkout is gone; a bound note is what is left when there was
// never a diff at all.
func Resolve(in Inputs) (Target, error) {
	switch {
	case in.PR != "":
		return Target{
			Kind: KindPR, Repo: in.Repo, Branch: in.Branch, Base: in.Base, PR: in.PR,
			Args: []string{"--pr", in.PR},
		}, nil

	case in.WorktreeDir != "" && dirExists(in.WorktreeDir):
		return Target{
			Kind: KindWorktree, Repo: in.WorktreeDir, Branch: in.Branch, Base: in.Base,
			Args: []string{".", "--include-untracked"},
		}, nil

	case in.Branch != "" && in.BranchExists:
		return Target{
			Kind: KindBranch, Repo: in.Repo, Branch: in.Branch, Base: in.Base,
			Args: []string{in.Branch, in.Base, "--merge-base"},
		}, nil

	case in.Note != "":
		return Target{Kind: KindDoc, Note: in.Note}, nil

	default:
		return Target{}, ErrNoTarget
	}
}

// dirExists is rung 2's own check: a disposed worktree must fall through to
// the next rung rather than handing difit a directory that is no longer
// there.
func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// BranchChecker reports whether branch is still a ref in repo. The real
// implementation shells to git (workspace.BranchExists); tests fake it so
// the ladder never needs a repository on disk.
type BranchChecker func(ctx context.Context, repo, branch string) bool

// GitBranchChecker is the production BranchChecker.
func GitBranchChecker(ctx context.Context, repo, branch string) bool {
	return workspace.BranchExists(ctx, "", repo, branch)
}

// BuildInputs reads everything the ladder needs off a task and its config,
// inventing nothing that is not already recorded somewhere.
func BuildInputs(ctx context.Context, task wf.Task, cfg *config.Config, branchExists BranchChecker) Inputs {
	repo := ""
	if v, ok := task.Meta[wf.RepoKey].(string); ok && v != "" {
		repo = v
	} else if cfg != nil {
		repo = cfg.Repo
	}

	worktreeDir := ""
	if binding, ok := wf.BindingFromMeta(task.Meta); ok {
		worktreeDir = binding.Cwd
	}

	branch := "wf/" + workspace.WorktreeName(task)

	base := ""
	if cfg != nil {
		base = cfg.Base
	}
	if base == "" {
		base = "main"
	}

	exists := false
	if branchExists != nil && repo != "" {
		exists = branchExists(ctx, repo, branch)
	}

	note, _ := task.Meta[wf.ObsidianNoteKey].(string)

	prs := wf.PRsFromMeta(task.Meta)
	pr := ""
	if len(prs) > 0 {
		pr = prs[0]
	}

	return Inputs{
		Repo:         repo,
		WorktreeDir:  worktreeDir,
		Branch:       branch,
		BranchExists: exists,
		Base:         base,
		PR:           pr,
		Note:         note,
	}
}
