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

	"github.com/ssinnott/skills-n-stuff/wf/internal/review"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func (a *app) reviewSession() *review.Session {
	return &review.Session{
		Spawner:      review.ExecSpawner{Command: strings.Fields(a.cfg.DifitCommand)},
		StatePath:    review.StatePath(a.cfg.Path),
		BranchExists: review.GitBranchChecker,
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

	ref := firstPositional(args)
	if ref == "" {
		return 1, errors.New("wf review <ref>")
	}

	task, err := a.queue.Get(ctx, ref)
	if err != nil {
		return 1, err
	}
	issues := wf.IssuesFromMeta(task.Meta)

	result, err := sess.Open(ctx, task, a.cfg, issues)
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

	task, err := a.queue.Get(ctx, ref)
	if err != nil {
		return 1, err
	}
	if err := a.queue.Comment(ctx, task.ID, review.FormatComment(string(raw))); err != nil {
		return 1, fmt.Errorf("comment on %s: %w", task.ShortID, err)
	}

	if hasFlag(args, "--json") {
		return 0, emit("review", jsonReview{Ref: task.ShortID, Commented: true})
	}
	fmt.Printf("commented on %s\n", task.ShortID)
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
