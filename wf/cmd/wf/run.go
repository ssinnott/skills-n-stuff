package main

// wf run: dispatch work to agents, once or in a loop.

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/runner"
	"github.com/ssinnott/skills-n-stuff/wf/internal/supervisor"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workspace"
)

func (a *app) cmdRun(ctx context.Context, args []string) (int, error) {
	fs, cf := newFlagSet("run")
	once := fs.Bool("once", false, "run exactly one task and exit")
	max := fs.Int("max", a.cfg.MaxConcurrent, "max concurrent dispatches")
	repoFlag := fs.String("repo", "", "repo root override")
	workflowFlag := fs.String("workflow", "", "workflow name override")
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return 1, err
	}
	if len(positionals) > 1 {
		return 1, fmt.Errorf("wf run: too many arguments: %v", positionals)
	}
	var ref string
	if len(positionals) == 1 {
		ref = positionals[0]
	}

	repo := *repoFlag
	if repo == "" {
		repo = a.cfg.Repo
	}
	repo = config.Expand(repo)

	sup := &supervisor.Supervisor{
		Queue:     a.queue,
		Runner:    &runner.Pi{Bin: a.cfg.PiBin, SessionRoot: a.cfg.SessionRoot},
		Workflows: a.workflows,
		Config:    a.cfg,
		Store:     a.ledger,
		Log:       func(format string, v ...any) { fmt.Printf(format+"\n", v...) },
		Workspaces: func(w workflow.Workflow) wf.WorkspaceProvider {
			r := repo
			if w.Repo != "" {
				r = config.Expand(w.Repo)
			}
			base := a.cfg.Base
			if w.Base != "" {
				base = w.Base
			}
			return &workspace.Provider{Repo: r, Root: a.cfg.WorktreeRoot, Base: base}
		},
	}

	asJSON := cf.json
	if asJSON {
		// Progress goes to stderr so stdout stays a single JSON document.
		sup.Log = func(format string, v ...any) { fmt.Fprintf(os.Stderr, format+"\n", v...) }
	}

	if *once || ref != "" {
		result, err := sup.RunOnceWith(ctx, ref,
			supervisor.Dispatch{Workflow: *workflowFlag})
		if errors.Is(err, supervisor.ErrNothingReady) {
			if asJSON {
				return 0, emit("results", []jsonRunResult{})
			}
			fmt.Println("nothing ready")
			return 0, nil
		}
		if err != nil {
			return 1, err
		}
		if asJSON {
			return 0, emit("results", []jsonRunResult{a.runResultToJSON(result)})
		}
		report(result)
		return 0, nil
	}

	results, err := sup.Run(ctx, *max)
	if err != nil && !errors.Is(err, context.Canceled) {
		return 1, err
	}
	if asJSON {
		out := make([]jsonRunResult, 0, len(results))
		for _, result := range results {
			out = append(out, a.runResultToJSON(result))
		}
		return 0, emit("results", out)
	}
	for _, result := range results {
		report(result)
	}
	fmt.Printf("%d task(s) dispatched\n", len(results))
	return 0, nil
}

func report(r supervisor.Result) {
	switch {
	case r.Applied.Escalated:
		fmt.Printf("%s escalated: %s\n", r.Task.ShortID, r.Applied.Reason)
		fmt.Printf("   wf attach %s\n", r.Task.ShortID)
	case r.Applied.Closed:
		fmt.Printf("%s closed\n", r.Task.ShortID)
	}
	for _, doc := range r.Applied.Bound {
		fmt.Printf("   note: %s\n", doc.VaultPath)
	}
	for _, ref := range r.Applied.Created {
		fmt.Printf("   follow-on: %s\n", ref)
	}
}
