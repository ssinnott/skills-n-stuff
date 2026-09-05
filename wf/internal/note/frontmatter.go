// Package note handles the Obsidian side of the task binding.
//
// The join is one id each way: the note's frontmatter carries the task's
// durable ref, and the task's metadata carries the note path. Nothing is
// mirrored — titles and status live in the tracker, prose lives in the note,
// and the only shared state is the pair. That is what keeps the binding
// cheap and free of write races.
package note

import (
	"fmt"
	"regexp"
	"strings"
)

// IssueKey is the frontmatter field naming the bound task. The durable ref
// (a ULID on kata) is stored rather than the short id: kata documents it as
// the ref that survives renames and moves between projects, which is what a
// persisted binding needs.
const IssueKey = "kata-issue"

// Deliberately not a YAML parser: wf reads and writes exactly one scalar
// key per call, and anything richer belongs to the note's author.
var frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

// GetField reads one scalar frontmatter field. The em-dash placeholder is
// treated as unset, matching the convention pi-tasks documents already use
// for an unbound session.
func GetField(text, key string) string {
	m := frontmatterRe.FindStringSubmatch(text)
	if m == nil {
		return ""
	}

	line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*(.*)$`).FindStringSubmatch(m[1])
	if line == nil {
		return ""
	}

	value := strings.TrimSpace(line[1])
	value = strings.Trim(value, `"'`)
	if value == "" || value == "—" || value == "-" {
		return ""
	}
	return value
}

// SetField writes one scalar frontmatter field, creating the frontmatter
// block if the note has none and replacing the key if it already exists.
func SetField(text, key, value string) string {
	m := frontmatterRe.FindStringSubmatchIndex(text)
	if m == nil {
		return fmt.Sprintf("---\n%s: %s\n---\n%s", key, value, text)
	}

	body := text[m[2]:m[3]]
	existing := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:.*$`)

	var updated string
	if existing.MatchString(body) {
		updated = existing.ReplaceAllString(body, key+": "+value)
	} else {
		updated = body + "\n" + key + ": " + value
	}

	return "---\n" + updated + "\n---\n" + text[m[1]:]
}
