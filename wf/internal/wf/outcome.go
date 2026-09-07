package wf

import (
	"regexp"
	"strings"
)

// The outcome protocol: agents end a run with verb lines, wf turns those
// into tracker writes.
//
//	DONE                       DONE — review: notes/plan.md
//	PR: <url> — <title>        ISSUE: <url> — <title>
//	NEXT: <task text>          DOC: <path> — <title>
//	REPO: <path>
const (
	VerbDone  = "DONE"
	VerbPR    = "PR"
	VerbIssue = "ISSUE"
	VerbNext  = "NEXT"
	VerbDoc   = "DOC"
	VerbRepo  = "REPO"
)

// Outcome is one reported result; which fields matter depends on Verb.
type Outcome struct {
	Verb string
	// URL for PR and ISSUE.
	URL string
	// Path for DOC, REPO, and a DONE that names a review artifact.
	Path string
	// Title for PR, ISSUE and DOC; Text for NEXT; Message for DONE.
	Title   string
	Text    string
	Message string
}

// Verbs must be uppercase at line start, or prose like "Next: we should…"
// would parse as an outcome; separators stay tolerant of dash style.
var (
	verbRe      = regexp.MustCompile(`^(DONE|PR|ISSUE|NEXT|DOC|REPO)\b[:\s]?\s*(.*)$`)
	separatorRe = regexp.MustCompile(`\s+(?:—|–|--|-)\s+`)
	reviewRe    = regexp.MustCompile(`(?i)review\s*:\s*(\S.*)$`)
	reviewTrim  = regexp.MustCompile(`(?i)\s*[—–-]*\s*review\s*:.*$`)
	bulletRe    = regexp.MustCompile(`^[-*+]\s+`)
	quoteRe     = regexp.MustCompile(`^>\s*`)
)

// unwrap strips list bullets, blockquote marks and bold from a line.
func unwrap(line string) string {
	s := strings.TrimSpace(line)
	s = quoteRe.ReplaceAllString(s, "")
	s = bulletRe.ReplaceAllString(s, "")
	s = strings.TrimPrefix(s, "**")
	s = strings.TrimSuffix(s, "**")
	return strings.TrimSpace(s)
}

// split divides "value — title" on the first separator.
func split(rest string) (string, string) {
	loc := separatorRe.FindStringIndex(rest)
	if loc == nil {
		return strings.TrimSpace(rest), ""
	}
	return strings.TrimSpace(rest[:loc[0]]), strings.TrimSpace(rest[loc[1]:])
}

// ParseOutcomes scans the whole text for verb lines, not just the last line:
// a runner that appends its own footer must not hide the agent's report.
func ParseOutcomes(text string) []Outcome {
	var found []Outcome

	for _, raw := range strings.Split(text, "\n") {
		line := unwrap(strings.TrimSuffix(raw, "\r"))
		m := verbRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		verb, rest := m[1], m[2]

		switch verb {
		case VerbDone:
			out := Outcome{Verb: VerbDone}
			if rm := reviewRe.FindStringSubmatch(rest); rm != nil {
				out.Path = strings.TrimSpace(rm[1])
				out.Message = strings.TrimSpace(reviewTrim.ReplaceAllString(rest, ""))
			} else {
				out.Message = strings.TrimSpace(rest)
			}
			found = append(found, out)

		case VerbPR, VerbIssue:
			url, title := split(rest)
			if url == "" {
				continue
			}
			found = append(found, Outcome{Verb: verb, URL: url, Title: title})

		case VerbDoc:
			path, title := split(rest)
			if path == "" {
				continue
			}
			found = append(found, Outcome{Verb: VerbDoc, Path: path, Title: title})

		case VerbNext:
			if strings.TrimSpace(rest) == "" {
				continue
			}
			found = append(found, Outcome{Verb: VerbNext, Text: strings.TrimSpace(rest)})

		case VerbRepo:
			if strings.TrimSpace(rest) == "" {
				continue
			}
			found = append(found, Outcome{Verb: VerbRepo, Path: strings.TrimSpace(rest)})
		}
	}

	return found
}

// IsComplete reports whether the run declared a terminal DONE. A run
// without one escalates rather than completing — silence is not success.
func IsComplete(outcomes []Outcome) bool {
	for _, o := range outcomes {
		if o.Verb == VerbDone {
			return true
		}
	}
	return false
}

// HasOutput reports whether the run has anything to show: a pull request, a
// document, an issue it filed, or a DONE naming a review artifact. This is
// a run's bar, not the task's — an ISSUE is a run's whole output when the
// workflow was "file the issue", but never evidence that the task's work
// exists (see CloseResult).
func HasOutput(outcomes []Outcome) bool {
	for _, o := range outcomes {
		switch o.Verb {
		case VerbPR, VerbDoc, VerbIssue:
			return true
		case VerbDone:
			if o.Path != "" {
				return true
			}
		}
	}
	return false
}

// DocPaths returns the documents a run reported, in report order: DOC
// lines plus a DONE that names a review artifact.
func DocPaths(outcomes []Outcome) []string {
	var paths []string
	for _, o := range outcomes {
		switch o.Verb {
		case VerbDoc:
			paths = appendUnique(paths, o.Path)
		case VerbDone:
			if o.Path != "" {
				paths = appendUnique(paths, o.Path)
			}
		}
	}
	return paths
}

// Spawned splits out the outcomes that create sibling work.
func Spawned(outcomes []Outcome) (issues, next []Outcome) {
	for _, o := range outcomes {
		switch o.Verb {
		case VerbIssue:
			issues = append(issues, o)
		case VerbNext:
			next = append(next, o)
		}
	}
	return issues, next
}

// ReportedRepo returns the repo an agent said it worked in, if any.
func ReportedRepo(outcomes []Outcome) string {
	for _, o := range outcomes {
		if o.Verb == VerbRepo {
			return o.Path
		}
	}
	return ""
}

// appendUnique keeps evidence lists free of duplicates.
func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}
