package review

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")

	want := State{
		Ref: "neck", PID: 3983, Port: 4966, URL: "http://localhost:4966",
		Started: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
	}
	if err := SaveState(path, want); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if got != want {
		t.Errorf("LoadState() = %+v, want %+v", got, want)
	}
}

func TestLoadStateMissingFileIsZeroValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v, want nil for a missing file", err)
	}
	if got != (State{}) {
		t.Errorf("LoadState() = %+v, want zero value", got)
	}
}

func TestSaveStateReplacesPreviousViewer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")

	if err := SaveState(path, State{Ref: "first", PID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(path, State{Ref: "second", PID: 2}); err != nil {
		t.Fatal(err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != "second" || got.PID != 2 {
		t.Errorf("LoadState() = %+v, want the latest save", got)
	}
}

func TestClearStateRemovesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	if err := SaveState(path, State{Ref: "neck", PID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := ClearState(path); err != nil {
		t.Fatalf("ClearState() error = %v", err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() after clear error = %v", err)
	}
	if got != (State{}) {
		t.Errorf("LoadState() after clear = %+v, want zero value", got)
	}
}

func TestClearStateOnAbsentFileIsNotAnError(t *testing.T) {
	// Stopping twice, or stopping when nothing ever ran, must not fail.
	path := filepath.Join(t.TempDir(), "never-written.json")
	if err := ClearState(path); err != nil {
		t.Errorf("ClearState() on an absent file error = %v, want nil", err)
	}
}

func TestStatePathSitsBesideConfig(t *testing.T) {
	got := StatePath(filepath.Join("/home/me/.wf", "config.json"))
	want := filepath.Join("/home/me/.wf", "review.json")
	if got != want {
		t.Errorf("StatePath() = %q, want %q", got, want)
	}
}

func TestStatePathFallsBackToDefaultConfigDir(t *testing.T) {
	// An empty cfgPath means defaults were used, which resolves the same
	// directory config.DefaultPath does.
	if got := StatePath(""); filepath.Base(got) != "review.json" {
		t.Errorf("StatePath(\"\") = %q, want a review.json path", got)
	}
}
