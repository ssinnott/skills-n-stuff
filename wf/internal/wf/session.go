package wf

import (
	"fmt"
	"strings"
)

// A pi session is bound to a task at spawn, before the agent has produced
// anything, so a crashed or hung run is still attachable. The write itself
// lives in the supervisor, since a session is machine-local.

// SessionOutcome is how a run settled.
type SessionOutcome string

const (
	SessionDone      SessionOutcome = "done"
	SessionFailed    SessionOutcome = "failed"
	SessionEscalated SessionOutcome = "escalated"
)

// AttachArgs is the argv for reattaching to a session binding, by file path
// rather than bare id since pi organizes sessions by working directory.
func AttachArgs(session Binding) []string {
	return []string{"--session", session.Ref}
}

// SeedPreamble opens a worker's prompt: the agent may read and comment, but
// wf alone owns claim and lease transitions, and a human owns the close.
func SeedPreamble(ref, title string) string {
	return strings.Join([]string{
		fmt.Sprintf("You are working on tracker issue %s: %s", ref, title),
		"",
		"Read the issue for full context. You may comment progress on it.",
		"Do NOT close it or change its owner — a human does that once the",
		"work as a whole is finished. End your run with the outcome verbs:",
		"",
		"  DONE — review: <path>     when this run's work is finished",
		"  PR: <url> — <title>       the pull request this run opened or worked on",
		"  ISSUE: <url> — <title>    for each issue filed",
		"  DOC: <path> — <title>     for each document produced",
		"  NEXT: <task text>         to propose follow-on work",
		"  REPO: <path>              the repo you worked in",
		"",
		"DONE needs something to show: at least one PR, DOC, or ISSUE line. A",
		"DONE with nothing reported is treated as unfinished and sent to a",
		"human, so report what you actually produced.",
		"",
		"If you cannot proceed without a human decision, say so plainly and",
		"end without DONE — the run will be flagged for a human rather than",
		"marked complete.",
	}, "\n")
}
