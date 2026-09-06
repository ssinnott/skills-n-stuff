package main

// wf gc: what in the ledger points at something that is no longer there.
//
// argv and rendering only; the sweep is internal/gc. The default is a
// report — a bare `wf gc` writes nothing at all — because a sweep that
// quietly repaired things would be a tool nobody could run just to look.
// --fix corrects recorded states, --delete removes stale records, and
// neither ever touches the artifact a record points at.

import (
	"context"
	"fmt"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/gc"
	"github.com/ssinnott/skills-n-stuff/wf/internal/review"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
)

// jsonFinding is the `wf gc` contract, additive-only like jsonReview.
type jsonFinding struct {
	Kind   string `json:"kind"`
	Task   string `json:"task,omitempty"`
	Handle string `json:"handle,omitempty"`
	Ref    string `json:"ref"`
	Detail string `json:"detail,omitempty"`
	// Repair is what could be done about it; Done says whether this run
	// did it. A finding with a repair and Done false is what a flag would
	// have acted on.
	Repair string `json:"repair,omitempty"`
	Done   bool   `json:"done,omitempty"`
	Error  string `json:"error,omitempty"`
}

type jsonGC struct {
	Records  int           `json:"records"`
	Before   string        `json:"before"`
	Fixed    bool          `json:"fixed"`
	Deleted  bool          `json:"deleted"`
	Repaired int           `json:"repaired"`
	Findings []jsonFinding `json:"findings"`
}

func (a *app) cmdGC(ctx context.Context, args []string) (int, error) {
	before, err := gc.ParseWindow(flagValue(args, "--before"))
	if err != nil {
		return 1, err
	}
	if before == 0 {
		before = gc.DefaultBefore
	}

	opts := gc.Options{
		Before: before,
		Fix:    hasFlag(args, "--fix"),
		Delete: hasFlag(args, "--delete"),
	}

	sweep := &gc.GC{
		Store: store.New(store.Root(a.cfg.Path)),
		// The actor is what a binding's Host was stamped with, so this is
		// what tells this machine's checkouts from another machine's.
		Actor:        a.cfg.Actor,
		ReviewState:  review.StatePath(a.cfg.Path),
		WorktreeRoot: a.cfg.WorktreeRoot,
		Repo:         a.cfg.Repo,
	}

	report, err := sweep.Sweep(ctx, opts)
	if err != nil {
		return 1, err
	}

	if hasFlag(args, "--json") {
		return 0, emit("gc", jsonGCReport(report, opts))
	}
	printGC(report, opts)
	// A repair that failed is not a usage error and not a clean sweep.
	for _, f := range report.Findings {
		if f.Err != nil {
			return 2, nil
		}
	}
	return 0, nil
}

func jsonGCReport(report gc.Report, opts gc.Options) jsonGC {
	out := jsonGC{
		Records:  report.Records,
		Before:   opts.Before.String(),
		Fixed:    opts.Fix,
		Deleted:  opts.Delete,
		Repaired: report.Repaired(),
		Findings: make([]jsonFinding, 0, len(report.Findings)),
	}
	for _, f := range report.Findings {
		row := jsonFinding{
			Kind: string(f.Kind), Task: f.Task, Handle: f.Handle,
			Ref: f.Ref, Detail: f.Detail, Repair: string(f.Repair), Done: f.Done,
		}
		if f.Err != nil {
			row.Error = f.Err.Error()
		}
		out.Findings = append(out.Findings, row)
	}
	return out
}

func printGC(report gc.Report, opts gc.Options) {
	if len(report.Findings) == 0 {
		fmt.Printf("%d task record(s), nothing to collect\n", report.Records)
		return
	}

	for _, f := range report.Findings {
		name := f.Handle
		if name == "" {
			name = f.Task
		}
		if name != "" {
			name = " " + name
		}
		fmt.Printf("%-20s%s %s\n", f.Kind, name, f.Ref)
		if f.Detail != "" {
			fmt.Printf("   %s\n", f.Detail)
		}
		switch {
		case f.Err != nil:
			fmt.Printf("   repair failed: %v\n", f.Err)
		case f.Done && f.Repair == gc.RepairDelete:
			fmt.Println("   record deleted (the work it pointed at was not touched)")
		case f.Done:
			fmt.Println("   state corrected")
		case f.Repair == gc.RepairMark:
			fmt.Println("   --fix would correct the recorded state")
		case f.Repair == gc.RepairDelete:
			fmt.Println("   --delete would drop this record")
		}
	}

	fmt.Printf("\n%d task record(s), %d finding(s), retention %s\n",
		report.Records, len(report.Findings), window(opts.Before))
	if !opts.Fix && !opts.Delete {
		fmt.Println("reporting only — nothing was written")
	}
}

// window renders a retention duration the way it was probably typed.
func window(d time.Duration) string {
	if d%(24*time.Hour) == 0 && d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	return d.String()
}
