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
	// Ports remembers the port difit last actually bound for each ref, so
	// a reopened task is likely — not guaranteed — to land back on the
	// same browser origin. See the package comment below for why this
	// exists and why it can't do better than "likely". Additive: a
	// review.json written before this field existed has no "ports" key at
	// all, which decodes as a nil slice rather than an error, the same
	// tolerance LoadState already gives a wholly missing file.
	Ports []PortBinding `json:"ports,omitempty"`
}

// PortBinding is one ref's remembered port.
type PortBinding struct {
	Ref  string `json:"ref"`
	Port int    `json:"port"`
}

// difit's comments live in the browser's own localStorage, which is
// scoped per *origin* — and the origin is localhost:<port>. difit picks a
// preferred port but silently falls back when it's occupied (verified:
// two instances asking for 4980 got 4980 and 4981 back; it also steps
// over ports held by unrelated, non-difit processes), so a task reopened
// on a different port shows up as a blank comment store even though the
// earlier comments are still sitting on the origin nothing returns to.
//
// Remembering the port a ref last bound, and asking difit for that same
// port again next time, makes reuse *likely*. It cannot make it
// guaranteed: a foreign process squatting the remembered port still costs
// that task its previous comments, and nothing wf does can stop that.
// See DESIGN.md for why a deterministic hash-of-ref port was rejected in
// favor of this.

// maxRememberedPorts caps how many refs' ports review.json retains, so
// the file cannot grow without bound across a long-lived install. 50 is
// comfortably more refs than anyone plausibly keeps reopening for review.
const maxRememberedPorts = 50

// portFor looks up the port last remembered for ref, or 0 if none is
// recorded. 0 doubles as "not found" rather than a distinct sentinel
// because a real difit server never actually binds port 0.
func portFor(ports []PortBinding, ref string) int {
	for _, p := range ports {
		if p.Ref == ref {
			return p.Port
		}
	}
	return 0
}

// rememberPort records the port difit actually reported for ref —
// deliberately the bound port, never the one requested, since --port's
// silent fallback means those can differ — moving ref to the
// most-recently-used end and evicting the oldest entry once the list
// would grow past maxRememberedPorts.
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
