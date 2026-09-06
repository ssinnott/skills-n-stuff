package main

// Shared test harness for driving the CLI's own entry point end to end,
// against a real FileStore on disk. review_test.go's siblings use this to
// exercise `wf review --pr`, which needs no queue at all.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
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
