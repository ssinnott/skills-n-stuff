package review

// Spawning and reaping the difit background viewer.
//
// Empirically verified against difit v5.0.12, which no published reference
// agrees with in every detail:
//
//   - `difit <target> --no-open --background` prints exactly one line of
//     JSON to stdout and exits 0 immediately, leaving a detached server:
//     {"port":4966,"url":"http://localhost:4966","pid":3983}.
//   - On bad input (a malformed --comment, say) it prints
//     "Error: Error: Invalid --comment JSON" and prints NO JSON line.
//   - The exit code is not reliable either way, so it is never consulted:
//     "a parseable JSON line landed on stdout" is the actual success
//     signal, and its absence is the failure one, with difit's own text as
//     the diagnostic.
//   - --port picks a preferred port and falls back if occupied, so the
//     port actually bound is always read from that JSON line, never
//     assumed from what was asked for.
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Spawned is what a successful launch reports back.
type Spawned struct {
	Port int    `json:"port"`
	URL  string `json:"url"`
	PID  int    `json:"pid"`
}

// Spawner starts a difit process and returns its captured output. A real
// Spawner shells out; unit tests fake it the way internal/runner is stubbed
// in the supervisor tests, so difit need not be installed to test this
// seam.
type Spawner interface {
	Spawn(ctx context.Context, dir string, args []string) (stdout, stderr string)
}

// ExecSpawner runs the configured difit command as a real subprocess.
type ExecSpawner struct {
	// Command is argv0 plus any fixed leading arguments — "npx" "difit"
	// for the default config.difitCommand, or a single path when DIFIT_BIN
	// names one directly.
	Command []string
}

// Spawn runs the command and captures its output. The process's error
// return is deliberately not surfaced here: difit's exit code is not a
// reliable success signal (see the package comment), so ParseSpawned is
// what decides success, from the text alone.
func (s ExecSpawner) Spawn(ctx context.Context, dir string, args []string) (string, string) {
	if len(s.Command) == 0 {
		return "", "no difit command configured"
	}
	argv := append(append([]string{}, s.Command[1:]...), args...)
	cmd := exec.CommandContext(ctx, s.Command[0], argv...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	return stdout.String(), stderr.String()
}

// Launch starts difit against targetArgs (the ladder's own Target.Args)
// plus any seeded --comment flags, in dir, and returns what it reported.
func Launch(ctx context.Context, s Spawner, dir string, targetArgs, commentArgs []string) (Spawned, error) {
	args := make([]string, 0, len(targetArgs)+2+len(commentArgs))
	args = append(args, targetArgs...)
	args = append(args, "--no-open", "--background")
	args = append(args, commentArgs...)

	stdout, stderr := s.Spawn(ctx, dir, args)
	return ParseSpawned(stdout, stderr)
}

// ParseSpawned extracts the one JSON line difit prints on success. Scanning
// every line rather than assuming the first or last is deliberate: a
// wrapper (npx, a shell function) can print its own banner text around the
// line that matters.
func ParseSpawned(stdout, stderr string) (Spawned, error) {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var s Spawned
		if err := json.Unmarshal([]byte(line), &s); err == nil && s.Port != 0 && s.URL != "" {
			return s, nil
		}
	}

	detail := strings.TrimSpace(stdout)
	if e := strings.TrimSpace(stderr); e != "" {
		if detail != "" {
			detail += "\n"
		}
		detail += e
	}
	if detail == "" {
		detail = "difit produced no output"
	}
	return Spawned{}, fmt.Errorf("difit: %s", detail)
}

// Kill stops a viewer by pid, tolerating one that is already gone — a
// stale record left by a crashed process, or a `--stop` run twice, must
// never turn into an error.
func Kill(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		// Unix FindProcess never fails this way in practice, but a platform
		// that does report absence here has already told us what we need.
		return nil
	}
	// FindProcess always succeeds on Unix regardless of whether the pid is
	// live; signal 0 is the actual liveness probe, sent without effect.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return nil
	}
	if err := proc.Kill(); err != nil {
		if strings.Contains(err.Error(), "process already finished") {
			return nil
		}
		return fmt.Errorf("stop viewer (pid %d): %w", pid, err)
	}
	return nil
}
