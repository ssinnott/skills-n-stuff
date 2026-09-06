package review

// The live-viewer record.
//
// There is one review pane: a human has one browser tab open on one diff at
// a time, so a new `wf review` replaces whatever difit is already running
// rather than accumulating orphaned background servers. The record lives
// beside wf's own config — the same neighborhood as sessions and
// worktrees — so it survives process restarts and is inspectable with a
// plain `cat`.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
)

// State is the one live viewer wf knows about.
type State struct {
	Ref     string    `json:"ref"`
	PID     int       `json:"pid"`
	Port    int       `json:"port"`
	URL     string    `json:"url"`
	Started time.Time `json:"started"`
}

// StatePath resolves the review state file next to wf's config — the same
// directory config.Load reads from, so `--config` moves both together.
// cfgPath is a loaded Config's own Path field; empty means defaults, which
// resolves the same directory config.DefaultPath does.
func StatePath(cfgPath string) string {
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	return filepath.Join(filepath.Dir(cfgPath), "review.json")
}

// LoadState reads the recorded viewer. A missing file is not an error — no
// review has run yet — and reads as a zero State.
func LoadState(path string) (State, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read review state %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("parse review state %s: %w", path, err)
	}
	return s, nil
}

// SaveState records the viewer that now owns the review pane, replacing
// whatever was recorded before.
func SaveState(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	encoded, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode review state: %w", err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write review state %s: %w", path, err)
	}
	return nil
}

// ClearState removes the record after `--stop`. Removing an already-absent
// file is not an error: stopping twice must not fail the second time.
func ClearState(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove review state %s: %w", path, err)
	}
	return nil
}
