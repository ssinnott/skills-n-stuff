package wf

import (
	"fmt"
	"strings"
	"time"
)

// Binding a pi session to a task: written at spawn, before the agent has
// produced anything, so a crashed or hung run is still attachable. The
// write lives in the supervisor (recordSession, in
// internal/supervisor/ledger.go) since a session is machine-local. This
// file only carries the shape and what a caller does with one.

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

// AttachArgs is the argv for reattaching to a session binding. The file
// path is used rather than the bare id because pi organizes sessions by
// working directory, and one task can have run in several worktrees.
func AttachArgs(session Binding) []string {
	return []string{"--session", session.Ref}
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
