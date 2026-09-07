// Package runner drives coding agents; pi is the only implementation. It
// captures a run's raw text and hands it to the outcome parser rather than
// learning pi's event schema. See DESIGN.md.
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
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Pi runs the pi coding agent.
type Pi struct {
	// Bin overrides the pi binary; pi is often off PATH under a service.
	Bin string
	// SessionRoot is where wf-owned session files are written.
	SessionRoot string
}

var _ wf.Runner = (*Pi)(nil)

func (p *Pi) bin() string {
	if p.Bin != "" {
		return p.Bin
	}
	return "pi"
}

// sessionRoot is wf-owned rather than pi's own sessions directory: wf mints
// the path and passes it via `--session`, so the binding is writable at
// spawn, before the agent has produced a byte.
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
	cmd         *exec.Cmd
	output      *bytes.Buffer
}

func (r *piRun) SessionID() string   { return r.sessionID }
func (r *piRun) SessionPath() string { return r.sessionPath }

// Wait blocks until pi exits. A non-zero exit is not an error to the
// caller: the agent may still have reported outcomes worth applying, and
// one that did not will escalate for want of a DONE.
func (r *piRun) Wait(ctx context.Context) (wf.RunResult, error) {
	err := r.cmd.Wait()
	result := wf.RunResult{TranscriptTail: r.output.String()}
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return result, nil
		}
		return result, fmt.Errorf("pi run: %w", err)
	}
	return result, nil
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

	cmd := exec.CommandContext(ctx, p.bin(), BuildArgs(path, opts.Prompt, opts.Model)...)
	cmd.Dir = opts.Cwd
	cmd.Env = os.Environ()
	if opts.ProfileDir != "" {
		cmd.Env = append(cmd.Env, "PI_CODING_AGENT_DIR="+opts.ProfileDir)
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

	return &piRun{sessionID: id, sessionPath: path, cmd: cmd, output: output}, nil
}

// BuildArgs assembles pi's argv. ASSUMPTION, unverified against a live pi:
// print mode takes the prompt as a positional argument, `--session` accepts
// a path that does not yet exist, and `--model <name>` selects the model —
// per pi's published CLI reference (`-p, --print`, `--session <path|id>`,
// `--model <name>`).
func BuildArgs(sessionPath, prompt, model string) []string {
	args := []string{"--session", sessionPath, "-p"}
	if model != "" {
		args = append(args, "--model", model)
	}
	return append(args, prompt)
}
