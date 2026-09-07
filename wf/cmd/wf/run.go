package main

// wf run: dispatch one task to an agent under a workflow. Every run is a
// human's choice, so a ref is required and there is no loop.

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
	repoFlag := fs.String("repo", "", "repo root override")
	workflowFlag := fs.String("workflow", "", "workflow name override")
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return 1, err
	}
	if len(positionals) != 1 {
		return 1, errors.New("wf run <ref> [--workflow W] [--repo P]")
	}
	ref := positionals[0]

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
	if cf.json {
		// Progress goes to stderr so stdout stays a single JSON document.
		sup.Log = func(format string, v ...any) { fmt.Fprintf(os.Stderr, format+"\n", v...) }
	}

	result, err := sup.RunOnce(ctx, ref, supervisor.Dispatch{Workflow: *workflowFlag})
	if err != nil {
		return 1, err
	}
	if cf.json {
		return 0, emit("results", []jsonRunResult{a.runResultToJSON(result)})
	}
	report(result)
	return 0, nil
}

func report(r supervisor.Result) {
	ref := taskRef(r.Task)
	switch {
	case r.Applied.Escalated:
		fmt.Printf("%s escalated: %s\n", ref, r.Applied.Reason)
		fmt.Printf("   wf attach %s\n", ref)
	case r.Applied.Completed:
		fmt.Printf("%s run complete — next: wf run %s --workflow <name>, or wf close %s\n", ref, ref, ref)
	}
	for _, doc := range r.Applied.Bound {
		fmt.Printf("   note: %s\n", doc.VaultPath)
	}
	for _, ref := range r.Applied.Created {
		fmt.Printf("   follow-on: %s\n", ref)
	}
}
