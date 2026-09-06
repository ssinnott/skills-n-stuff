package review

// Session is `wf review`'s one entry point: resolve a target, replace
// whatever viewer is running, seed it with the task's own findings, and
// record what is now live. cmd/wf parses flags and renders the result;
// everything that decides what actually happens lives here so it can be
// tested without a terminal.

import (
	"context"
	"strconv"
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
// the live viewer with one seeded from the task's own findings. It reads
// the task's shareable bindings through LoadBindings and takes the task's
// machine-local bindings — its workspace — from the ledger record the
// caller holds: the target and the findings are two questions about the
// union of those two sets.
func (s *Session) Open(ctx context.Context, task wf.Task, local wf.Bindings, cfg *config.Config) (Result, error) {
	ref := task.ShortID
	if ref == "" {
		ref = task.ID
	}
	bs, ex := BuildLadder(ctx, task, local, cfg, s.BranchExists)
	return s.OpenBindings(ctx, ref, bs, ex)
}

// OpenBindings runs the ladder over whatever bindings the caller holds.
//
// This is the whole of `wf review`, and it takes bindings rather than a task
// because a task is no longer the only thing that has them: DESIGN-task.md's
// stage 4 turns `--pr` from a bypass into a task carrying exactly one PR
// binding, and a task the ledger knows about has bindings without any queue
// row to read them off. One entry point, one ladder, and the caller decides
// where the bindings came from.
func (s *Session) OpenBindings(ctx context.Context, ref string, bs wf.Bindings, ex Externals) (Result, error) {
	target, err := Resolve(bs, ex)
	if err != nil {
		return Result{}, err
	}

	if target.Kind == KindDoc {
		// Not a diff — there is nothing to spawn or replace.
		return Result{Ref: ref, Target: target}, nil
	}

	findings := FindingsFromIssues(bs)
	comments := CommentFlags(findings)

	spawned, err := s.launch(ctx, ref, target, comments)
	if err != nil {
		return Result{}, err
	}

	return Result{Ref: ref, Target: target, Viewer: spawned, Seeded: len(findings)}, nil
}

// PRBindings is the whole of a task whose only fact is a pull request
// someone opened by hand.
//
// It is what turned `--pr` from a documented second entry point into an
// ordinary one-binding task. The bypass existed because rung 1 only fires on
// `wf.pr` metadata, so a PR with no tracker row could never reach the ladder
// no matter how the ladder was ordered — and stage 4's answer is that the
// row was never what a task needed. A binding is, and one is enough.
func PRBindings(url string) wf.Bindings {
	return wf.Bindings{{Kind: wf.KindPR, Ref: url, State: wf.BindingLive}}
}

// OpenPR reviews a PR by URL. It still works with no kata running — that
// was always the point of `--pr` and it is unchanged — but it is no longer a
// separate code path: the URL becomes the one binding a task has, and the
// ladder resolves it like any other. Nothing is seeded because a task with
// one PR binding has no filed issues to seed from, not because seeding was
// skipped.
func (s *Session) OpenPR(ctx context.Context, url, repo string) (Result, error) {
	return s.OpenBindings(ctx, PRHandle(url), PRBindings(url), Externals{Repo: repo})
}

// launch is the one-viewer-at-a-time lifecycle shared by a task review and
// an ad-hoc --pr review: replace whatever is recorded, ask difit for ref's
// remembered port if one exists, and record the port difit actually
// bound — never the one requested, since --port's fallback means those
// can differ (see State.Ports).
func (s *Session) launch(ctx context.Context, ref string, target Target, comments []string) (Spawned, error) {
	// A corrupt or absent state record must not block a new review; it
	// just means there is nothing to kill and no port to remember.
	prev, _ := LoadState(s.StatePath)

	// One review pane: a new run replaces whatever difit is already
	// running rather than leaving it orphaned in the background.
	if prev.PID != 0 {
		_ = Kill(prev.PID)
	}

	args := target.Args
	if port := portFor(prev.Ports, ref); port != 0 {
		args = append(append([]string{}, args...), "--port", strconv.Itoa(port))
	}

	spawned, err := Launch(ctx, s.Spawner, target.Repo, args, comments)
	if err != nil {
		return Spawned{}, err
	}

	state := State{
		Ref: ref, PID: spawned.PID, Port: spawned.Port, URL: spawned.URL,
		Started: s.now().UTC(),
		Ports:   rememberPort(prev.Ports, ref, spawned.Port),
	}
	if err := SaveState(s.StatePath, state); err != nil {
		return Spawned{}, err
	}
	return spawned, nil
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
