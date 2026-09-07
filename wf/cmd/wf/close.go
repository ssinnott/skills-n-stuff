package main

// wf close: the human's terminal transition. A task is a unit of work that
// several workflow runs happen against; none of them closes it. When the
// work is done — the PR merged, the question answered — this closes the
// tracker row with the evidence those runs recorded on it.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func (a *app) cmdClose(ctx context.Context, args []string) (int, error) {
	fs, cf := newFlagSet("close")
	message := fs.String("message", "", "close message; composed from the evidence when empty")
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return 1, err
	}
	if len(positionals) != 1 {
		return 1, errors.New("wf close <ref> [--message M]")
	}

	task, err := a.queue.Get(ctx, positionals[0])
	if err != nil {
		return 1, err
	}
	result, err := wf.Close(ctx, a.queue, task, *message)
	if err != nil {
		return 1, err
	}

	if cf.json {
		return 0, emit("closed", jsonClosed{
			Task: a.toJSON(task), PRs: result.PRs, Docs: result.Docs, Message: result.Message,
		})
	}
	fmt.Printf("%s closed\n", taskRef(task))
	if len(result.PRs) > 0 {
		fmt.Printf("   pull requests: %s\n", strings.Join(result.PRs, ", "))
	}
	if len(result.Docs) > 0 {
		fmt.Printf("   documents: %s\n", strings.Join(result.Docs, ", "))
	}
	return 0, nil
}
