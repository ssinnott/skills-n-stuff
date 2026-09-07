// Package workspace provides isolated git-worktree checkouts for agent
// runs, one worktree per task. See DESIGN.md.
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// branchPrefix namespaces the branches wf creates.
const branchPrefix = "wf/"

// Worktree is a git worktree bound to one task.
type Worktree struct {
	dir    string
	branch string
	repo   string
	// keep suppresses removal on Dispose, for an escalated run's evidence.
	keep bool
}

func (w *Worktree) Path() string   { return w.dir }
func (w *Worktree) Branch() string { return w.branch }

// compile-time proof Worktree satisfies the seam.
var _ wf.Workspace = (*Worktree)(nil)

// Keep marks the worktree to survive Dispose.
func (w *Worktree) Keep() { w.keep = true }

// Dispose removes the checkout unless kept. The branch stays: a task runs
// under several workflows over its life, and the next run picks the branch
// back up — it is what the pull request was pushed from.
func (w *Worktree) Dispose(ctx context.Context) error {
	if w.keep {
		return nil
	}
	if _, err := runGit(ctx, w.repo, "worktree", "remove", "--force", w.dir); err != nil {
		return fmt.Errorf("remove worktree %s: %w", w.dir, err)
	}
	return nil
}

// Provider creates worktrees under Root from the repository at Repo.
type Provider struct {
	// Repo is the repository worktrees are cut from.
	Repo string
	// Root is the directory worktrees are created under.
	Root string
	// Base is the branch or commit a fresh branch starts from; empty means
	// the repo's current HEAD.
	Base string
}

var _ wf.WorkspaceProvider = (*Provider)(nil)

// Create provides a worktree for the task. When branch names a branch an
// earlier run left in the repo, the run continues on it: in the checkout
// that still holds it if one does (an escalated run keeps its checkout, and
// the state in it is what the next run picks up from), otherwise in a new
// checkout of that branch. With no branch to continue, a fresh branch is
// cut from Base in a new directory; a taken name gets the next free
// numeric suffix.
func (p *Provider) Create(ctx context.Context, task wf.Task, branch string) (wf.Workspace, error) {
	if p.Repo == "" {
		return nil, fmt.Errorf("worktree provider: no repository configured")
	}
	if p.Root == "" {
		return nil, fmt.Errorf("worktree provider: no root directory configured")
	}
	if err := os.MkdirAll(p.Root, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree root %s: %w", p.Root, err)
	}

	name := WorktreeName(task)
	if branch != "" && BranchExists(ctx, p.Repo, branch) {
		if dir := checkoutOf(ctx, p.Repo, branch); dir != "" {
			return &Worktree{dir: dir, branch: branch, repo: p.Repo}, nil
		}
		dir, err := p.freeDir(name)
		if err != nil {
			return nil, err
		}
		if out, err := runGit(ctx, p.Repo, "worktree", "add", dir, branch); err != nil {
			return nil, fmt.Errorf("check out %s for %s: %w: %s", branch, task.ShortID, err, out)
		}
		return &Worktree{dir: dir, branch: branch, repo: p.Repo}, nil
	}

	dir, branch, err := p.free(ctx, name)
	if err != nil {
		return nil, err
	}
	args := []string{"worktree", "add", "-b", branch, dir}
	if p.Base != "" {
		args = append(args, p.Base)
	}
	if out, err := runGit(ctx, p.Repo, args...); err != nil {
		return nil, fmt.Errorf("create worktree for %s: %w: %s", task.ShortID, err, out)
	}
	return &Worktree{dir: dir, branch: branch, repo: p.Repo}, nil
}

// maxWorktrees bounds the search for a free name; it is a guard, not a
// cleanup policy.
const maxWorktrees = 50

// free returns the first directory and branch pair that nothing holds. Both
// halves must be free: a branch can outlive its checkout, and
// `git worktree add -b` refuses a name that is already a ref.
func (p *Provider) free(ctx context.Context, base string) (dir, branch string, err error) {
	for n := 1; n <= maxWorktrees; n++ {
		dir = p.candidate(base, n)
		branch = branchPrefix + filepath.Base(dir)
		if _, statErr := os.Stat(dir); statErr == nil {
			continue
		}
		if BranchExists(ctx, p.Repo, branch) {
			continue
		}
		return dir, branch, nil
	}
	return "", "", fmt.Errorf("no free worktree name for %s under %s after %d tries — clean up old checkouts", base, p.Root, maxWorktrees)
}

// freeDir returns the first directory nothing holds, for a checkout of an
// existing branch.
func (p *Provider) freeDir(base string) (string, error) {
	for n := 1; n <= maxWorktrees; n++ {
		dir := p.candidate(base, n)
		if _, err := os.Stat(dir); err != nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no free worktree directory for %s under %s after %d tries — clean up old checkouts", base, p.Root, maxWorktrees)
}

func (p *Provider) candidate(base string, n int) string {
	if base == "" {
		base = "task"
	}
	if n > 1 {
		base = fmt.Sprintf("%s-%d", base, n)
	}
	return filepath.Join(p.Root, base)
}

var unsafeChars = regexp.MustCompile(`[^a-z0-9]+`)

// WorktreeName builds a directory name stable for a task and safe on disk:
// the short id keeps it unique, the slug keeps it readable. A tracker
// rename changes what this returns, so a run records its actual branch
// rather than recomputing it later.
func WorktreeName(task wf.Task) string {
	slug := unsafeChars.ReplaceAllString(strings.ToLower(task.Title), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}

	id := task.ShortID
	if id == "" {
		id = task.ID
	}
	id = unsafeChars.ReplaceAllString(strings.ToLower(id), "-")

	if slug == "" {
		return id
	}
	if id == "" {
		return slug
	}
	return id + "-" + slug
}

// checkoutOf returns the worktree directory that has branch checked out, or
// "" when none does. git refuses a second checkout of one branch, and a
// kept checkout is where the run should continue anyway.
func checkoutOf(ctx context.Context, repo, branch string) string {
	out, err := runGit(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	dir := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			dir = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch:
			if _, err := os.Stat(dir); err == nil {
				return dir
			}
		}
	}
	return ""
}

// BranchExists reports whether branch is a local ref in repo; `wf review`
// uses it to tell an unpushed branch from a merged-and-deleted one.
func BranchExists(ctx context.Context, repo, branch string) bool {
	_, err := runGit(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return strings.TrimSpace(stdout.String()), nil
}
