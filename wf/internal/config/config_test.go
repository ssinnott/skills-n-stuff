package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load() error = %v, want defaults for a missing file", err)
	}
	if cfg.Actor == "" {
		t.Error("Actor must default to something identifying this instance")
	}
	if cfg.MaxConcurrent != 1 {
		t.Errorf("MaxConcurrent = %d, want 1", cfg.MaxConcurrent)
	}
}

func TestLoadReadsAndExpands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{
		"actor": "wf-laptop",
		"vault": "~/vault",
		"repo": "~/code/app",
		"maxConcurrent": 3,
		"profiles": {"coding": "~/.pi/profiles/coding"},
		"defaultProfile": "coding"
	}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Actor != "wf-laptop" || cfg.MaxConcurrent != 3 {
		t.Errorf("scalars not read: %+v", cfg)
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(cfg.Vault, "~") {
		t.Errorf("Vault = %q, want ~ expanded", cfg.Vault)
	}
	if home != "" && strings.HasPrefix(cfg.Profiles["coding"], "~") {
		t.Errorf("profile path = %q, want ~ expanded", cfg.Profiles["coding"])
	}
	if cfg.Path != path {
		t.Errorf("Path = %q, want the file it came from", cfg.Path)
	}
}

func TestLoadRejectsBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a malformed config must be an error, not silently ignored")
	}
}

func TestProfileDir(t *testing.T) {
	cfg := &Config{
		Profiles:       map[string]string{"coding": "/profiles/coding"},
		DefaultProfile: "coding",
	}

	if got := cfg.ProfileDir("coding"); got != "/profiles/coding" {
		t.Errorf("ProfileDir() = %q", got)
	}
	// An empty name falls back to the default profile.
	if got := cfg.ProfileDir(""); got != "/profiles/coding" {
		t.Errorf("ProfileDir(\"\") = %q, want the default", got)
	}
	// An unknown name resolves to empty: running under pi's own default is
	// better than refusing to run.
	if got := cfg.ProfileDir("nope"); got != "" {
		t.Errorf("ProfileDir(\"nope\") = %q, want empty", got)
	}
}

func TestResolveModel(t *testing.T) {
	cfg := &Config{DefaultModel: "claude-sonnet-5"}

	if got := cfg.ResolveModel("claude-opus-5"); got != "claude-opus-5" {
		t.Errorf("ResolveModel() = %q, want the workflow's own choice", got)
	}
	if got := cfg.ResolveModel(""); got != "claude-sonnet-5" {
		t.Errorf("ResolveModel(\"\") = %q, want the default", got)
	}
}

func TestExpand(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	if got := Expand("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("Expand(~/x) = %q", got)
	}
	if got := Expand("~"); got != home {
		t.Errorf("Expand(~) = %q", got)
	}
	for _, in := range []string{"", "/abs/path", "relative/path", "~user/path"} {
		if got := Expand(in); got != in {
			t.Errorf("Expand(%q) = %q, want unchanged", in, got)
		}
	}
}
