package review

import (
	"context"
	"strings"
	"testing"
)

func TestParseSpawnedSuccessLine(t *testing.T) {
	got, err := ParseSpawned(`{"port":4966,"url":"http://localhost:4966","pid":3983}`+"\n", "")
	if err != nil {
		t.Fatalf("ParseSpawned() error = %v", err)
	}
	if got.Port != 4966 || got.URL != "http://localhost:4966" || got.PID != 3983 {
		t.Errorf("ParseSpawned() = %+v", got)
	}
}

func TestParseSpawnedIgnoresSurroundingText(t *testing.T) {
	stdout := "npm notice fetching...\n" +
		`{"port":4966,"url":"http://localhost:4966","pid":3983}` + "\n" +
		"done\n"
	got, err := ParseSpawned(stdout, "")
	if err != nil {
		t.Fatalf("ParseSpawned() error = %v", err)
	}
	if got.Port != 4966 {
		t.Errorf("Port = %d, want 4966 found among surrounding text", got.Port)
	}
}

func TestParseSpawnedUsesActualPortNotRequested(t *testing.T) {
	// --port picks a preferred port and falls back if occupied, so a caller
	// that asked for one port must still get back whatever difit actually
	// bound.
	got, err := ParseSpawned(`{"port":5001,"url":"http://localhost:5001","pid":1}`, "")
	if err != nil {
		t.Fatalf("ParseSpawned() error = %v", err)
	}
	if got.Port != 5001 {
		t.Errorf("Port = %d, want the bound port regardless of what was requested", got.Port)
	}
}

func TestParseSpawnedNoJSONLineIsAnError(t *testing.T) {
	// Empirically verified: bad input prints "Error: ..." and no JSON line
	// at all, and the exit code is not reliable — this is the actual
	// failure signal.
	_, err := ParseSpawned("Error: Error: Invalid --comment JSON\n", "")
	if err == nil {
		t.Fatal("ParseSpawned() = nil error, want one when no JSON line is present")
	}
	if !strings.Contains(err.Error(), "Invalid --comment JSON") {
		t.Errorf("error = %v, want difit's own diagnostic text surfaced", err)
	}
}

func TestParseSpawnedEmptyOutputIsAnError(t *testing.T) {
	if _, err := ParseSpawned("", ""); err == nil {
		t.Fatal("ParseSpawned(\"\", \"\") = nil error, want one")
	}
}

func TestParseSpawnedSurfacesStderr(t *testing.T) {
	_, err := ParseSpawned("", "spawn ENOENT")
	if err == nil || !strings.Contains(err.Error(), "spawn ENOENT") {
		t.Errorf("error = %v, want stderr surfaced when stdout has nothing", err)
	}
}

// fakeSpawner stands in for a real difit process — internal/runner is
// stubbed the same way in the supervisor tests, so difit need not be
// installed to test this seam.
type fakeSpawner struct {
	stdout, stderr string
	gotDir         string
	gotArgs        []string
}

func (f *fakeSpawner) Spawn(_ context.Context, dir string, args []string) (string, string) {
	f.gotDir = dir
	f.gotArgs = args
	return f.stdout, f.stderr
}

func TestLaunchAppendsBackgroundFlagsAndComments(t *testing.T) {
	s := &fakeSpawner{stdout: `{"port":4966,"url":"http://localhost:4966","pid":3983}`}

	spawned, err := Launch(context.Background(), s, "/repo", []string{"--pr", "https://a/pr/1"}, []string{"--comment", `{"type":"thread"}`})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if spawned.PID != 3983 {
		t.Errorf("Launch() = %+v", spawned)
	}
	if s.gotDir != "/repo" {
		t.Errorf("Spawn() dir = %q, want the target's repo", s.gotDir)
	}
	want := []string{"--pr", "https://a/pr/1", "--no-open", "--background", "--comment", `{"type":"thread"}`}
	if len(s.gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", s.gotArgs, want)
	}
	for i := range want {
		if s.gotArgs[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, s.gotArgs[i], want[i])
		}
	}
}

func TestLaunchSurfacesFailureFromSpawner(t *testing.T) {
	s := &fakeSpawner{stdout: "Error: Error: Invalid --comment JSON"}
	if _, err := Launch(context.Background(), s, "/repo", []string{"."}, nil); err == nil {
		t.Fatal("Launch() = nil error, want the parse failure surfaced")
	}
}

func TestKillToleratesAbsentPID(t *testing.T) {
	if err := Kill(0); err != nil {
		t.Errorf("Kill(0) error = %v, want nil", err)
	}
	if err := Kill(-1); err != nil {
		t.Errorf("Kill(-1) error = %v, want nil", err)
	}
}

func TestKillToleratesAlreadyDeadPID(t *testing.T) {
	// A pid essentially guaranteed not to be alive: max pid on Linux plus
	// well beyond any real process table, and Kill must not error on it.
	if err := Kill(1 << 30); err != nil {
		t.Errorf("Kill() on a dead pid error = %v, want nil", err)
	}
}
