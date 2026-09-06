package main

// wf review: resolve a task to something a human can look at, and drive
// difit as the surface that shows it. The logic lives in internal/review;
// this file only parses argv and renders whatever it returns, the same
// division cmdRun keeps with internal/supervisor.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/review"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func (a *app) reviewSession() *review.Session {
	return &review.Session{
		Spawner:      review.ExecSpawner{Command: strings.Fields(a.cfg.DifitCommand)},
		StatePath:    review.StatePath(a.cfg.Path),
		BranchExists: review.GitBranchChecker,
	}
}

// reviewArgs decides between the two ways `wf review` can be invoked: a
// task ref resolved through the target ladder, or an ad-hoc --pr url that
// bypasses the ladder (and any task lookup) entirely. Passing both is a
// usage error rather than one silently winning — kept as a pure function
// so the mutual-exclusion rule is testable without an app or a queue.
func reviewArgs(args []string) (ref, prURL string, err error) {
	ref = firstPositional(args)
	prURL = flagValue(args, "--pr")
	if ref != "" && prURL != "" {
		return "", "", errors.New("wf review: <ref> and --pr are mutually exclusive")
	}
	return ref, prURL, nil
}

// recordReviewPane writes the live viewer onto the task's ledger record.
//
// It lives here rather than in internal/review for the same reason Apply
// returns bindings instead of writing them: that package would otherwise
// have to import the store, and the caller already holds it. Until this
// existed nothing produced a KindReview binding at all — gc swept for one,
// but review.json was the only real record of a running viewer, which is
// the file the ledger is supposed to supersede.
//
// A failure here never fails the review. The viewer is already up and a
// human is already looking at it; losing the record costs gc a hint, not
// the work.
func (a *app) recordReviewPane(recordID string, result review.Result) {
	if recordID == "" || result.Viewer.URL == "" {
		return
	}
	b := wf.Binding{
		Kind:  wf.KindReview,
		Ref:   result.Viewer.URL,
		State: wf.BindingLive,
		At:    time.Now().UTC(),
		Host:  a.cfg.Actor,
		Meta: map[string]string{
			wf.MetaPort: strconv.Itoa(result.Viewer.Port),
			wf.MetaPID:  strconv.Itoa(result.Viewer.PID),
		},
	}
	_ = a.ledger.Update(recordID, func(rec *wf.Record) error {
		// One pane at a time is wf's rule, so an older pane on this task is
		// retired rather than left looking live to gc.
		rec.Bindings = rec.Bindings.Supersede(wf.KindReview, "").Upsert(b)
		return nil
	})
}

// retireReviewPanes marks every recorded pane disposed once the viewer is
// stopped. There is one difit at a time across all tasks, so a stop settles
// whichever task was holding it.
func (a *app) retireReviewPanes() {
	recs, _ := a.ledger.List()
	for _, rec := range recs {
		if len(rec.Bindings.Live(wf.KindReview)) == 0 {
			continue
		}
		id := rec.ID
		_ = a.ledger.Update(id, func(r *wf.Record) error {
			for i := range r.Bindings {
				if r.Bindings[i].Kind == wf.KindReview && r.Bindings[i].IsLive() {
					r.Bindings[i].State = wf.BindingDisposed
				}
			}
			return nil
		})
	}
}

func (a *app) cmdReview(ctx context.Context, args []string) (int, error) {
	positional := positionals(args)
	if len(positional) > 0 && positional[0] == "comment" {
		return a.cmdReviewComment(ctx, args, positional)
	}

	asJSON := hasFlag(args, "--json")
	sess := a.reviewSession()

	if hasFlag(args, "--stop") {
		ref := firstPositional(args)
		if ref == "" {
			if prURL := flagValue(args, "--pr"); prURL != "" {
				ref = review.PRHandle(prURL)
			}
		}
		stopped, err := sess.Stop(ctx)
		if err != nil {
			return 1, err
		}
		if stopped {
			a.retireReviewPanes()
		}
		if asJSON {
			return 0, emit("review", jsonReview{Ref: ref, Stopped: stopped})
		}
		if stopped {
			fmt.Println("viewer stopped")
		} else {
			fmt.Println("no viewer was running")
		}
		return 0, nil
	}

	ref, prURL, err := reviewArgs(args)
	if err != nil {
		return 1, err
	}

	if prURL != "" {
		// Still no queue call — that was always the point of --pr and it
		// has not changed. What has is that the PR is now a task: it gets
		// a ledger record carrying exactly one binding, so a PR a human
		// opened by hand finally has somewhere to hang, and the second
		// review of it finds the first rather than starting over.
		repo := flagValue(args, "--repo")
		if repo == "" {
			repo = a.cfg.Repo
		}
		repo = config.Expand(repo)

		rec, err := a.taskForPR(prURL)
		if err != nil {
			return 1, err
		}
		result, err := sess.OpenBindings(ctx, review.PRHandle(prURL), rec.Bindings,
			review.Externals{Repo: repo})
		if err != nil {
			return 1, err
		}
		a.recordReviewPane(rec.ID, result)
		if asJSON {
			return 0, emit("review", reviewToJSON(result))
		}
		printReviewResult(result)
		return 0, nil
	}

	if ref == "" {
		return 1, errors.New("wf review <ref> | --pr <url>")
	}

	found, err := a.resolve(ctx, ref)
	if err != nil {
		return 1, err
	}
	// A task the tracker still holds goes through the ladder over its
	// metadata, exactly as before. One the ledger minted has no row and no
	// metadata, and its bindings are the record's own.
	var result review.Result
	if found.Task.ID != "" {
		result, err = sess.Open(ctx, found.Task, a.cfg)
	} else {
		result, err = sess.OpenBindings(ctx, found.Record.Ref(), found.Record.Bindings,
			review.Externals{Repo: a.cfg.Repo, Base: reviewBase(a.cfg.Base)})
	}
	if err != nil {
		return 1, err
	}
	a.recordReviewPane(found.Record.ID, result)

	if asJSON {
		return 0, emit("review", reviewToJSON(result))
	}
	printReviewResult(result)
	return 0, nil
}

