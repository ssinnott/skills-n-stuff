// Package runner drives coding agents. pi is the only implementation.
//
// The runner deliberately learns as little as possible about pi's output:
// it captures the run's text and hands it to the outcome parser. That is
// the narrow waist doing its job — a runner that understood pi's event
// schema would have to be rewritten for every other agent, and would break
// whenever the schema moved.
package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Pi runs the pi coding agent.
type Pi struct {
	// Bin overrides the pi binary; pi is often off PATH under a service.
	Bin string
	// SessionRoot is where wf-owned session files are written.
	SessionRoot string
	// ProfileDir becomes PI_CODING_AGENT_DIR, selecting the worker's
	// package and skill set.
	ProfileDir string
	// ExtraArgs are appended before the prompt, for model or tool flags.
	ExtraArgs []string
}

var _ wf.Runner = (*Pi)(nil)

func (p *Pi) Name() string { return "pi" }

func (p *Pi) bin() string {
	if p.Bin != "" {
		return p.Bin
	}
	return "pi"
}

// sessionRoot is wf-owned rather than pi's own sessions directory.
//
// wf mints the session path and passes it to `--session`, instead of
// letting pi choose one and then parsing it back out of the output. That is
// what makes the binding writable at spawn — we know the path before the
// agent has produced a byte. The cost is that these sessions do not appear
// in pi's own `-r` browser, which is an acceptable trade because `wf attach`
// is the intended way in, and because a wf-owned path survives the
// worktree being disposed.
func (p *Pi) sessionRoot() (string, error) {
	if p.SessionRoot != "" {
		return p.SessionRoot, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".wf", "sessions"), nil
}

// NewSessionPath mints a session file path for a task. The short id keeps
// it identifiable on disk; the suffix keeps retries from colliding.
func NewSessionPath(root, taskRef string, now time.Time) (id, path string) {
	var b [4]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])

	ref := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, taskRef)

	id = fmt.Sprintf("%s-%s-%s", ref, now.UTC().Format("20060102T150405"), suffix)
	return id, filepath.Join(root, id+".jsonl")
}

type piRun struct {
	sessionID   string
	sessionPath string
	cwd         string

	cmd    *exec.Cmd
	output *bytes.Buffer

	mu   sync.Mutex
	done bool
}

func (r *piRun) SessionID() string   { return r.sessionID }
func (r *piRun) SessionPath() string { return r.sessionPath }
func (r *piRun) Cwd() string         { return r.cwd }

func (r *piRun) Wait(ctx context.Context) (wf.RunResult, error) {
	err := r.cmd.Wait()

	r.mu.Lock()
	r.done = true
	r.mu.Unlock()

	result := wf.RunResult{TranscriptTail: r.output.String()}

	if err != nil {
		var exitErr *exec.ExitError
		if e, ok := err.(*exec.ExitError); ok {
			exitErr = e
			result.ExitCode = exitErr.ExitCode()
			// A non-zero exit is not an error to the caller: the agent may
			// still have reported outcomes worth applying, and a run that
			// reported nothing escalates rather than failing the loop.
			return result, nil
		}
		return result, fmt.Errorf("pi run: %w", err)
	}

	result.OK = true
	return result, nil
}

func (r *piRun) Abort() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done || r.cmd.Process == nil {
		return nil
	}
	return r.cmd.Process.Kill()
}

// Start spawns pi against a pre-minted session file and returns as soon as
// the process is running, so the caller can record the binding before
// waiting on it.
func (p *Pi) Start(ctx context.Context, opts wf.RunOptions) (wf.RunHandle, error) {
	root, err := p.sessionRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create session root %s: %w", root, err)
	}

	ref := opts.TaskRef
	if ref == "" {
		ref = "run"
	}
	id, path := NewSessionPath(root, ref, time.Now())
	args := p.BuildArgs(path, opts.Prompt)

	cmd := exec.CommandContext(ctx, p.bin(), args...)
	cmd.Dir = opts.Cwd

	profile := opts.ProfileDir
	if profile == "" {
		profile = p.ProfileDir
	}
	cmd.Env = os.Environ()
	if profile != "" {
		cmd.Env = append(cmd.Env, "PI_CODING_AGENT_DIR="+profile)
	}

	// stdout and stderr both feed the transcript: an agent that failed
	// usually said why on stderr, and escalation comments are more useful
	// with that text than without it.
	output := &bytes.Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start pi: %w", err)
	}

	return &piRun{sessionID: id, sessionPath: path, cwd: opts.Cwd, cmd: cmd, output: output}, nil
}

// BuildArgs assembles pi's argv.
//
// ASSUMPTION, unverified against a live pi: print mode takes the prompt as
// a positional argument, and `--session` accepts a path that does not yet
// exist. Both are taken from pi's published CLI reference
// (`-p, --print`, `--session <path|id>`). If the first live run disagrees,
// this function is the only thing to change.
func (p *Pi) BuildArgs(sessionPath, prompt string) []string {
	args := []string{"--session", sessionPath, "-p"}
	args = append(args, p.ExtraArgs...)
	return append(args, prompt)
}
