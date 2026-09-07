package review

import (
	"context"
	"strconv"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Session is `wf review`'s one entry point: resolve a target, replace the
// running viewer, seed it with the task's findings, and record what's live.
type Session struct {
	Spawner      Spawner
	StatePath    string
	BranchExists BranchChecker
	// Now is injectable so state-file tests do not depend on wall time.
	Now func() time.Time
}

// Result is what one invocation produced.
type Result struct {
	Ref    string
	Target Target
	// Viewer is the zero value when Target.Kind is KindDoc.
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
func (s *Session) Open(ctx context.Context, task wf.Task, local wf.Bindings, cfg *config.Config) (Result, error) {
	ref := task.ShortID
	if ref == "" {
		ref = task.ID
	}
	bs, ex := BuildLadder(ctx, task, local, cfg, s.BranchExists)
	return s.OpenBindings(ctx, ref, bs, ex)
}

// OpenBindings runs the ladder over whatever bindings the caller holds: one
// entry point, one ladder, regardless of where the bindings came from.
func (s *Session) OpenBindings(ctx context.Context, ref string, bs wf.Bindings, ex Externals) (Result, error) {
	target, err := Resolve(bs, ex)
	if err != nil {
		return Result{}, err
	}

	if target.Kind == KindDoc {
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
func PRBindings(url string) wf.Bindings {
	return wf.Bindings{{Kind: wf.KindPR, Ref: url, State: wf.BindingLive}}
}

// OpenPR reviews a PR by URL with no kata running. The URL becomes the
// task's one binding; nothing is seeded, since it has no filed issues.
func (s *Session) OpenPR(ctx context.Context, url, repo string) (Result, error) {
	return s.OpenBindings(ctx, PRHandle(url), PRBindings(url), Externals{Repo: repo})
}

// launch is the one-viewer-at-a-time lifecycle shared by a task review and
// an ad-hoc --pr review: replace whatever is recorded, ask difit for ref's
// remembered port if one exists, and record the port difit actually bound.
func (s *Session) launch(ctx context.Context, ref string, target Target, comments []string) (Spawned, error) {
	prev, _ := LoadState(s.StatePath)

	// One viewer at a time: the previous pid is killed before spawning.
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

// Stop kills the recorded viewer, if one is running, and clears the
// record; it reports false, not an error, when nothing was running.
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
