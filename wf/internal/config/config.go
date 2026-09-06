// Package config loads wf's JSON settings file, applying defaults for
// anything unset. See DESIGN.md for why wf has no third-party dependencies.
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
	// Actor identifies this wf instance in leases. Defaults to wf@<hostname>.
	Actor string `json:"actor"`
	// Repo is the default repository worktrees are cut from.
	Repo         string `json:"repo"`
	Base         string `json:"base"`
	WorktreeRoot string `json:"worktreeRoot"`
	// SessionRoot is where wf-owned pi session files are written.
	SessionRoot string `json:"sessionRoot"`
	// WorkflowDir holds canned workflow markdown files.
	WorkflowDir string `json:"workflowDir"`
	// Vault is the Obsidian vault root that artifacts bind into.
	Vault string `json:"vault"`
	// Profiles maps workflow profile names to PI_CODING_AGENT_DIR paths.
	Profiles map[string]string `json:"profiles"`
	// DefaultProfile is used when a workflow names none.
	DefaultProfile string `json:"defaultProfile"`
	// DefaultModel is used when a workflow names none.
	DefaultModel string `json:"defaultModel"`
	// KataBin and PiBin override binaries often off PATH under a launchd/systemd unit.
	KataBin string `json:"kataBin"`
	PiBin   string `json:"piBin"`
	// DifitCommand is the review viewer's shell command line, defaulting to "npx difit".
	DifitCommand    string `json:"difitCommand"`
	MaxConcurrent   int    `json:"maxConcurrent"`
	LeaseTTLSeconds int    `json:"leaseTTLSeconds"`

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

// ProfileDir resolves a workflow's profile name to a directory, or "" if unknown.
func (c *Config) ProfileDir(name string) string {
	if name == "" {
		name = c.DefaultProfile
	}
	if name == "" {
		return ""
	}
	return c.Profiles[name]
}

// ResolveModel picks the workflow's own model, falling back to the configured default.
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
