package review

// difit's --comment flag takes one JSON thread per flag, verified against
// v5.0.12: {"type":"thread","filePath":"f.txt","position":{"side":"new",
// "line":2},"body":"..."}. Only issues whose title names a location
// ("internal/foo.go:42 — nil check") become findings; the rest are
// skipped.

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

// anchorRe recognizes "path:line" at the start of a filed issue's title.
var anchorRe = regexp.MustCompile(`^(\S+):(\d+)\b\s*[-–—:]*\s*(.*)$`)

// FindingsFromIssues turns a task's issue bindings into findings, keeping
// only the ones anchored to a file and line.
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

// CommentFlags renders findings as repeatable `--comment <json>` args.
func CommentFlags(findings []Finding) []string {
	args := make([]string, 0, len(findings)*2)
	for _, f := range findings {
		thread := commentThread{Type: "thread", FilePath: f.FilePath, Body: f.Body}
		thread.Position.Side = f.Side
		thread.Position.Line = f.Line

		encoded, err := json.Marshal(thread)
		if err != nil {
			continue
		}
		args = append(args, "--comment", string(encoded))
	}
	return args
}
