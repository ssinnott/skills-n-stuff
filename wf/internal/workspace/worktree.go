// Package workspace provides isolated checkouts for agent runs.
//
// One worktree per task. Two agents in one checkout is the failure that
// costs an afternoon: they fight over the index, over branch state, and
// over each other's uncommitted work, and the resulting mess looks like an
// agent bug rather than a scheduling one.
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
	// keep suppresses removal on Dispose, so a run that escalated leaves
	// its evidence on disk for a human to open.
	keep bool
}

func (w *Worktree) Path() string   { return w.dir }
func (w *Worktree) Branch() string { return w.branch }

// Keep marks the worktree to survive Dispose.
func (w *Worktree) Keep() { w.keep = true }

// Dispose removes the worktree and its branch unless it has been kept.
// Removal is best effort on the branch: a worktree whose work was pushed
// is worth cleaning up even if the branch ref lingers.
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

// Create cuts a worktree for the task on a fresh branch. It fails rather
// than reusing an existing directory: silently handing an agent someone
// else's checkout is worse than refusing to start.
func (p *Provider) Create(ctx context.Context, task wf.Task) (wf.Workspace, error) {
	if p.Repo == "" {
		return nil, fmt.Errorf("worktree provider: no repository configured")
	}
	if p.Root == "" {
		return nil, fmt.Errorf("worktree provider: no root directory configured")
	}

	name := WorktreeName(task)
	dir := filepath.Join(p.Root, name)
	branch := p.prefix() + name

	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("worktree %s already exists — release the previous run first", dir)
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

var unsafeChars = regexp.MustCompile(`[^a-z0-9]+`)

// WorktreeName builds a directory name that is stable for a task and safe
// on disk: the short id keeps it unique, the slug keeps it readable.
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
