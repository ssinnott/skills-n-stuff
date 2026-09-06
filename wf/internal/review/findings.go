package review

// Seeding difit with the task's own findings.
//
// difit's --comment flag takes one JSON thread per flag, verified against
// v5.0.12:
//
//	{"type":"thread","filePath":"f.txt","position":{"side":"new","line":2},"body":"..."}
//
// The outcome protocol's ISSUE: verb carries a URL and a title, not a file
// and a line — most filed issues have nowhere to anchor a thread at all,
// and difit has no way to render one it cannot place. So only issues whose
// title itself names a location ("internal/foo.go:42 — nil check") become
// findings; the rest are skipped rather than guessed onto some line.

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Finding is one review comment difit can anchor to a line.
type Finding struct {
	FilePath string
	// Side is "old" or "new", per difit's schema.
	Side string
	Line int
	Body string
}

// anchorRe recognizes "path:line" at the start of a filed issue's title —
// the one shape that carries a location, since the protocol otherwise
// records nothing more specific than a URL and a title.
var anchorRe = regexp.MustCompile(`^(\S+):(\d+)\b\s*[-–—:]*\s*(.*)$`)

// FindingsFromIssues turns a task's issue bindings into findings, keeping
// only the ones anchored to a file and line. A binding's Label is the
// issue's title and its Ref the URL.
func FindingsFromIssues(bs wf.Bindings) []Finding {
	var out []Finding
	for _, iss := range bs.ByKind(wf.KindIssue) {
		m := anchorRe.FindStringSubmatch(strings.TrimSpace(iss.Label))
		if m == nil {
			continue
		}
		line, err := strconv.Atoi(m[2])
		if err != nil || line <= 0 {
			continue
		}
		body := strings.TrimSpace(m[3])
		if body == "" {
			body = iss.Label
		}
		if iss.Ref != "" {
			body = body + " (" + iss.Ref + ")"
		}
		out = append(out, Finding{FilePath: m[1], Side: "new", Line: line, Body: body})
	}
	return out
}

// commentThread is difit's --comment JSON shape.
type commentThread struct {
	Type     string `json:"type"`
	FilePath string `json:"filePath"`
	Position struct {
		Side string `json:"side"`
		Line int    `json:"line"`
	} `json:"position"`
	Body string `json:"body"`
}

// CommentFlags renders findings as repeatable `--comment <json>` argv
// entries, in the order given.
func CommentFlags(findings []Finding) []string {
	args := make([]string, 0, len(findings)*2)
	for _, f := range findings {
		thread := commentThread{Type: "thread", FilePath: f.FilePath, Body: f.Body}
		thread.Position.Side = f.Side
		thread.Position.Line = f.Line

		encoded, err := json.Marshal(thread)
		if err != nil {
			// A Finding built from plain strings and an int never fails to
			// marshal; this guards the shape, not any real input.
			continue
		}
		args = append(args, "--comment", string(encoded))
	}
	return args
}
