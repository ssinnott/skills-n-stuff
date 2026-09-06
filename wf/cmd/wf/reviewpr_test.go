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
