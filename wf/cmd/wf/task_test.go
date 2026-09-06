package main

// End to end through the CLI's own entry point, against a real FileStore on
// disk and a real kata subprocess — a stub script, but a real process spoken
// to over the real argv-and-JSON contract, in the spirit of
// internal/supervisor/realgit_test.go. What these prove is exactly the three
// claims stage 4 makes and nothing below the CLI can:
//
//   - a task exists with no queue running and none installed;
//   - filing one later adds a row to the record that is already there,
//     rather than starting a second one;
//   - a ref resolves across both id spaces, all four forms.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// home is a wf installation: its config, and the ledger directory that sits
// beside it exactly as review.json does.
type home struct {
	dir     string
	cfgPath string
}

// newHome writes a config naming kataBin, which is how a test points wf at a
// tracker without touching the environment. An unwritten stub means the
// binary is simply not there, which is the "no kata installed" case.
func newHome(t *testing.T, kataBin string) home {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := map[string]any{
		"actor":        "wf-test",
		"kataBin":      kataBin,
		"workflowDir":  filepath.Join(dir, "workflows"),
		"worktreeRoot": filepath.Join(dir, "worktrees"),
		"sessionRoot":  filepath.Join(dir, "sessions"),
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return home{dir: dir, cfgPath: cfgPath}
}

func (h home) ledger() store.Store { return store.New(h.dir) }

// cli runs one wf invocation the way a shell would, capturing stdout. It
// goes through run() rather than through an app built by hand, so flag
// parsing and command dispatch are part of what is under test.
func (h home) cli(t *testing.T, args ...string) (string, error) {
	t.Helper()
	args = append(args, "--config", h.cfgPath)

	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	_, runErr := run(context.Background(), args)

	w.Close()
	os.Stdout = prev
	return <-done, runErr
}

// stubKata writes a kata that answers `show` with one issue and nothing else.
// Every other verb exits non-zero, so a test that accidentally depends on the
// tracker fails loudly instead of passing on a silent default.
func stubKata(t *testing.T, id, shortID, title string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kata")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  show) printf '%%s' '{"issue":{"uid":"%s","short_id":"%s","title":"%s","priority":1}}' ;;
  *) echo "stub kata: unsupported verb $1" >&2; exit 2 ;;
esac
`, id, shortID, title)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func onlyRecord(t *testing.T, h home) wf.Record {
	t.Helper()
	recs, err := h.ledger().List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("ledger holds %d records, want exactly one: %+v", len(recs), recs)
	}
	return recs[0]
}

func TestTaskNewNeedsNoQueueAtAll(t *testing.T) {
	// The claim in one line: kataBin names a binary that does not exist, so
	// any queue call at all would fail the command.
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))

	out, err := h.cli(t, "task", "new", "Fix the tolerant separator parser", "--handle", "neck")
	if err != nil {
		t.Fatalf("wf task new error = %v", err)
	}
	if !strings.Contains(out, "neck") || !strings.Contains(out, "tolerant separator parser") {
		t.Errorf("output = %q, want the handle and the title", out)
	}

	rec := onlyRecord(t, h)
	if rec.Handle != "neck" {
		t.Errorf("handle = %q, want the one that was asked for", rec.Handle)
	}
	if rec.Title != "Fix the tolerant separator parser" {
		t.Errorf("title = %q", rec.Title)
	}
	if rec.ID == "" || rec.ID == rec.Handle {
		t.Errorf("id = %q, want wf's own minted id", rec.ID)
	}
	if rec.Created.IsZero() {
		t.Error("a minted task must record when it was created")
	}
	// The whole point: no tracker row, and no pretence of one.
	if len(rec.Bindings) != 0 {
		t.Errorf("bindings = %+v, want none — nothing has been bound yet", rec.Bindings)
	}
	if _, filed := rec.QueueRef(); filed {
		t.Error("a task nobody filed must not claim a queue binding")
	}
	if len(rec.Runs) != 0 {
		t.Errorf("runs = %+v, want none", rec.Runs)
	}

	// And it is readable, still with no kata anywhere.
	shown, err := h.cli(t, "show", "neck")
	if err != nil {
		t.Fatalf("wf show neck error = %v", err)
	}
	if !strings.Contains(shown, rec.ID) || !strings.Contains(shown, "tolerant separator parser") {
		t.Errorf("wf show output = %q", shown)
	}
}

func TestTaskNewRefusesAHandleAlreadyTaken(t *testing.T) {
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	if _, err := h.cli(t, "task", "new", "First", "--handle", "neck"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.cli(t, "task", "new", "Second", "--handle", "neck"); err == nil {
		t.Fatal("a taken handle must be refused, not silently suffixed")
	}
}

func TestTaskAdoptAddsARowWithoutCreatingASecondRecord(t *testing.T) {
	h := newHome(t, stubKata(t, "01HZTRACKERROW", "abc4", "Filed work"))

	if _, err := h.cli(t, "task", "new", "Started before it was filed", "--handle", "neck"); err != nil {
		t.Fatal(err)
	}
	before := onlyRecord(t, h)

	out, err := h.cli(t, "task", "adopt", "neck", "--queue", "01HZTRACKERROW")
	if err != nil {
		t.Fatalf("wf task adopt error = %v", err)
	}
	if !strings.Contains(out, "abc4") {
		t.Errorf("output = %q, want the tracker's short ref", out)
	}

	// One record, still. This is the assertion the whole verb exists for:
	// adopting attaches a binding, it does not re-file the work.
	after := onlyRecord(t, h)
	if after.ID != before.ID {
		t.Errorf("id moved from %q to %q — adopting must not re-key the record", before.ID, after.ID)
	}
	if !after.Created.Equal(before.Created) {
		t.Errorf("created moved from %v to %v", before.Created, after.Created)
	}
	if after.Handle != "neck" || after.Title != before.Title {
		t.Errorf("identity changed: %+v", after)
	}

	rows := after.Bindings.ByKind(wf.KindQueue)
	if len(rows) != 1 {
		t.Fatalf("queue bindings = %+v, want exactly one", rows)
	}
	if rows[0].Ref != "01HZTRACKERROW" {
		t.Errorf("queue ref = %q", rows[0].Ref)
	}
	if rows[0].Get(wf.MetaShortID) != "abc4" {
		t.Errorf("short id = %q — it cannot be derived, so it has to be recorded", rows[0].Get(wf.MetaShortID))
	}
	if rows[0].Get(wf.MetaBackend) != "kata" {
		t.Errorf("backend = %q, want the queue's name as a value", rows[0].Get(wf.MetaBackend))
	}
	if rows[0].Via != "" {
		t.Errorf("Via = %q — filing work and doing it are different acts", rows[0].Via)
	}

	// The tracker's title now wins for display, without overwriting the one
	// the ledger recorded.
	if after.Name() != "Filed work" {
		t.Errorf("Name() = %q, want the tracker's title once a row exists", after.Name())
	}
	if after.Title != "Started before it was filed" {
		t.Errorf("Title = %q, want the ledger's own to survive underneath", after.Title)
	}

	// Adopting twice is not two rows.
	if _, err := h.cli(t, "task", "adopt", "neck", "--queue", "01HZTRACKERROW"); err != nil {
		t.Fatalf("second adopt error = %v", err)
	}
	if rows := onlyRecord(t, h).Bindings.ByKind(wf.KindQueue); len(rows) != 1 {
		t.Errorf("queue bindings after re-adopting = %+v, want still one", rows)
	}
}

func TestTaskAdoptRefusesARowBoundToAnotherTask(t *testing.T) {
	h := newHome(t, stubKata(t, "01HZTRACKERROW", "abc4", "Filed work"))

	if _, err := h.cli(t, "task", "new", "First", "--handle", "neck"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.cli(t, "task", "new", "Second", "--handle", "wrist"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.cli(t, "task", "adopt", "neck", "--queue", "01HZTRACKERROW"); err != nil {
		t.Fatal(err)
	}

	// Two records naming one row is what makes every later resolution
	// ambiguous, so it is refused here rather than reconciled later.
	if _, err := h.cli(t, "task", "adopt", "wrist", "--queue", "01HZTRACKERROW"); err == nil {
		t.Fatal("a row already bound elsewhere must be refused")
	}
}

func TestRefResolvesInEveryForm(t *testing.T) {
	h := newHome(t, stubKata(t, "01HZTRACKERROW", "abc4", "Filed work"))

	if _, err := h.cli(t, "task", "new", "Work with two names", "--handle", "neck"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.cli(t, "task", "adopt", "neck", "--queue", "01HZTRACKERROW"); err != nil {
		t.Fatal(err)
	}
	rec := onlyRecord(t, h)

	forms := map[string]string{
		"wf id":            rec.ID,
		"wf handle":        "neck",
		"tracker id":       "01HZTRACKERROW",
		"tracker short id": "abc4",
	}
	for name, ref := range forms {
		t.Run(name, func(t *testing.T) {
			out, err := h.cli(t, "show", ref, "--json")
			if err != nil {
				t.Fatalf("wf show %s error = %v", ref, err)
			}
			var payload struct {
				Record jsonRecord `json:"record"`
			}
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("decode %q: %v", out, err)
			}
			if payload.Record.ID != rec.ID {
				t.Errorf("%s resolved to %q, want %q", name, payload.Record.ID, rec.ID)
			}
			if !payload.Record.Filed || payload.Record.Queue != "01HZTRACKERROW" {
				t.Errorf("record = %+v, want the tracker row reported", payload.Record)
			}
		})
	}
}

func TestAmbiguousRefNamesItsCandidates(t *testing.T) {
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	for _, handle := range []string{"necklace", "neckline"} {
		if _, err := h.cli(t, "task", "new", "Work", "--handle", handle); err != nil {
			t.Fatal(err)
		}
	}

	_, err := h.cli(t, "show", "neck")
	if err == nil {
		t.Fatal("an abbreviation matching two tasks must not silently pick one")
	}
	for _, want := range []string{"necklace", "neckline"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name candidate %q", err, want)
		}
	}
}

func TestRunOnAnUnfiledTaskNamesTheWayOut(t *testing.T) {
	// The honest limit of this stage. The dispatch loop leases, claims, sets
	// state and applies outcomes through the Queue, so a task with no row
	// has nothing for any of those verbs to act on. Closing that means a
	// Queue adapter over the ledger — which is what the seam was cut for and
	// is not what this stage builds — so the error says so and names the fix
	// rather than sending kata a handle it will not recognize.
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	if _, err := h.cli(t, "task", "new", "Unfiled work", "--handle", "neck"); err != nil {
		t.Fatal(err)
	}

	_, err := h.cli(t, "run", "neck", "--workflow", "plan-to-pr")
	if err == nil {
		t.Fatal("dispatching a task with no tracker row must not silently succeed")
	}
	if !strings.Contains(err.Error(), "wf task adopt neck") {
		t.Errorf("error = %v, want it to name the way out", err)
	}
}

func TestShowFallsBackToTrackerMetadataForATaskTheLedgerNeverSaw(t *testing.T) {
	// The record is not the only source: a task bound by an earlier release,
	// or filed on another host, still renders from what its metadata holds.
	h := newHome(t, stubKata(t, "01HZTRACKERROW", "abc4", "Filed elsewhere"))

	out, err := h.cli(t, "show", "abc4")
	if err != nil {
		t.Fatalf("wf show error = %v", err)
	}
	if !strings.Contains(out, "Filed elsewhere") {
		t.Errorf("output = %q, want the tracker's title", out)
	}
	if recs, _ := h.ledger().List(); len(recs) != 0 {
		t.Errorf("reading a task must not create a ledger record: %+v", recs)
	}
}
