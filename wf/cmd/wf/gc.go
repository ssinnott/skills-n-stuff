package main // wf gc: bindings whose referent is gone here; --delete marks and prunes.

import (
	"context"
	"fmt"

	"github.com/ssinnott/skills-n-stuff/wf/internal/gc"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
)

func (a *app) cmdGC(ctx context.Context, args []string) (int, error) {
	opts := gc.Options{Delete: hasFlag(args, "--delete")}
	report, err := (&gc.GC{Store: store.New(store.Root(a.cfg.Path)), Actor: a.cfg.Actor}).Sweep(ctx, opts)
	if err != nil {
		return 1, err
	}
	if hasFlag(args, "--json") {
		out := jsonGC{Records: report.Records, Deleted: opts.Delete, Repaired: report.Repaired()}
		for _, f := range report.Findings {
			row := jsonFinding{Kind: string(f.Kind), Task: f.Task, Handle: f.Handle,
				Ref: f.Ref, Detail: f.Detail, Repair: string(f.Repair), Done: f.Done}
			if f.Err != nil {
				row.Error = f.Err.Error()
			}
			out.Findings = append(out.Findings, row)
		}
		return 0, emit("gc", out)
	}
	failed := false
	for _, f := range report.Findings {
		status := "reported"
		if f.Err != nil {
			status, failed = "failed: "+f.Err.Error(), true
		} else if f.Done {
			status = "done"
		}
		fmt.Printf("%-20s%s %s — %s (%s)\n", f.Kind, f.Handle, f.Ref, f.Detail, status)
	}
	fmt.Printf("%d record(s), %d finding(s)\n", report.Records, len(report.Findings))
	if failed {
		return 2, nil
	}
	return 0, nil
}
