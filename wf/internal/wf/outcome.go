package wf

import (
	"regexp"
	"strings"
)

// The outcome protocol. Agents end a run with verb lines; wf turns those
// into tracker writes. These six verbs are the entire vocabulary a runner
// needs to speak and the only thing a queue backend has to know how to
// apply — which is what keeps the backend interface at seven methods.
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

// Outcome is one reported result. Which fields are meaningful depends on
// Verb; the constructors in ParseOutcomes are the only writers.
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

// Verbs must be uppercase at line start. The verb is strict because prose
// like "Next: we should…" would otherwise parse as an outcome; separators
// are tolerant because models are inconsistent about dashes.
var (
	verbRe      = regexp.MustCompile(`^(DONE|PR|ISSUE|NEXT|DOC|REPO)\b[:\s]?\s*(.*)$`)
	separatorRe = regexp.MustCompile(`\s+(?:—|–|--|-)\s+`)
	reviewRe    = regexp.MustCompile(`(?i)review\s*:\s*(\S.*)$`)
	reviewTrim  = regexp.MustCompile(`(?i)\s*[—–-]*\s*review\s*:.*$`)
	bulletRe    = regexp.MustCompile(`^[-*+]\s+`)
	quoteRe     = regexp.MustCompile(`^>\s*`)
)

// unwrap strips list bullets, blockquote marks and bold so a verb still
// matches when an agent formats its summary as a list.
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

// ParseOutcomes scans text for verb lines. It reads the whole text rather
// than only the last line: a runner that appends its own footer must not
// be able to hide the agent's report.
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
// without one escalates rather than closing — silence is not success.
func IsComplete(outcomes []Outcome) bool {
	for _, o := range outcomes {
		if o.Verb == VerbDone {
			return true
		}
	}
	return false
}

// ToCloseResult folds outcomes into the evidence a close needs. PRs are
// recorded as evidence, not treated as gates: whether a task may close
// while its PRs are open is caller policy, not the parser's.
func ToCloseResult(outcomes []Outcome) CloseResult {
	result := CloseResult{Message: "Completed by agent"}

	for _, o := range outcomes {
		switch o.Verb {
		case VerbDone:
			if o.Message != "" {
				result.Message = o.Message
			}
			if o.Path != "" {
				result.Docs = appendUnique(result.Docs, o.Path)
			}
		case VerbPR:
			result.PRs = appendUnique(result.PRs, o.URL)
		case VerbDoc:
			result.Docs = appendUnique(result.Docs, o.Path)
		}
	}

	return result
}

// Spawned splits out the outcomes that create sibling work rather than
// closing this task.
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

// appendUnique keeps evidence lists free of duplicates: a document named
// by both DONE and DOC is one artifact, not two.
func appendUnique(list []string, v string) []string {
	for _, existing := range list {
		if existing == v {
			return list
		}
	}
	return append(list, v)
}
