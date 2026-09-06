package main

// wf show: the whole task object, grouped by run.
//
// The ledger is what this renders. Before stage 4 `wf show` printed a
// hand-assembled subset of tracker metadata — a session, a note, a count of
// runs — and had no way to say which run produced which artifact, because
// flat keys have nowhere to record it. What it prints now is a record:
// identity, the runs, and under each run the bindings it made.
//
// The tracker is still read, and still owns what it owns. Priority, labels,
// the lease and open/closed are reads against it, never mirrored here. A
// task the tracker cannot answer for right now — kata down, or a task that
// was never filed — renders everything wf itself knows and simply omits
// those lines, which is the split working rather than a degraded mode.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func (a *app) cmdShow(ctx context.Context, args []string) (int, error) {
	ref := firstPositional(args)
	if ref == "" {
		return 1, errors.New("wf show <ref>")
	}

	found, err := a.resolve(ctx, ref)
	if err != nil {
		return 1, err
	}

	if hasFlag(args, "--json") {
		// Both keys, because both are still true and clients read one or
		// the other: `task` is the tracker row as it always was, `record`
		// is wf's own object. A client that only knows the old shape keeps
		// working; one that wants provenance reads the new one.
		return 0, emitFields(map[string]any{
			"task":   a.taskJSON(found),
			"record": a.recordToJSON(found.Record, found.Task),
		})
	}

	a.printRecord(found)
	return 0, nil
}

// printRecord renders a task as identity, then its own bindings, then a
// block per run holding what that run produced.
func (a *app) printRecord(found resolved) {
	rec, task := found.Record, found.Task

	fmt.Printf("%-9s %s", rec.Ref(), rec.Name())
	if state, ok := task.Meta[wf.StateKey].(string); ok && state != "" {
		fmt.Printf("   [%s]", state)
	}
	fmt.Println()
	fmt.Printf("id        %s\n", rec.ID)

	own := taskOwnBindings(rec)
	for _, b := range own.ByKind(wf.KindQueue) {
		fmt.Println(a.bindingLine("", b, found))
	}
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
		if b.Kind != wf.KindQueue && b.Kind != wf.KindRepo {
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
	if task.ID == "" {
		if found.Filed {
			// Saying so beats printing nothing: the missing lines below are
			// facts wf deliberately does not keep, not facts it lost.
			fmt.Println("tracker   unreachable — showing what wf recorded")
		}
		return
	}
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
	// A queue binding's state is only ever "the row was there when wf wrote
	// this down" — nothing refreshes it — so rendering it would read as a
	// claim about open or closed that wf does not have. The tracker lines
	// answer that question or nobody does.
	if label := b.StateLabel(); label != "" && b.Kind != wf.KindQueue {
		parts = append(parts, label)
	}

	switch b.Kind {
	case wf.KindQueue:
		if backend := b.Get(wf.MetaBackend); backend != "" {
			if short := b.Get(wf.MetaShortID); short != "" {
				backend += " " + short
			}
			parts = append(parts, backend)
		}
		if found.Task.ID != "" {
			parts = append(parts, fmt.Sprintf("p%d", found.Task.Priority))
		}
	case wf.KindWorkspace:
		if branch := b.Get(wf.MetaBranch); branch != "" {
			parts = append(parts, branch)
		}
	case wf.KindSession:
		// The command, not the path: the path is already in the left
		// column and is not what anyone types.
		parts = append(parts, "wf attach "+found.Record.Ref())
	case wf.KindReview:
		if port := b.Get(wf.MetaPort); port != "" {
			parts = append(parts, ":"+port)
		}
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
	if b.Label != "" && b.Kind != wf.KindQueue {
		parts = append(parts, b.Label)
	}
	return strings.Join(parts, " · ")
}

// kindOrder is the order bindings read in, strongest evidence first, which
// is the same order `wf review`'s ladder walks.
var kindOrder = map[wf.Kind]int{
	wf.KindQueue: 0, wf.KindRepo: 1, wf.KindWorkspace: 2, wf.KindSession: 3,
	wf.KindPR: 4, wf.KindDoc: 5, wf.KindIssue: 6, wf.KindReview: 7, wf.KindTask: 8,
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
