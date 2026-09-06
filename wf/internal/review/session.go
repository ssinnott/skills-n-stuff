package review

// Session is `wf review`'s one entry point: resolve a target, replace
// whatever viewer is running, seed it with the task's own findings, and
// record what is now live. cmd/wf parses flags and renders the result;
// everything that decides what actually happens lives here so it can be
// tested without a terminal.

import (
	"context"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Session carries what one `wf review` invocation needs beyond the task
// itself.
type Session struct {
	Spawner      Spawner
	StatePath    string
	BranchExists BranchChecker
	// Now is injectable so state-file tests do not depend on wall time.
	Now func() time.Time
}

// Result is what one invocation produced, independent of how it is
// rendered — JSON and human output both read off this.
type Result struct {
	Ref    string
	Target Target
	// Viewer is the zero value when Target.Kind is KindDoc: a document has
	// nothing to spawn.
	Viewer Spawned
	Seeded int
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Open resolves ref's target and, unless it is a bare document, replaces
// the live viewer with one seeded from the task's own findings.
func (s *Session) Open(ctx context.Context, task wf.Task, cfg *config.Config, issues []wf.IssueRecord) (Result, error) {
	in := BuildInputs(ctx, task, cfg, s.BranchExists)
	target, err := Resolve(in)
	if err != nil {
		return Result{}, err
	}

	ref := task.ShortID
	if ref == "" {
		ref = task.ID
	}

	if target.Kind == KindDoc {
		// Not a diff — there is nothing to spawn or replace.
		return Result{Ref: ref, Target: target}, nil
	}

	findings := FindingsFromIssues(issues)
	comments := CommentFlags(findings)

	// One review pane: a new run replaces whatever difit is already
	// running rather than leaving it orphaned in the background.
	if prev, err := LoadState(s.StatePath); err == nil && prev.PID != 0 {
		_ = Kill(prev.PID)
	}

	spawned, err := Launch(ctx, s.Spawner, target.Repo, target.Args, comments)
	if err != nil {
		return Result{}, err
	}

	state := State{Ref: ref, PID: spawned.PID, Port: spawned.Port, URL: spawned.URL, Started: s.now().UTC()}
	if err := SaveState(s.StatePath, state); err != nil {
		return Result{}, err
	}

	return Result{Ref: ref, Target: target, Viewer: spawned, Seeded: len(findings)}, nil
}

// Stop kills the recorded viewer, if one is running, and clears the record.
// It reports false rather than erroring when nothing was running: stopping
// an idle review pane is not a failure.
func (s *Session) Stop(context.Context) (bool, error) {
	prev, err := LoadState(s.StatePath)
	if err != nil {
		return false, err
	}
	if prev.PID == 0 && prev.URL == "" {
		return false, nil
	}
	if err := Kill(prev.PID); err != nil {
		return false, err
	}
	if err := ClearState(s.StatePath); err != nil {
		return false, err
	}
	return true, nil
}
