package review

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadState() = %+v, want %+v", got, want)
	}
}

func TestLoadStateMissingFileIsZeroValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v, want nil for a missing file", err)
	}
	if !reflect.DeepEqual(got, State{}) {
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
	if !reflect.DeepEqual(got, State{}) {
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

func TestStatePortsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	want := State{
		Ref: "neck", PID: 1, Port: 4981, URL: "http://localhost:4981",
		Ports: []PortBinding{{Ref: "neck", Port: 4981}, {Ref: "other", Port: 4980}},
	}
	if err := SaveState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadState() = %+v, want %+v", got, want)
	}
}

func TestLoadStateToleratesFileWithNoPortsField(t *testing.T) {
	// A review.json written before port memory existed has no "ports" key
	// at all — this must load as an empty slice, not fail, the same
	// tolerance LoadState already gives a wholly missing file.
	path := filepath.Join(t.TempDir(), "review.json")
	old := `{"ref":"neck","pid":3983,"port":4966,"url":"http://localhost:4966","started":"2026-09-06T12:00:00Z"}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v, want nil for a pre-port-memory file", err)
	}
	if got.Ref != "neck" || got.PID != 3983 {
		t.Errorf("LoadState() = %+v, want the old fields preserved", got)
	}
	if len(got.Ports) != 0 {
		t.Errorf("Ports = %v, want empty for a file predating port memory", got.Ports)
	}
}

func TestPortForLooksUpRememberedPort(t *testing.T) {
	ports := []PortBinding{{Ref: "a", Port: 4980}, {Ref: "b", Port: 4981}}
	if got := portFor(ports, "b"); got != 4981 {
		t.Errorf("portFor() = %d, want 4981", got)
	}
	if got := portFor(ports, "missing"); got != 0 {
		t.Errorf("portFor() = %d, want 0 for an unremembered ref", got)
	}
	if got := portFor(nil, "a"); got != 0 {
		t.Errorf("portFor(nil, ...) = %d, want 0", got)
	}
}

func TestRememberPortReplacesRatherThanDuplicatesAnExistingRef(t *testing.T) {
	ports := rememberPort(nil, "a", 4980)
	ports = rememberPort(ports, "b", 4981)
	ports = rememberPort(ports, "a", 4982) // a reopened, difit fell back to a new port

	if len(ports) != 2 {
		t.Fatalf("len(ports) = %d, want 2 (no duplicate entry for a)", len(ports))
	}
	if got := portFor(ports, "a"); got != 4982 {
		t.Errorf("portFor(a) = %d, want the updated port 4982", got)
	}
	if got := portFor(ports, "b"); got != 4981 {
		t.Errorf("portFor(b) = %d, want it untouched", got)
	}
}

func TestRememberPortEvictsOldestPastCap(t *testing.T) {
	var ports []PortBinding
	for i := 0; i < maxRememberedPorts+5; i++ {
		ports = rememberPort(ports, fmt.Sprintf("ref-%d", i), i+1)
	}
	if len(ports) != maxRememberedPorts {
		t.Fatalf("len(ports) = %d, want capped at %d", len(ports), maxRememberedPorts)
	}
	if got := portFor(ports, "ref-0"); got != 0 {
		t.Errorf("portFor(ref-0) = %d, want 0 — the oldest ref should have been evicted", got)
	}
	if got := portFor(ports, "ref-4"); got != 0 {
		t.Errorf("portFor(ref-4) = %d, want 0 — also evicted to make room", got)
	}
	last := maxRememberedPorts + 4
	if got := portFor(ports, fmt.Sprintf("ref-%d", last)); got != last+1 {
		t.Errorf("portFor(newest) = %d, want %d — the most recent ref must survive", got, last+1)
	}
}
