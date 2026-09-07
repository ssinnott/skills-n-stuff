package review

// Verified against difit v5.0.12: `--no-open --background` prints one line
// of JSON on success ({"port":4966,"url":"http://localhost:4966",
// "pid":3983}) and exits 0; on bad input it prints "Error: ..." with no
// JSON line, and the exit code is unreliable either way, so success is "a
// parseable JSON line landed on stdout". --port falls back silently when
// occupied, so the bound port is always read from that line.
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

// Spawner starts a difit process and returns its captured output; tests
// fake it so difit need not be installed.
type Spawner interface {
	Spawn(ctx context.Context, dir string, args []string) (stdout, stderr string)
}

// ExecSpawner runs the configured difit command as a real subprocess.
type ExecSpawner struct {
	// Command is argv0 plus any fixed leading arguments.
	Command []string
}

// Spawn runs the command and captures its output. The process's error
// return is not surfaced: ParseSpawned decides success from the text alone.
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

// Launch starts difit against targetArgs plus any seeded --comment flags.
func Launch(ctx context.Context, s Spawner, dir string, targetArgs, commentArgs []string) (Spawned, error) {
	args := make([]string, 0, len(targetArgs)+2+len(commentArgs))
	args = append(args, targetArgs...)
	args = append(args, "--no-open", "--background")
	args = append(args, commentArgs...)

	stdout, stderr := s.Spawn(ctx, dir, args)
	return ParseSpawned(stdout, stderr)
}

// ParseSpawned extracts the one JSON line difit prints on success, scanning
// every line since a wrapper (npx) can print its own banner around it.
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

// Kill stops a viewer by pid, tolerating one that is already gone.
func Kill(pid int) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	// FindProcess always succeeds on Unix; signal 0 is the liveness probe.
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
