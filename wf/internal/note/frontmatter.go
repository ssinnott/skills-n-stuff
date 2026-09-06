// Package note reads and writes the frontmatter fields that bind an
// Obsidian note to a wf task. See DESIGN.md for why the binding is a pair
// of ids rather than mirrored content.
package note

import (
	"fmt"
	"regexp"
	"strings"
)

// IssueKey is the frontmatter field naming the bound task's durable ref (kata's ULID).
const IssueKey = "kata-issue"

// TaskKey is the frontmatter field naming the bound wf task.
const TaskKey = "wf-task"

// Not a YAML parser: reads and writes exactly one scalar key per call.
var frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

// GetField reads one scalar frontmatter field. An em-dash value counts as unset.
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

// SetField writes one scalar frontmatter field, creating the block if the note has none.
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
