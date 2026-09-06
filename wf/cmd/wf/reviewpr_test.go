package main

// `wf review --pr` as an ordinary task.
//
// DESIGN.md called --pr "a second, ad-hoc entry point, not a fifth ladder
// rung", because rung 1 only fired on wf.pr metadata and a PR a human opened
// had no task to hang on. Stage 4 removes the reason: a task is an identity
// plus bindings, and a PR URL is a binding. The bypass is gone; what stayed
// is the property that mattered — it works with no kata running.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// stubDifit answers the way difit does: one JSON line on stdout naming the
// port it actually bound.
func stubDifit(t *testing.T, port int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "difit")
	line := fmt.Sprintf(`{"port":%d,"url":"http://localhost:%d","pid":424242}`, port, port)
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%s'\n", line)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// setDifit points this installation's viewer at a stub.
func (h home) setDifit(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["difitCommand"] = path
	out, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.cfgPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReviewPRBecomesAnOrdinaryOneBindingTask(t *testing.T) {
	// kataBin names a binary that is not there, so any queue call would
	// fail: --pr still works with no kata running, which was always its
	// point and is the part that must survive the rewrite.
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	h.setDifit(t, stubDifit(t, 4966))

	const url = "https://github.com/acme/widgets/pull/482"
	out, err := h.cli(t, "review", "--pr", url, "--repo", t.TempDir(), "--json")
	if err != nil {
		t.Fatalf("wf review --pr error = %v", err)
	}

	var payload struct {
		Review jsonReview `json:"review"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if payload.Review.Kind != "pr" || payload.Review.PR != url {
		t.Errorf("review = %+v, want the PR resolved through the ladder", payload.Review)
	}
	if payload.Review.Ref != "acme/widgets#482" {
		t.Errorf("ref = %q, want the derived PR handle", payload.Review.Ref)
	}
	if payload.Review.Port != 4966 {
		t.Errorf("port = %d, want the one difit reported binding", payload.Review.Port)
	}

	// The part that is new: the PR is now a task, so it has somewhere to
	// hang and something to come back to.
	rec := onlyRecord(t, h)
	prs := rec.Bindings.ByKind(wf.KindPR)
	if len(prs) != 1 || prs[0].Ref != url {
		t.Fatalf("bindings = %+v, want exactly the one PR", rec.Bindings)
	}
	if prs[0].Via != "" {
		t.Errorf("Via = %q — no run produced this, a human did", prs[0].Via)
	}
	if len(rec.Runs) != 0 {
		t.Errorf("runs = %+v, want none", rec.Runs)
	}

	// Reviewing the same PR again finds that task rather than filing a
	// second one — which is what keeps its remembered port, and with it
	// difit's comment store, attached to one origin.
	if _, err := h.cli(t, "review", "--pr", url, "--repo", t.TempDir(), "--json"); err != nil {
		t.Fatalf("second review error = %v", err)
	}
	again := onlyRecord(t, h)
	if again.ID != rec.ID {
		t.Errorf("second review filed a new task %q, want %q", again.ID, rec.ID)
	}
}

// Nothing used to produce a KindReview binding: taskblock rendered one and
// gc swept for one, but review.json was the only real record of a viewer.
// Opening a review now writes the pane onto the task and stopping retires
// it, so gc's pane sweep is a query over bindings rather than over the file
// the ledger is supposed to supersede.
func TestReviewRecordsAndRetiresThePane(t *testing.T) {
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	h.setDifit(t, stubDifit(t, 4971))

	const url = "https://github.com/acme/widgets/pull/17"
	if _, err := h.cli(t, "review", "--pr", url, "--repo", t.TempDir()); err != nil {
		t.Fatalf("wf review --pr: %v", err)
	}

	pane, ok := onlyPane(t, h)
	if !ok {
		t.Fatal("opening a review recorded no KindReview binding")
	}
	if pane.Host == "" {
		t.Error("a review pane is machine-local and must carry a host")
	}
	if pane.Get(wf.MetaPort) != "4971" {
		t.Errorf("port = %q, want 4971 — the port is what makes difit's comments findable again",
			pane.Get(wf.MetaPort))
	}

	if _, err := h.cli(t, "review", "--stop"); err != nil {
		t.Fatalf("wf review --stop: %v", err)
	}
	if _, ok := onlyPane(t, h); ok {
		t.Error("a stopped viewer must not still read as live to gc")
	}
}

// onlyPane returns the one live review binding across the ledger, if any —
// there is one viewer at a time, so more than one live pane is itself a bug.
func onlyPane(t *testing.T, h home) (wf.Binding, bool) {
	t.Helper()
	recs, err := h.ledger().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found []wf.Binding
	for _, rec := range recs {
		found = append(found, rec.Bindings.Live(wf.KindReview)...)
	}
	if len(found) > 1 {
		t.Fatalf("%d live review panes, want at most one", len(found))
	}
	if len(found) == 0 {
		return wf.Binding{}, false
	}
	return found[0], true
}
