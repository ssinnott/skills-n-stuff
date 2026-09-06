package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func TestNewSessionPathIsIdentifiable(t *testing.T) {
	now := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)
	id, path := NewSessionPath("/sessions", "abc4", now)

	if !strings.HasPrefix(id, "abc4-20260905T143000-") {
		t.Errorf("id = %q, want the task ref and timestamp", id)
	}
	if filepath.Dir(path) != "/sessions" || !strings.HasSuffix(path, ".jsonl") {
		t.Errorf("path = %q", path)
	}
}

func TestNewSessionPathAvoidsCollisions(t *testing.T) {
	now := time.Now()
	// Two runs of one task in the same second must not share a session file.
	_, first := NewSessionPath("/sessions", "abc4", now)
	_, second := NewSessionPath("/sessions", "abc4", now)
	if first == second {
		t.Error("two session paths collided within the same second")
	}
}

func TestNewSessionPathSanitizesRef(t *testing.T) {
	id, _ := NewSessionPath("/sessions", "Proj#ABC/4", time.Now())
	if strings.ContainsAny(id, "#/") {
		t.Errorf("id = %q still contains path-unsafe characters", id)
	}
	if !strings.HasPrefix(id, "proj-abc-4-") {
		t.Errorf("id = %q, want a lowercased sanitized ref", id)
	}
}

func TestBuildArgsPassesSessionAndPrompt(t *testing.T) {
	p := &Pi{}
	args := p.BuildArgs("/sessions/abc4.jsonl", "do the thing", "")

	// wf mints the session path so the binding is writable before the agent
	// produces anything — that is the whole reason attach works on a crash.
	if args[0] != "--session" || args[1] != "/sessions/abc4.jsonl" {
		t.Errorf("args = %v, want --session first", args)
	}
	if args[len(args)-1] != "do the thing" {
		t.Errorf("prompt must be last, got %v", args)
	}
	if !contains(args, "-p") {
		t.Errorf("args = %v, want print mode", args)
	}
}

func TestBuildArgsIncludesExtras(t *testing.T) {
	p := &Pi{ExtraArgs: []string{"--dangerously-skip-permissions"}}
	args := p.BuildArgs("/s/a.jsonl", "prompt", "")
	if !contains(args, "--dangerously-skip-permissions") || args[len(args)-1] != "prompt" {
		t.Errorf("args = %v, want extras before the prompt", args)
	}
}

func TestBuildArgsIncludesModel(t *testing.T) {
	p := &Pi{}
	args := p.BuildArgs("/s/a.jsonl", "prompt", "opus")
	if !contains(args, "--model") || args[len(args)-1] != "prompt" {
		t.Errorf("args = %v, want --model before the prompt", args)
	}
	for i, v := range args {
		if v == "--model" && (i+1 >= len(args) || args[i+1] != "opus") {
			t.Errorf("args = %v, want opus to follow --model", args)
		}
	}
}

func TestBuildArgsOmitsModelWhenEmpty(t *testing.T) {
	p := &Pi{}
	args := p.BuildArgs("/s/a.jsonl", "prompt", "")
	if contains(args, "--model") {
		t.Errorf("args = %v, want no --model flag when unset", args)
	}
}

// A real subprocess run, using a stub binary rather than pi: it proves the
// spawn path, the transcript capture, and the profile env var without
// needing pi installed.
func TestStartCapturesOutputAndProfile(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no shell available")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "fake-pi")
	script := "#!/bin/sh\necho \"profile=$PI_CODING_AGENT_DIR\"\necho \"DONE\"\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := &Pi{Bin: stub, SessionRoot: filepath.Join(dir, "sessions")}
	handle, err := p.Start(context.Background(), wf.RunOptions{
		Cwd:        dir,
		Prompt:     "do it",
		TaskRef:    "abc4",
		ProfileDir: "/profiles/coding",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// The binding is knowable before the run finishes.
	if handle.SessionPath() == "" || handle.SessionID() == "" {
		t.Error("session must be identifiable at spawn")
	}

	result, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if !result.OK {
		t.Errorf("result = %+v, want OK", result)
	}
	if !strings.Contains(result.TranscriptTail, "profile=/profiles/coding") {
		t.Errorf("PI_CODING_AGENT_DIR not passed through:\n%s", result.TranscriptTail)
	}
	if outcomes := wf.ParseOutcomes(result.TranscriptTail); !wf.IsComplete(outcomes) {
		t.Error("transcript should carry the agent's outcome verbs")
	}
}

func TestStartNonZeroExitIsNotAnError(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no shell available")
	}

	dir := t.TempDir()
	stub := filepath.Join(dir, "failing-pi")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'blew up' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	p := &Pi{Bin: stub, SessionRoot: filepath.Join(dir, "sessions")}
	handle, err := p.Start(context.Background(), wf.RunOptions{Cwd: dir, Prompt: "x", TaskRef: "abc4"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// A failed agent still escalates through the normal path, so Wait must
	// hand back the transcript rather than an error.
	result, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v, want the failure reported in the result", err)
	}
	if result.OK || result.ExitCode != 3 {
		t.Errorf("result = %+v, want a failed run with exit 3", result)
	}
	if !strings.Contains(result.TranscriptTail, "blew up") {
		t.Errorf("stderr must reach the transcript:\n%s", result.TranscriptTail)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