// taskForPR finds or mints the task a pull request URL belongs to.
//
// Finding it matters as much as minting it: reviewing the same PR twice must
// land on one task, or the ledger fills with a record per invocation and the
// port memory that makes difit's comments survive is spread across all of
// them. The lookup is by binding rather than through store.Resolve, whose
// business is refs a human types.
//
// A ledger that cannot be written costs the record, never the review: the
// bindings are built either way and the viewer opens on them.
func (a *app) taskForPR(url string) (wf.Record, error) {
	bare := wf.Record{ID: wf.NewTaskID(now()), Bindings: review.PRBindings(url)}

	recs, err := a.ledger.List()
	if err != nil {
		var skip *store.SkipError
		if !errors.As(err, &skip) {
			return bare, nil
		}
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	for _, rec := range recs {
		for _, b := range rec.Bindings.ByKind(wf.KindPR) {
			if b.Ref == url {
				return rec, nil
			}
		}
	}

	bare.Handle = review.PRHandle(url)
	if err := wf.ValidHandle(bare.Handle); err != nil {
		// "acme/widgets#482" is a fine thing to print and a poor thing to
		// type, so it stays the display ref and the record goes unhandled.
		bare.Handle = ""
	}
	bare.Title = "PR " + review.PRHandle(url)
	bare.Created = now().UTC()
	if err := a.ledger.Save(bare); err != nil {
		fmt.Fprintf(os.Stderr, "warning: record PR task: %v\n", err)
	}
	return bare, nil
}

// reviewBase is the branch a diff is taken against when nothing names one.
// BuildLadder applies the same default for a tracker-backed task; a ledger
// task takes this path instead and must not end up with an empty base.
func reviewBase(base string) string {
	if base == "" {
		return "main"
	}
	return base
}

func (a *app) cmdReviewComment(ctx context.Context, args, positional []string) (int, error) {
	if len(positional) < 2 {
		return 1, errors.New("wf review comment <ref>")
	}
	ref := positional[1]

	found, err := a.resolve(ctx, ref)
	if err != nil {
		return 1, err
	}
	// A comment goes on the tracker row: it is the discussion surface, and
	// wf keeps no thread of its own.
	task, err := a.requireTask(found)
	if err != nil {
		return 1, err
	}

	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1, fmt.Errorf("read stdin: %w", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return 1, errors.New("wf review comment: stdin was empty — paste difit's review prompt")
	}

	// --format selects what stdin holds: a human's plain-text paste from
	// difit's "Copy All Prompt" button (the default, unchanged), or
	// difit's own comment store harvested straight from the browser
	// frame's localStorage.
	format := flagValue(args, "--format")
	var body string
	count := 0
	switch format {
	case "", "text":
		body = review.FormatComment(string(raw))
	case "difit":
		threads, err := review.ParseDifitStore(string(raw))
		if err != nil {
			return 1, err
		}
		body = review.FormatComment(review.FormatDifitThreads(threads))
		count = len(threads)
	default:
		return 1, fmt.Errorf("wf review comment: unknown --format %q, want text or difit", format)
	}

	if err := a.queue.Comment(ctx, task.ID, body); err != nil {
		return 1, fmt.Errorf("comment on %s: %w", task.ShortID, err)
	}

	if hasFlag(args, "--json") {
		return 0, emit("review", jsonReview{Ref: task.ShortID, Commented: true, Count: count})
	}
	if count > 0 {
		fmt.Printf("commented on %s (%d difit thread(s))\n", task.ShortID, count)
	} else {
		fmt.Printf("commented on %s\n", task.ShortID)
	}
	return 0, nil
}

func printReviewResult(r review.Result) {
	if r.Target.Kind == review.KindDoc {
		fmt.Printf("%s is a document, not a diff — nothing to open\n", r.Ref)
		fmt.Printf("   note: %s\n", r.Target.Note)
		return
	}

	fmt.Printf("%s [%s] → %s\n", r.Ref, r.Target.Kind, r.Viewer.URL)
	if r.Target.PR != "" {
		fmt.Printf("   pr:     %s\n", r.Target.PR)
	}
	if r.Target.Kind == review.KindBranch {
		fmt.Printf("   branch: %s (base %s)\n", r.Target.Branch, r.Target.Base)
	}
	fmt.Printf("   repo:   %s\n", r.Target.Repo)
	if r.Seeded > 0 {
		fmt.Printf("   seeded %d finding(s) from filed issues\n", r.Seeded)
	}
}
