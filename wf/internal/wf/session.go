package wf

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Binding a pi session to a task.
//
// The session is the agent's working memory; the task is the durable
// artifact. Binding them means any run — finished, crashed, or still
// going — can be reopened, which is the whole point: the runs you most
// need to inspect are the ones that went wrong.
//
// So the binding is written at SPAWN, before the agent has produced
// anything. Recording it on completion would lose exactly those runs.

// Metadata keys carrying the session binding. The split between them is
// deliberate: kata's `--meta key=value` filter matches string equality, so
// the current session lives in plain string keys that stay filterable
// (`kata list --meta pi.session`), while the history is JSON under a key
// nobody filters on.
const (
	// SessionPathKey is the absolute session file path — what
	// `pi --session` resolves.
	SessionPathKey = "pi.session"
	// SessionIDKey is the session UUID, for display and pi's own browser.
	SessionIDKey = "pi.session_id"
	// SessionWorkspaceKey is the directory the session ran in. pi organizes
	// sessions by working directory, so one task may have several.
	SessionWorkspaceKey = "pi.workspace"
	// SessionHistoryKey is a JSON array of every run against this task.
	SessionHistoryKey = "pi.session_history"
)

// SessionOutcome is how a run settled.
type SessionOutcome string

const (
	SessionDone      SessionOutcome = "done"
	SessionFailed    SessionOutcome = "failed"
	SessionAborted   SessionOutcome = "aborted"
	SessionEscalated SessionOutcome = "escalated"
)

// SessionBinding ties one pi session to one task.
type SessionBinding struct {
	ID      string         `json:"id"`
	Path    string         `json:"path"`
	Cwd     string         `json:"cwd"`
	Started time.Time      `json:"started"`
	Ended   *time.Time     `json:"ended,omitempty"`
	Outcome SessionOutcome `json:"outcome,omitempty"`
}

// BindingFromMeta reads the current session binding, if the task has one.
func BindingFromMeta(meta map[string]any) (SessionBinding, bool) {
	path, _ := meta[SessionPathKey].(string)
	if path == "" {
		return SessionBinding{}, false
	}
	id, _ := meta[SessionIDKey].(string)
	cwd, _ := meta[SessionWorkspaceKey].(string)
	return SessionBinding{ID: id, Path: path, Cwd: cwd}, true
}

// HistoryFromMeta reads every recorded run. A malformed history reads as
// empty rather than failing: history is a convenience, not a lifecycle
// input.
func HistoryFromMeta(meta map[string]any) []SessionBinding {
	raw, ok := meta[SessionHistoryKey]
	if !ok || raw == nil {
		return nil
	}

	var data []byte
	switch v := raw.(type) {
	case string:
		data = []byte(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		data = b
	}

	var history []SessionBinding
	if err := json.Unmarshal(data, &history); err != nil {
		return nil
	}

	kept := history[:0]
	for _, h := range history {
		if h.Path != "" {
			kept = append(kept, h)
		}
	}
	return kept
}

// BindSession records a newly spawned session on the task. Current-session
// keys are overwritten; history is appended, so a retried task keeps every
// attempt.
func BindSession(ctx context.Context, q Queue, ref string, b SessionBinding) error {
	meta, err := q.GetMeta(ctx, ref)
	if err != nil {
		return fmt.Errorf("read metadata for %s: %w", ref, err)
	}
	history := append(HistoryFromMeta(meta), b)

	encoded, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("encode session history: %w", err)
	}

	for _, kv := range []struct{ key, value string }{
		{SessionPathKey, b.Path},
		{SessionIDKey, b.ID},
		{SessionWorkspaceKey, b.Cwd},
	} {
		if err := q.SetMeta(ctx, ref, kv.key, kv.value, SetMetaOptions{}); err != nil {
			return fmt.Errorf("bind %s on %s: %w", kv.key, ref, err)
		}
	}

	if err := q.SetMeta(ctx, ref, SessionHistoryKey, string(encoded), SetMetaOptions{JSON: true}); err != nil {
		return fmt.Errorf("bind session history on %s: %w", ref, err)
	}
	return nil
}

// FinishSession closes out the most recent history entry once a run settles.
func FinishSession(ctx context.Context, q Queue, ref string, outcome SessionOutcome, now time.Time) error {
	meta, err := q.GetMeta(ctx, ref)
	if err != nil {
		return fmt.Errorf("read metadata for %s: %w", ref, err)
	}
	history := HistoryFromMeta(meta)
	if len(history) == 0 {
		return nil
	}

	ended := now.UTC()
	history[len(history)-1].Ended = &ended
	history[len(history)-1].Outcome = outcome

	encoded, err := json.Marshal(history)
	if err != nil {
		return fmt.Errorf("encode session history: %w", err)
	}
	if err := q.SetMeta(ctx, ref, SessionHistoryKey, string(encoded), SetMetaOptions{JSON: true}); err != nil {
		return fmt.Errorf("update session history on %s: %w", ref, err)
	}
	return nil
}

// AttachArgs is the argv for reattaching to a task's session. The file path
// is used rather than the bare id because pi organizes sessions by working
// directory, and one task can have run in several worktrees.
func (b SessionBinding) AttachArgs() []string {
	return []string{"--session", b.Path}
}

// SeedPreamble opens a worker's prompt. The agent is told its ref so it can
// read context and comment progress itself — but wf keeps claim, close and
// lease transitions, so there is exactly one writer for the lifecycle.
func SeedPreamble(ref, title string) string {
	return strings.Join([]string{
		fmt.Sprintf("You are working on tracker issue %s: %s", ref, title),
		"",
		"Read the issue for full context. You may comment progress on it.",
		"Do NOT close it or change its owner — the supervisor does that from",
		"your reported outcome. End your run with the outcome verbs:",
		"",
		"  DONE — review: <path>     when the work is finished",
		"  PR: <url> — <title>       for each pull request opened",
		"  ISSUE: <url> — <title>    for each issue filed",
		"  DOC: <path> — <title>     for each document produced",
		"  NEXT: <task text>         to propose follow-on work",
		"  REPO: <path>              the repo you worked in",
		"",
		"DONE needs evidence: at least one PR, DOC, or reviewed path. A DONE",
		"with nothing to show for it is treated as unfinished and sent to a",
		"human, so report what you actually produced.",
		"",
		"If you cannot proceed without a human decision, say so plainly and",
		"end without DONE — the supervisor will escalate rather than close.",
	}, "\n")
}
