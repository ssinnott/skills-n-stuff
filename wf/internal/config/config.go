// Package config loads wf's settings.
//
// JSON rather than TOML because the standard library reads JSON and wf has
// no dependencies. Every path field accepts a leading ~.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the whole of wf's configuration.
type Config struct {
	// Actor identifies this wf instance in leases. Defaults to
	// wf@<hostname>, which is enough to tell two machines apart.
	Actor string `json:"actor"`
	// Repo is the default repository worktrees are cut from.
	Repo string `json:"repo"`
	// Base is the default branch worktrees branch from.
	Base string `json:"base"`
	// WorktreeRoot is where worktrees are created.
	WorktreeRoot string `json:"worktreeRoot"`
	// SessionRoot is where wf-owned pi session files are written.
	SessionRoot string `json:"sessionRoot"`
	// WorkflowDir holds canned workflow markdown files.
	WorkflowDir string `json:"workflowDir"`
	// Vault is the Obsidian vault root that artifacts bind into.
	Vault string `json:"vault"`
	// NoteDir is where `wf note sync` creates a task note for a task that
	// has none, relative to the vault root. Separate from a workflow's
	// vault-dir because the two hold different things: produced documents
	// land where the workflow that produced them says, while a task note
	// is wf's own face for the task and belongs wherever the vault keeps
	// those. Empty takes notesync's default; the fallback lives there
	// rather than here because config cannot import it without a cycle.
	NoteDir string `json:"noteDir"`
	// Profiles maps workflow profile names to PI_CODING_AGENT_DIR paths.
	Profiles map[string]string `json:"profiles"`
	// DefaultProfile is used when a workflow names none.
	DefaultProfile string `json:"defaultProfile"`
	// DefaultModel is used when a workflow names none.
	DefaultModel string `json:"defaultModel"`
	// KataBin, PiBin and GhBin override binaries that are often off PATH
	// under a launchd or systemd unit. GhBin is the GitHub CLI wf asks
	// about pull request state; like the others its absence is a runtime
	// failure, and one that degrades to "state unknown" rather than to a
	// wrong answer.
	KataBin string `json:"kataBin"`
	PiBin   string `json:"piBin"`
	GhBin   string `json:"ghBin"`
	// DifitCommand is the review viewer, as a shell-style command line
	// rather than a bare binary: the default is the two-word "npx difit"
	// so a checkout with no global install still works. DIFIT_BIN
	// overrides it the same way KATA_BIN and PI_BIN override their own
	// binaries.
	DifitCommand string `json:"difitCommand"`
	// MaxConcurrent caps simultaneous runs.
	MaxConcurrent int `json:"maxConcurrent"`
	// LeaseTTLSeconds overrides the default lease window.
	LeaseTTLSeconds int `json:"leaseTTLSeconds"`

	// Path records where this config was loaded from, or "" for defaults.
	Path string `json:"-"`
}

// DefaultPath is where wf looks when no path is given.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wf/config.json"
	}
	return filepath.Join(home, ".wf", "config.json")
}

// Load reads config from path, falling back to defaults when it is absent.
// A missing config is not an error: wf runs on defaults plus flags.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}

	cfg := &Config{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		cfg.Path = path
	case os.IsNotExist(err):
		// Defaults only.
	default:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	home, _ := os.UserHomeDir()

	if c.Actor == "" {
		host, err := os.Hostname()
		if err != nil {
			host = "unknown"
		}
		c.Actor = "wf@" + host
	}
	if c.WorktreeRoot == "" && home != "" {
		c.WorktreeRoot = filepath.Join(home, ".wf", "worktrees")
	}
	if c.SessionRoot == "" && home != "" {
		c.SessionRoot = filepath.Join(home, ".wf", "sessions")
	}
	if c.WorkflowDir == "" && home != "" {
		c.WorkflowDir = filepath.Join(home, ".wf", "workflows")
	}
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 1
	}
	if c.DifitCommand == "" {
		c.DifitCommand = "npx difit"
	}

	c.Repo = Expand(c.Repo)
	c.WorktreeRoot = Expand(c.WorktreeRoot)
	c.SessionRoot = Expand(c.SessionRoot)
	c.WorkflowDir = Expand(c.WorkflowDir)
	c.Vault = Expand(c.Vault)

	for name, dir := range c.Profiles {
		c.Profiles[name] = Expand(dir)
	}
}

// ProfileDir resolves a workflow's profile name to a directory. An unknown
// name resolves to empty rather than failing: running under the default
// profile is better than refusing to run at all.
func (c *Config) ProfileDir(name string) string {
	if name == "" {
		name = c.DefaultProfile
	}
	if name == "" {
		return ""
	}
	return c.Profiles[name]
}

// ResolveModel picks the model a run should use: the workflow's own choice
// first, falling back to the configured default. Unlike ProfileDir this
// needs no lookup table — a workflow's model is already the value pi wants,
// not a name to resolve further.
func (c *Config) ResolveModel(workflowModel string) string {
	if workflowModel != "" {
		return workflowModel
	}
	return c.DefaultModel
}

// Expand resolves a leading ~ against the home directory.
func Expand(path string) string {
	if path == "" || !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
