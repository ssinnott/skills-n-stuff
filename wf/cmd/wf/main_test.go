package main

// Flag parsing for `wf run`, exercised through run() itself so a subcommand's
// FlagSet is genuinely what is under test, not a hand-built stand-in for it.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRunRequiresARef: every run is a human's choice of task, so `wf run`
// with no ref has nothing to do and says so before touching the tracker.
func TestRunRequiresARef(t *testing.T) {
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	if _, err := h.cli(t, "run"); err == nil || !strings.Contains(err.Error(), "wf run <ref>") {
		t.Fatalf("wf run with no ref: err = %v, want the usage line — every run is a human's choice of task", err)
	}
	if _, err := h.cli(t, "run", "a", "b"); err == nil {
		t.Fatal("wf run with two refs: err = nil, want an error")
	}
}

// TestRunRejectsUnknownFlag is stage 6's headline behavior: a flag typo is a
// hard error now, not a silently accepted positional the way the old argv
// scanner treated it.
func TestRunRejectsUnknownFlag(t *testing.T) {
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	if _, err := h.cli(t, "run", "--wrokflow", "x"); err == nil {
		t.Fatal(`wf run --wrokflow x: err = nil, want an error naming the flag`)
	}
}

// TestRunPositionalEitherSideOfFlag holds up the promise that flags may come
// before or after the ref positional: `wf run <ref> --workflow W` is what a
// human types, `wf run --workflow W <ref>` is what happens to fall out of
// argument order elsewhere, and both must reach cmdRun's dispatch the same
// way. Both point at a task ref that does not exist on a kata binary that
// is not there either, so both are expected to fail — the assertion is that
// they fail identically, past flag parsing, rather than one silently
// dropping --workflow or the ref.
func TestRunPositionalEitherSideOfFlag(t *testing.T) {
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	_, errRefFirst := h.cli(t, "run", "abc4", "--workflow", "W")
	_, errFlagFirst := h.cli(t, "run", "--workflow", "W", "abc4")

	if errRefFirst == nil || errFlagFirst == nil {
		t.Fatalf("expected both forms to fail past flag parsing (no kata binary), got refFirst=%v flagFirst=%v", errRefFirst, errFlagFirst)
	}
	if errRefFirst.Error() != errFlagFirst.Error() {
		t.Errorf("the two argument orders parsed differently:\n  <ref> --workflow W: %v\n  --workflow W <ref>: %v", errRefFirst, errFlagFirst)
	}
}
