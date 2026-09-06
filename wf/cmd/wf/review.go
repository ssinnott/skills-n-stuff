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
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/review"
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
		// The ad-hoc PR path: no task, no queue call, so it works with no
		// kata running at all.
		repo := flagValue(args, "--repo")
		if repo == "" {
			repo = a.cfg.Repo
		}
		repo = config.Expand(repo)

		result, err := sess.OpenPR(ctx, prURL, repo)
		if err != nil {
			return 1, err
		}
		if asJSON {
			return 0, emit("review", reviewToJSON(result))
		}
		printReviewResult(result)
		return 0, nil
	}

	if ref == "" {
		return 1, errors.New("wf review <ref> | --pr <url>")
	}

	task, err := a.queue.Get(ctx, ref)
	if err != nil {
		return 1, err
	}
	result, err := sess.Open(ctx, task, a.cfg)
	if err != nil {
		return 1, err
	}

	if asJSON {
		return 0, emit("review", reviewToJSON(result))
	}
	printReviewResult(result)
	return 0, nil
}

func (a *app) cmdReviewComment(ctx context.Context, args, positional []string) (int, error) {
	if len(positional) < 2 {
		return 1, errors.New("wf review comment <ref>")
	}
	ref := positional[1]

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

	task, err := a.queue.Get(ctx, ref)
	if err != nil {
		return 1, err
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
