package main

// `wf review --pr` as the ad hoc entry point DESIGN.md always meant it to
// be: no queue call, no task, no ledger record. The URL is the whole task —
// one PR binding, resolved straight through the ladder — and the only
// thing wf remembers about it lives in review.json, keyed by the PR's own
// handle so a reopened PR is likely to land back on the same difit origin.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/review"
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

func TestReviewPRNeedsNoLedger(t *testing.T) {
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

	// The whole point: nothing was filed anywhere to hang this on.
	recs, err := h.ledger().List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("ledger holds %d record(s), want none — --pr needs no ledger", len(recs))
	}
}

func TestReviewPRPortRememberedByHandle(t *testing.T) {
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	h.setDifit(t, stubDifit(t, 4971))

	const url = "https://github.com/acme/widgets/pull/17"
	if _, err := h.cli(t, "review", "--pr", url, "--repo", t.TempDir()); err != nil {
		t.Fatalf("wf review --pr: %v", err)
	}

	// review.json's port map is keyed by ref; for a pasted URL that ref is
	// the PR handle, not the URL itself — the same key `wf review --stop
	// --pr <url>` and a second `--pr <url>` review derive independently.
	state, err := review.LoadState(review.StatePath(h.cfgPath))
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	handle := review.PRHandle(url)
	if state.Ref != handle {
		t.Errorf("state.Ref = %q, want the PR handle %q", state.Ref, handle)
	}
	found := false
	for _, p := range state.Ports {
		if p.Ref == handle && p.Port == 4971 {
			found = true
		}
	}
	if !found {
		t.Errorf("ports = %+v, want %q remembered at 4971", state.Ports, handle)
	}

	if _, err := h.cli(t, "review", "--stop"); err != nil {
		t.Fatalf("wf review --stop: %v", err)
	}
	after, err := review.LoadState(review.StatePath(h.cfgPath))
	if err != nil {
		t.Fatalf("LoadState after stop: %v", err)
	}
	if after.PID != 0 || after.URL != "" {
		t.Error("a stopped viewer must not still read as live")
	}
}
