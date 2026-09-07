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

// Worktree is a git worktree bound to one task.
type Worktree struct {
	dir    string
	branch string
	repo   string
	git    string
	// keep suppresses removal on Dispose, for an escalated run's evidence.
	keep bool
}

func (w *Worktree) Path() string   { return w.dir }
func (w *Worktree) Repo() string   { return w.repo }
func (w *Worktree) Branch() string { return w.branch }

// compile-time proof Worktree satisfies the seam.
var _ wf.Workspace = (*Worktree)(nil)

// Keep marks the worktree to survive Dispose.
func (w *Worktree) Keep() { w.keep = true }

// Dispose removes the worktree and its branch (best effort) unless kept.
func (w *Worktree) Dispose(ctx context.Context) error {
	if w.keep {
		return nil
	}
	if _, err := runGit(ctx, w.git, w.repo, "worktree", "remove", "--force", w.dir); err != nil {
		return fmt.Errorf("remove worktree %s: %w", w.dir, err)
	}
	_, _ = runGit(ctx, w.git, w.repo, "branch", "-D", w.branch)
	return nil
}

// Provider creates worktrees under Root from the repository at Repo.
type Provider struct {
	// Repo is the repository worktrees are cut from.
	Repo string
	// Root is the directory worktrees are created under.
	Root string
	// Base is the branch or commit to branch from; empty means the repo's
	// current HEAD.
	Base string
	// BranchPrefix namespaces created branches. Defaults to "wf/".
	BranchPrefix string
	// Git overrides the git binary.
	Git string
}

var _ wf.WorkspaceProvider = (*Provider)(nil)

func (p *Provider) Name() string { return "worktree" }

func (p *Provider) git() string {
	if p.Git != "" {
		return p.Git
	}
	return "git"
}

func (p *Provider) prefix() string {
	if p.BranchPrefix == "" {
		return "wf/"
	}
	return p.BranchPrefix
}

// Create cuts a worktree for the task on a fresh branch. It never reuses an
// existing directory; a taken name gets the next free numeric suffix.
func (p *Provider) Create(ctx context.Context, task wf.Task) (wf.Workspace, error) {
	if p.Repo == "" {
		return nil, fmt.Errorf("worktree provider: no repository configured")
	}
	if p.Root == "" {
		return nil, fmt.Errorf("worktree provider: no root directory configured")
	}

	dir, branch, err := p.free(ctx, WorktreeName(task))
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(p.Root, 0o755); err != nil {
		return nil, fmt.Errorf("create worktree root %s: %w", p.Root, err)
	}

	args := []string{"worktree", "add", "-b", branch, dir}
	if p.Base != "" {
		args = append(args, p.Base)
	}
	if out, err := runGit(ctx, p.git(), p.Repo, args...); err != nil {
		return nil, fmt.Errorf("create worktree for %s: %w: %s", task.ShortID, err, out)
	}

	return &Worktree{dir: dir, branch: branch, repo: p.Repo, git: p.git()}, nil
}

// maxWorktrees bounds the search for a free name; it is a guard, not a
// cleanup policy.
const maxWorktrees = 50

// free returns the first directory and branch pair that nothing holds. Both
// halves must be free: a branch can outlive its checkout, and
// `git worktree add -b` refuses a name that is already a ref.
func (p *Provider) free(ctx context.Context, base string) (dir, branch string, err error) {
	if base == "" {
		base = "task"
	}
	for n := 1; n <= maxWorktrees; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s-%d", base, n)
		}
		dir = filepath.Join(p.Root, name)
		branch = p.prefix() + name

		if _, statErr := os.Stat(dir); statErr == nil {
			continue
		}
		if BranchExists(ctx, p.git(), p.Repo, branch) {
			continue
		}
		return dir, branch, nil
	}
	return "", "", fmt.Errorf("no free worktree name for %s under %s after %d tries — clean up old checkouts", base, p.Root, maxWorktrees)
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

// IsRepo reports whether dir is inside a git repository.
func IsRepo(ctx context.Context, git, dir string) bool {
	if git == "" {
		git = "git"
	}
	_, err := runGit(ctx, git, dir, "rev-parse", "--git-dir")
	return err == nil
}

// BranchExists reports whether branch is a local ref in repo; `wf review`
// uses it to tell an unpushed branch from a merged-and-deleted one.
func BranchExists(ctx context.Context, git, repo, branch string) bool {
	if git == "" {
		git = "git"
	}
	_, err := runGit(ctx, git, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func runGit(ctx context.Context, git, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, git, args...)
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
