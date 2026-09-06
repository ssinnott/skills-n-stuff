package review

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func TestSessionOpenSpawnsAndRecordsState(t *testing.T) {
	dir := t.TempDir() // stands in for a live worktree
	statePath := filepath.Join(t.TempDir(), "review.json")
	spawner := &fakeSpawner{stdout: `{"port":4966,"url":"http://localhost:4966","pid":3983}`}

	task := wf.Task{
		ID: "01HZ", ShortID: "neck", Title: "Add the parser",
		Meta: map[string]any{
			wf.SessionWorkspaceKey: dir,
			wf.SessionPathKey:      "/s/1.jsonl",
		},
	}

	s := &Session{Spawner: spawner, StatePath: statePath}
	result, err := s.Open(context.Background(), task, &config.Config{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if result.Target.Kind != KindWorktree {
		t.Fatalf("Target.Kind = %q, want worktree", result.Target.Kind)
	}
	if result.Viewer.PID != 3983 {
		t.Errorf("Viewer = %+v", result.Viewer)
	}

	saved, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if saved.PID != 3983 || saved.Ref != "neck" {
		t.Errorf("saved state = %+v, want the new viewer recorded", saved)
	}
}

func TestSessionOpenReplacesThePreviousViewer(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "review.json")

	// A record left by a viewer whose pid is certainly not alive: Open must
	// still proceed rather than erroring on the stale kill.
	if err := SaveState(statePath, State{Ref: "old", PID: 1 << 30}); err != nil {
		t.Fatal(err)
	}

	spawner := &fakeSpawner{stdout: `{"port":5000,"url":"http://localhost:5000","pid":42}`}
	task := wf.Task{
		ID: "01HZ", ShortID: "neck", Title: "Add the parser",
		Meta: map[string]any{wf.SessionWorkspaceKey: dir, wf.SessionPathKey: "/s/1.jsonl"},
	}

	s := &Session{Spawner: spawner, StatePath: statePath}
	result, err := s.Open(context.Background(), task, &config.Config{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if result.Viewer.PID != 42 {
		t.Errorf("Viewer = %+v, want the new viewer", result.Viewer)
	}

	saved, _ := LoadState(statePath)
	if saved.Ref != "neck" || saved.PID != 42 {
		t.Errorf("saved state = %+v, want it replaced", saved)
	}
}

func TestSessionOpenDocTargetSpawnsNothing(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "review.json")
	spawner := &fakeSpawner{stdout: `{"port":1,"url":"http://localhost:1","pid":1}`}

	task := wf.Task{
		ID: "01HZ", ShortID: "neck", Title: "Research the thing",
		Meta: map[string]any{wf.ObsidianNoteKey: "Research/plan.md"},
	}

	s := &Session{Spawner: spawner, StatePath: statePath}
	result, err := s.Open(context.Background(), task, &config.Config{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if result.Target.Kind != KindDoc || result.Target.Note != "Research/plan.md" {
		t.Errorf("Target = %+v", result.Target)
	}
	if spawner.gotArgs != nil {
		t.Error("a doc target must not spawn a viewer")
	}
	if _, err := LoadState(statePath); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadState(statePath); !reflect.DeepEqual(got, State{}) {
		t.Error("a doc target must not write a viewer record")
	}
}

func TestSessionOpenSeedsFindingsAndReportsCount(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "review.json")
	spawner := &fakeSpawner{stdout: `{"port":4966,"url":"http://localhost:4966","pid":3983}`}

	// The findings come off the task's own issue bindings, so the run that
	// filed them is the only thing that has to have happened.
	task := wf.Task{
		ID: "01HZ", ShortID: "neck", Title: "Add the parser",
		Meta: map[string]any{
			wf.SessionWorkspaceKey: dir,
			wf.SessionPathKey:      "/s/1.jsonl",
			wf.IssuesKey: `[{"url":"https://a/i1","title":"internal/foo.go:42 nil check"},` +
				`{"url":"https://a/i2","title":"no anchor here"}]`,
		},
	}

	s := &Session{Spawner: spawner, StatePath: statePath}
	result, err := s.Open(context.Background(), task, &config.Config{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if result.Seeded != 1 {
		t.Errorf("Seeded = %d, want 1 anchored finding", result.Seeded)
	}
	found := false
	for _, a := range spawner.gotArgs {
		if a == "--comment" {
			found = true
		}
	}
	if !found {
		t.Error("Launch args should include a --comment flag for the anchored finding")
	}
}

func TestSessionOpenNoTargetReturnsError(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "review.json")
	task := wf.Task{ID: "01HZ", ShortID: "neck", Title: "Nothing to show"}

	s := &Session{Spawner: &fakeSpawner{}, StatePath: statePath}
	if _, err := s.Open(context.Background(), task, &config.Config{}); err == nil {
		t.Error("Open() = nil error, want ErrNoTarget for a task with nothing recorded")
	}
}

func TestSessionStopKillsAndClearsState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "review.json")
	if err := SaveState(statePath, State{Ref: "neck", PID: 1 << 30}); err != nil {
		t.Fatal(err)
	}

	s := &Session{StatePath: statePath}
	stopped, err := s.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !stopped {
		t.Error("Stop() = false, want true when a viewer was recorded")
	}
	if got, _ := LoadState(statePath); !reflect.DeepEqual(got, State{}) {
		t.Error("Stop() must clear the state record")
	}
}

func TestSessionStopWithNothingRunningIsNotAnError(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "review.json")
	s := &Session{StatePath: statePath}

	stopped, err := s.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if stopped {
		t.Error("Stop() = true, want false when nothing was running")
	}
}

func TestSessionOpenPRBypassesTheLadderEntirely(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "review.json")
	spawner := &fakeSpawner{stdout: `{"port":4966,"url":"http://localhost:4966","pid":3983}`}

	s := &Session{Spawner: spawner, StatePath: statePath}
	result, err := s.OpenPR(context.Background(), "https://github.com/acme/widgets/pull/482", "/repo")
	if err != nil {
		t.Fatalf("OpenPR() error = %v", err)
	}
	if result.Ref != "acme/widgets#482" {
		t.Errorf("Ref = %q, want the derived PR handle", result.Ref)
	}
	if result.Target.Kind != KindPR || result.Target.PR != "https://github.com/acme/widgets/pull/482" {
		t.Errorf("Target = %+v", result.Target)
	}
	if result.Seeded != 0 {
		t.Errorf("Seeded = %d, want 0 — there is no task to seed findings from", result.Seeded)
	}
	if spawner.gotDir != "/repo" {
		t.Errorf("Spawn() dir = %q, want the given --repo", spawner.gotDir)
	}
	want := []string{"--pr", "https://github.com/acme/widgets/pull/482", "--no-open", "--background"}
	if len(spawner.gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", spawner.gotArgs, want)
	}
	for i := range want {
		if spawner.gotArgs[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, spawner.gotArgs[i], want[i])
		}
	}

	saved, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Ref != "acme/widgets#482" || saved.PID != 3983 {
		t.Errorf("saved state = %+v, want the ad-hoc PR viewer recorded", saved)
	}
}

