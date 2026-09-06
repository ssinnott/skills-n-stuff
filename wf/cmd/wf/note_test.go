package main

// `wf note sync` is the one command that reaches neither the queue nor an
// agent, so it can be driven end to end here: a real config, a real ledger
// and a real vault under t.TempDir(). Which is worth doing, because the
// bug this command must not have — writing the wrong file, or writing one
// that did not need writing — is invisible to any test that stops at the
// argument parsing.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func noteApp(t *testing.T) (*app, string) {
	t.Helper()
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Vault: vault, Path: filepath.Join(root, "config.json")}
	return &app{cfg: cfg}, vault
}

func TestCmdNoteUsage(t *testing.T) {
	a, _ := noteApp(t)
	for _, args := range [][]string{
		{},
		{"resync", "neck"},
		{"sync"},
	} {
		if code, err := a.cmdNote(args); err == nil || code == 0 {
			t.Errorf("cmdNote(%v) = %d, %v; want a usage error", args, code, err)
		}
	}
}

func TestCmdNoteSyncEndToEnd(t *testing.T) {
	a, vault := noteApp(t)

	at := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	ended := at.Add(time.Hour)
	rec := wf.Record{
		ID: "01TASK", Handle: "neck", Created: at, Updated: ended,
		Runs: []wf.Run{{ID: "r1", Workflow: "plan-to-pr", Model: "opus-5", Started: at, Ended: &ended, Outcome: wf.SessionDone}},
		Bindings: wf.Bindings{
			{Kind: wf.KindQueue, Ref: "01TASK", Label: "Add the parser", State: wf.BindingLive, At: at,
				Meta: map[string]string{wf.MetaBackend: "kata", wf.MetaShortID: "neck"}},
			{Kind: wf.KindPR, Ref: "https://github.com/me/app/pull/412", State: wf.BindingLive, At: at, Via: "r1"},
		},
	}
	if err := store.New(store.Root(a.cfg.Path)).Save(rec); err != nil {
		t.Fatal(err)
	}

	// Resolved by handle, which is the ref a human has in front of them.
	if code, err := a.cmdNote([]string{"sync", "neck"}); err != nil || code != 0 {
		t.Fatalf("cmdNote(sync neck) = %d, %v", code, err)
	}

	path := filepath.Join(vault, "Tasks", "Add the parser.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no note written: %v", err)
	}
	text := string(raw)
	for _, want := range []string{"wf-task: 01TASK", "# Add the parser", "%% wf:begin %%", "**run 1**", "#412"} {
		if !strings.Contains(text, want) {
			t.Errorf("note is missing %q:\n%s", want, text)
		}
	}

	// The sweep finds it, and finds nothing to do.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if code, err := a.cmdNote([]string{"sync", "--all"}); err != nil || code != 0 {
		t.Fatalf("cmdNote(sync --all) = %d, %v", code, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("the sweep rewrote an unchanged note")
	}
}

// A ledger the sweep cannot fully read is reported and does not fail the
// tasks it could read — the exit code says something went wrong, the
// records that were fine are still synced.
func TestCmdNoteSyncAllReportsSkips(t *testing.T) {
	a, _ := noteApp(t)
	st := store.New(store.Root(a.cfg.Path))
	if err := st.Save(wf.Record{ID: "01GOOD", Handle: "neck", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), "01BROKEN.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, err := a.cmdNote([]string{"sync", "--all"})
	if err != nil {
		t.Fatalf("cmdNote(sync --all) error = %v, want the sweep to survive", err)
	}
	if code == 0 {
		t.Error("exit code = 0, want a non-zero code when part of the ledger was unreadable")
	}
}
