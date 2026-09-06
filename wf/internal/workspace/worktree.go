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
func (w *Worktree) Repo() string   { return w.repo }
func (w *Worktree) Branch() string { return w.branch }

// compile-time proof the accessors a run records through are the ones the
// seam promises.
var _ wf.Workspace = (*Worktree)(nil)

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

// Create cuts a worktree for the task on a fresh branch.
//
// It never reuses an existing directory — silently handing an agent someone
// else's checkout is the failure this package exists to prevent — and it no
// longer *refuses* one either. WorktreeName is derived from the task, so the
// name a re-run computes is the name its predecessor already holds, and a
// run that escalated has its checkout deliberately kept on disk: refusing
// meant a task could never be re-run while the evidence from its last run
// was still there, which is precisely the case a re-run is for. A taken name
// takes the next free suffix instead, the same answer artifact binding
// already gives two runs producing one filename.
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

// maxWorktrees bounds the search for a free name. It is a guard against a
// root nobody ever cleans up, not a policy: fifty kept checkouts of one task
// is a `wf gc` problem, and looping forever to find the fifty-first would
// hide it.
const maxWorktrees = 50

// free returns the first directory and branch pair that nothing holds.
//
// Both halves have to be free, not just the directory: Dispose removes the
// branch on a best-effort basis, so a branch can outlive its checkout, and
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

// WorktreeName builds a directory name that is stable for a task and safe
// on disk: the short id keeps it unique, the slug keeps it readable.
//
// It is the naming rule for a *new* worktree and deliberately not the
// lookup rule for an existing one. The slug comes from the task's title, so
// a rename in the tracker changes what this returns while the checkout and
// the branch on disk keep the old name — which is why a run records its
// branch on the workspace binding it produced (see wf.MetaBranch) rather
// than expecting anyone to recompute it from here later.
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

// BranchExists reports whether branch is a local ref in repo. `wf review`
// uses this to decide whether a task's worktree branch is still around
// after its checkout was disposed: which branch to ask about now comes off
// the workspace binding the run recorded, and this is what tells "branch
// was never pushed" apart from "branch merged and deleted."
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
