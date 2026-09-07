package review

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
)

// State is the one live viewer wf knows about, replaced rather than accumulated.
type State struct {
	Ref     string    `json:"ref"`
	PID     int       `json:"pid"`
	Port    int       `json:"port"`
	URL     string    `json:"url"`
	Started time.Time `json:"started"`
	// Ports remembers the port difit last bound for each ref.
	Ports []PortBinding `json:"ports,omitempty"`
}

// PortBinding is one ref's remembered port.
type PortBinding struct {
	Ref  string `json:"ref"`
	Port int    `json:"port"`
}

// difit's comments live in localStorage, scoped per origin
// (localhost:<port>); difit falls back silently when its preferred port
// is occupied (verified: two instances asking for 4980 got 4980 and 4981
// back). See DESIGN.md.

// maxRememberedPorts caps how many refs' ports review.json retains.
const maxRememberedPorts = 50

// portFor looks up the port last remembered for ref, or 0 if none.
func portFor(ports []PortBinding, ref string) int {
	for _, p := range ports {
		if p.Ref == ref {
			return p.Port
		}
	}
	return 0
}

// rememberPort records the port difit actually bound for ref, evicting
// the oldest entry past maxRememberedPorts.
func rememberPort(ports []PortBinding, ref string, port int) []PortBinding {
	out := make([]PortBinding, 0, len(ports)+1)
	for _, p := range ports {
		if p.Ref != ref {
			out = append(out, p)
		}
	}
	out = append(out, PortBinding{Ref: ref, Port: port})
	if len(out) > maxRememberedPorts {
		out = out[len(out)-maxRememberedPorts:]
	}
	return out
}

// StatePath resolves the review state file next to wf's config.
func StatePath(cfgPath string) string {
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	return filepath.Join(filepath.Dir(cfgPath), "review.json")
}

// LoadState reads the recorded viewer; a missing file reads as zero.
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

// SaveState records the viewer that now owns the review pane.
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

// ClearState removes the record after `--stop`.
func ClearState(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove review state %s: %w", path, err)
	}
	return nil
}
