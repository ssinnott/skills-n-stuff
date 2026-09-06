package supervisor

// Explicit dispatch, and the identity it is recorded against.
//
// Two claims are under test and they pull in opposite directions, which is
// why they are tested together: naming a workflow has to *override* implicit
// selection, and implicit selection has to keep working untouched for a
// queue that routes by label with nobody naming anything.

import (
	"context"
	"strings"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// twoFlows is a label-routed recipe and one nothing routes to, which is what
// makes an override observable: only an explicit name can select `triage`.
func twoFlows(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"plan-to-pr.md": "---\nname: plan-to-pr\nprofile: coding\nlabels: code\n---\nPlan and ship {{TASK_TITLE}}\n",
		"triage.md":     "---\nname: triage\nprofile: writer\nworkspace: none\n---\nTriage {{TASK_TITLE}}\n",
	}
}

func TestNamedWorkflowOverridesLabelRouting(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Ambiguous work", Labels: []string{"code"}})
	r := &fakeRunner{transcript: "DOC: /vault/triage.md — Triage\nDONE Wrote the triage note.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, loadFlows(t, twoFlows(t)))
	ledger := withLedger(t, s)

	result, err := s.RunOnceWith(context.Background(), "01HZ", Dispatch{Workflow: "triage"})
	if err != nil {
		t.Fatalf("RunOnceWith() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	if len(rec.Runs) != 1 {
		t.Fatalf("runs = %+v, want one", rec.Runs)
	}
	run := rec.Runs[0]
	// The workflow is recorded on the *run*, which is what gives a task
	// dispatched twice under two recipes a history instead of one
	// overwritten field.
	if run.ID != result.Run || run.Workflow != "triage" {
		t.Errorf("run = %+v, want the named workflow recorded on it", run)
	}
	if run.Profile != "writer" {
		t.Errorf("profile = %q, want the named workflow's, not the label match's", run.Profile)
	}
	// And it actually ran that recipe, rather than merely writing the name
	// down: triage declares no workspace and a prompt of its own.
	if len(r.prompts) != 1 || !strings.Contains(r.prompts[0], "Triage Ambiguous work") {
		t.Errorf("prompt = %q, want the named workflow's body", r.prompts)
	}
}

func TestQueueDrivenSelectionIsUnchangedWhenNobodyNamesAnything(t *testing.T) {
	// The other half of the same decision. A queue that routes by label must
	// keep working with no name anywhere, which is what makes the explicit
	// form an override rather than a replacement.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Routed work", Labels: []string{"code"}})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, loadFlows(t, twoFlows(t)))
	ledger := withLedger(t, s)

	if _, err := s.RunOnce(context.Background(), ""); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	if rec.Runs[0].Workflow != "plan-to-pr" {
		t.Errorf("workflow = %q, want the label match", rec.Runs[0].Workflow)
	}
}

func TestNamedWorkflowThatIsNotLoadedFailsWithoutEscalating(t *testing.T) {
	// A name a human just typed has a human at the other end of the
	// terminal. Filing a comment about their typo is noise on the task, and
	// flagging it needs-human would take it out of the queue for a mistake
	// nobody made against the task itself.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Work"})
	r := &fakeRunner{transcript: "DONE\n"}
	s := newSupervisor(q, r, &fakeProvider{}, loadFlows(t, twoFlows(t)))
	ledger := withLedger(t, s)

	_, err := s.RunOnceWith(context.Background(), "01HZ", Dispatch{Workflow: "no-such-flow"})
	if err == nil {
		t.Fatal("naming a workflow that is not loaded must fail")
	}
	if !strings.Contains(err.Error(), "no-such-flow") {
		t.Errorf("error = %v, want it to name the workflow", err)
	}
	if len(q.comments) != 0 {
		t.Errorf("comments = %v, want none — this is not the task's problem", q.comments)
	}
	if attention := q.meta("01HZ", wf.AttentionKey); attention != nil {
		t.Errorf("attention = %v, want the task left alone", attention)
	}
	if _, err := ledger.Resolve("01HZ"); err == nil {
		t.Error("a dispatch that never started must not leave a run behind")
	}
}

func TestDispatchMintsWfsOwnIDAndFindsItAgain(t *testing.T) {
	// The inversion stage 4 is for: the record is found by the tracker row
	// through its queue binding, not keyed on it. A second dispatch has to
	// land on the record the first one minted rather than mint another.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Twice-run work"})
	r := &fakeRunner{transcript: "I stopped and need a decision.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)
	ledger := withLedger(t, s)

	first, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if first.TaskID == "" || first.TaskID == "01HZ" || first.TaskID == "abc4" {
		t.Fatalf("TaskID = %q, want wf's own minted id", first.TaskID)
	}

	second, err := s.RunOnce(context.Background(), "01HZ")
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if second.TaskID != first.TaskID {
		t.Errorf("second dispatch filed under %q, want the record %q that already existed",
			second.TaskID, first.TaskID)
	}

	recs, err := ledger.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("ledger holds %d records for one tracker row: %+v", len(recs), recs)
	}
	if len(recs[0].Runs) != 2 {
		t.Errorf("runs = %+v, want both on the one record", recs[0].Runs)
	}
	// The tracker id never becomes the key, and never stops being a ref.
	if recs[0].ID == "01HZ" {
		t.Error("the record is still keyed by the tracker id")
	}
	found, err := ledger.Resolve("01HZ")
	if err != nil || found.ID != recs[0].ID {
		t.Errorf("Resolve(01HZ) = %+v, %v", found, err)
	}
}
