package main

// wf show: one task, grouped by run.
//
// The tracker row and the ledger record describe one task now — kata's ULID
// is its only id — so this joins them at read time: identity and the facts
// only the tracker owns (priority, labels, the lease) come off the row,
// runs and machine-local bindings come off the ledger record.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// now is a variable so output formatting stays testable.
var now = time.Now

func (a *app) cmdShow(ctx context.Context, args []string) (int, error) {
	fs, cf := newFlagSet("show")
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return 1, err
	}
	var ref string
	if len(positionals) > 0 {
		ref = positionals[0]
	}
	if ref == "" {
		return 1, errors.New("wf show <ref>")
	}

	found, err := a.resolve(ctx, ref)
	if err != nil {
		return 1, err
	}

	if cf.json {
		return 0, emitValue(a.showToJSON(found))
	}

	a.printRecord(found)
	return 0, nil
}

// printRecord renders a task as identity, then its own bindings, then a
// block per run holding what that run produced.
func (a *app) printRecord(found resolved) {
	rec, task := found.Record, found.Task

	fmt.Printf("%-9s %s", task.ShortID, task.Title)
	if state, ok := task.Meta[wf.StateKey].(string); ok && state != "" {
		fmt.Printf("   [%s]", state)
	}
	fmt.Println()
	fmt.Printf("id        %s\n", task.ID)

	own := taskOwnBindings(rec)
	for _, b := range own.ByKind(wf.KindRepo) {
		fmt.Println(a.bindingLine("", b, found))
	}
	a.printTrackerFacts(found)

	for i, run := range rec.Runs {
		fmt.Println()
		fmt.Printf("run %-5d %s\n", i+1, runSummary(run))
		for _, b := range sortBindings(rec.Produced(run.ID)) {
			fmt.Println(a.bindingLine("  ", b, found))
		}
	}

	// Whatever is the task's own and is not identity: an adopted PR, a note
	// bound by hand, a live review pane, a NEXT sibling. Printed after the
	// runs because that is where they read — they are the task's current
	// state, not any run's output.
	var rest wf.Bindings
	for _, b := range own {
		if b.Kind != wf.KindRepo {
			rest = append(rest, b)
		}
	}
	rest = sortBindings(rest)
	if len(rest) > 0 {
		fmt.Println()
		for _, b := range rest {
			fmt.Println(a.bindingLine("", b, found))
		}
	}
}

// printTrackerFacts prints what only the tracker knows. Every line here is a
// read, never a copy: nothing below is stored in the ledger, which is what
// keeps the two records disjoint.
func (a *app) printTrackerFacts(found resolved) {
	task := found.Task
	fmt.Printf("priority  %d\n", task.Priority)
	if len(task.Labels) > 0 {
		fmt.Printf("labels    %s\n", strings.Join(task.Labels, ", "))
	}
	if task.Owner != "" {
		fmt.Printf("owner     %s\n", task.Owner)
	}
	if flow, ok := a.workflows.Select(task); ok {
		fmt.Printf("workflow  %s\n", flow.Name)
	}
	if lease, ok := wf.ParseLease(task.Meta[wf.LeaseKey]); ok {
		fmt.Printf("lease     %s\n", lease)
	} else {
		fmt.Println("lease     unheld")
	}
}

// taskOwnBindings are the bindings no run produced — the task's own. A
// binding pointing at a run the record does not hold is counted here too:
// losing the run row must not lose the artifact.
func taskOwnBindings(rec wf.Record) wf.Bindings {
	runs := map[string]bool{}
	for _, r := range rec.Runs {
		runs[r.ID] = true
	}
	var out wf.Bindings
	for _, b := range rec.Bindings {
		if b.Via == "" || !runs[b.Via] {
			out = append(out, b)
		}
	}
	return out
}

func runSummary(run wf.Run) string {
	parts := []string{}
	for _, v := range []string{run.Workflow, run.Profile, run.Model} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	summary := strings.Join(parts, " · ")
	when := age(run.Started, now())
	outcome := string(run.Outcome)
	if !run.Done() {
		outcome = "running"
	}
	return strings.TrimRight(fmt.Sprintf("%-34s %-10s %s", summary, when, outcome), " ")
}

// age renders how long ago something happened, in the one unit that matters
// at that distance. A run is scanned, not read, so "2h" carries the whole
// answer and "2h13m47s" costs the column it takes.
func age(t time.Time, ref time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := ref.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// bindingLine is one binding as a row: what kind it is, what it points at,
// and the one thing worth knowing about it.
func (a *app) bindingLine(indent string, b wf.Binding, found resolved) string {
	return strings.TrimRight(
		fmt.Sprintf("%s%-9s %-44s %s", indent, b.Kind, b.Ref, a.bindingDetail(b, found)), " ")
}

// bindingDetail is the right-hand column: the binding's state in the word
// its kind actually uses, plus whichever single fact makes it actionable.
func (a *app) bindingDetail(b wf.Binding, found resolved) string {
	var parts []string
	if label := b.StateLabel(); label != "" {
		parts = append(parts, label)
	}

	switch b.Kind {
	case wf.KindWorkspace:
		if branch := b.Get(wf.MetaBranch); branch != "" {
			parts = append(parts, branch)
		}
	case wf.KindSession:
		// The command, not the path: the path is already in the left
		// column and is not what anyone types.
		parts = append(parts, "wf attach "+taskRef(found.Task))
	case wf.KindTask:
		if rel := b.Get(wf.MetaRelation); rel != "" {
			parts = append(parts, rel)
		}
	}

	// A machine-local binding recorded elsewhere is a fact about another
	// machine, not a path on this one. Saying whose it is beats printing a
	// directory that will not resolve here.
	if b.Host != "" && b.Host != a.cfg.Actor {
		parts = append(parts, "on "+b.Host)
	}
	if b.Label != "" {
		parts = append(parts, b.Label)
	}
	return strings.Join(parts, " · ")
}

// taskRef is the shortest thing that resolves a task back to itself — what a
// human should type, and what other commands print alongside it.
func taskRef(task wf.Task) string {
	if task.ShortID != "" {
		return task.ShortID
	}
	return task.ID
}

// kindOrder is the order bindings read in, strongest evidence first, which
// is the same order `wf review`'s ladder walks.
var kindOrder = map[wf.Kind]int{
	wf.KindRepo: 0, wf.KindWorkspace: 1, wf.KindSession: 2,
	wf.KindPR: 3, wf.KindDoc: 4, wf.KindIssue: 5, wf.KindTask: 6,
}

// sortBindings puts a run's output in a readable order without disturbing
// the record's own, which is chronological and is what provenance rests on.
func sortBindings(bs wf.Bindings) wf.Bindings {
	out := append(wf.Bindings{}, bs...)
	sort.SliceStable(out, func(i, j int) bool {
		return kindOrder[out[i].Kind] < kindOrder[out[j].Kind]
	})
	return out
}