func TestSessionOpenPRReplacesThePreviousViewer(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "review.json")
	if err := SaveState(statePath, State{Ref: "old", PID: 1 << 30}); err != nil {
		t.Fatal(err)
	}

	spawner := &fakeSpawner{stdout: `{"port":5000,"url":"http://localhost:5000","pid":42}`}
	s := &Session{Spawner: spawner, StatePath: statePath}
	result, err := s.OpenPR(context.Background(), "https://github.com/acme/widgets/pull/1", "/repo")
	if err != nil {
		t.Fatalf("OpenPR() error = %v", err)
	}
	if result.Viewer.PID != 42 {
		t.Errorf("Viewer = %+v, want the new viewer, replacing the old one", result.Viewer)
	}
}

func TestSessionOpenRequestsRefsRememberedPort(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "review.json")
	if err := SaveState(statePath, State{Ports: []PortBinding{{Ref: "neck", Port: 4981}}}); err != nil {
		t.Fatal(err)
	}

	spawner := &fakeSpawner{stdout: `{"port":4981,"url":"http://localhost:4981","pid":1}`}
	task := wf.Task{
		ID: "01HZ", ShortID: "neck", Title: "Add the parser",
		Meta: map[string]any{wf.SessionWorkspaceKey: dir, wf.SessionPathKey: "/s/1.jsonl"},
	}

	s := &Session{Spawner: spawner, StatePath: statePath}
	if _, err := s.Open(context.Background(), task, &config.Config{}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	found := false
	for i, a := range spawner.gotArgs {
		if a == "--port" && i+1 < len(spawner.gotArgs) && spawner.gotArgs[i+1] == "4981" {
			found = true
		}
	}
	if !found {
		t.Errorf("args = %v, want --port 4981 requested for neck's remembered port", spawner.gotArgs)
	}
}

func TestSessionOpenWithNoRememberedPortAsksForNone(t *testing.T) {
	// A ref reviewed for the first time has no remembered port; --port
	// must not appear at all so difit picks its own default.
	dir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "review.json")
	spawner := &fakeSpawner{stdout: `{"port":4966,"url":"http://localhost:4966","pid":1}`}
	task := wf.Task{
		ID: "01HZ", ShortID: "neck", Title: "Add the parser",
		Meta: map[string]any{wf.SessionWorkspaceKey: dir, wf.SessionPathKey: "/s/1.jsonl"},
	}

	s := &Session{Spawner: spawner, StatePath: statePath}
	if _, err := s.Open(context.Background(), task, &config.Config{}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	for _, a := range spawner.gotArgs {
		if a == "--port" {
			t.Errorf("args = %v, want no --port flag with nothing remembered", spawner.gotArgs)
		}
	}
}

func TestSessionOpenRecordsTheBoundPortNotTheRequestedOne(t *testing.T) {
	// difit silently falls back when the requested port is occupied; the
	// state file must remember what it actually bound, not what was
	// asked for — that is the entire point of recording it at all.
	dir := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "review.json")
	if err := SaveState(statePath, State{Ports: []PortBinding{{Ref: "neck", Port: 4980}}}); err != nil {
		t.Fatal(err)
	}

	spawner := &fakeSpawner{stdout: `{"port":4981,"url":"http://localhost:4981","pid":1}`}
	task := wf.Task{
		ID: "01HZ", ShortID: "neck", Title: "Add the parser",
		Meta: map[string]any{wf.SessionWorkspaceKey: dir, wf.SessionPathKey: "/s/1.jsonl"},
	}

	s := &Session{Spawner: spawner, StatePath: statePath}
	if _, err := s.Open(context.Background(), task, &config.Config{}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	saved, _ := LoadState(statePath)
	if got := portFor(saved.Ports, "neck"); got != 4981 {
		t.Errorf("remembered port = %d, want the bound port 4981, not the requested 4980", got)
	}
}
