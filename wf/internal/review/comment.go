package review

import "strings"

// The human half of review is a clipboard hop, not a pipe.
//
// difit keeps its own comments in browser localStorage and exposes no
// endpoint to read them back (verified against v5.0.12: /api/diff exists,
// /api/comments 404s), so `wf review comment` reads whatever a human
// pasted from difit's "Copy All Prompt" button and appends it to the task,
// prefixed so it reads as review feedback rather than an unexplained wall
// of text.

// CommentPrefix marks a queue comment as pasted human feedback rather than
// something an agent wrote.
const CommentPrefix = "**Human review feedback** (pasted from difit)"

// FormatComment prefixes pasted review text. The caller is responsible for
// rejecting empty input before calling this — silently producing a
// content-free comment would be a worse failure than refusing to post one.
func FormatComment(text string) string {
	return CommentPrefix + "\n\n" + strings.TrimSpace(text)
}
