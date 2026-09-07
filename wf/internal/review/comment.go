package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// difit exposes no endpoint to read comments back (verified against
// v5.0.12: /api/diff exists, /api/comments 404s). See DESIGN.md.

// CommentPrefix marks a queue comment as pasted human feedback.
const CommentPrefix = "**Human review feedback** (pasted from difit)"

// FormatComment prefixes pasted review text; caller rejects empty input.
func FormatComment(text string) string {
	return CommentPrefix + "\n\n" + strings.TrimSpace(text)
}

// difit's localStorage comment store, verified against a live v5.0.12
// page: there is no bare `difit-storage-v1` key — real keys are namespaced
// per repo hash and commit range (`difit-storage-v1/<hash>/<base>-<target>`,
// `__default__` for no repo), and a value is a JSON string holding
// {baseCommitish, targetCommitish, threads:[{filePath, position:{side,
// line}, messages:[{body, author}]}]}; "author" is often absent.
// ParseDifitStore also tolerates pre-parsed values, a bare store object, or
// a bare thread array. See DESIGN.md.

// DifitThread is one review thread harvested from difit's comment store.
type DifitThread struct {
	FilePath string
	// Side is "old" or "new", per difit's schema.
	Side string
	Line int
	// Range is "<baseCommitish>..<targetCommitish>" when known.
	Range    string
	Messages []DifitMessage
}

// DifitMessage is one message within a DifitThread.
type DifitMessage struct {
	Body string
	// Author is often empty; FormatDifitThreads falls back to "Unknown".
	Author string
}

// difitStoreDoc mirrors the ingested fields; threads stay raw JSON so one
// malformed element cannot abort the whole array.
type difitStoreDoc struct {
	BaseCommitish   string            `json:"baseCommitish"`
	TargetCommitish string            `json:"targetCommitish"`
	Threads         []json.RawMessage `json:"threads"`
}

// rawDifitThread mirrors the ingested fields; unrecognized ones are dropped.
type rawDifitThread struct {
	FilePath string `json:"filePath"`
	Position struct {
		Side string `json:"side"`
		Line int    `json:"line"`
	} `json:"position"`
	Messages []struct {
		Body   string `json:"body"`
		Author string `json:"author"`
	} `json:"messages"`
}

// ParseDifitStore parses difit's own comment store, merging every key
// present; an unrecognized or empty result is an error.
func ParseDifitStore(raw string) ([]DifitThread, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("difit comment store: input was empty")
	}

	var docs []difitStoreDoc
	switch trimmed[0] {
	case '[':
		// Shape 4: a bare array of threads, no store wrapper at all.
		var threads []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &threads); err != nil {
			return nil, fmt.Errorf("difit comment store: not a valid thread array: %w", err)
		}
		docs = append(docs, difitStoreDoc{Threads: threads})

	case '{':
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
			return nil, fmt.Errorf("difit comment store: not a valid object: %w", err)
		}
		if _, ok := fields["threads"]; ok {
			// Shape 3: a top-level "threads" field tells this apart.
			var doc difitStoreDoc
			if err := json.Unmarshal([]byte(trimmed), &doc); err == nil {
				docs = append(docs, doc)
			}
		} else {
			// Shapes 1 & 2, sorted for a stable order across runs.
			keys := make([]string, 0, len(fields))
			for k := range fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				docs = append(docs, parseDifitStoreValue(fields[k])...)
			}
		}

	default:
		return nil, fmt.Errorf("difit comment store: expected a JSON object or array, got: %s", difitSnippet(trimmed))
	}

	var out []DifitThread
	for _, doc := range docs {
		rangeLabel := ""
		if doc.BaseCommitish != "" || doc.TargetCommitish != "" {
			rangeLabel = doc.BaseCommitish + ".." + doc.TargetCommitish
		}
		for _, rt := range doc.Threads {
			var t rawDifitThread
			if err := json.Unmarshal(rt, &t); err != nil {
				continue // malformed thread — skip it, not the whole ingest
			}
			if t.FilePath == "" || len(t.Messages) == 0 {
				continue
			}
			thread := DifitThread{FilePath: t.FilePath, Side: t.Position.Side, Line: t.Position.Line, Range: rangeLabel}
			for _, m := range t.Messages {
				if m.Body == "" {
					continue
				}
				thread.Messages = append(thread.Messages, DifitMessage{Body: m.Body, Author: m.Author})
			}
			if len(thread.Messages) == 0 {
				continue
			}
			out = append(out, thread)
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("difit comment store: no recoverable threads in input: %s", difitSnippet(trimmed))
	}
	return out, nil
}

// parseDifitStoreValue parses one entry as a JSON string or a parsed
// store object.
func parseDifitStoreValue(v json.RawMessage) []difitStoreDoc {
	var asString string
	if err := json.Unmarshal(v, &asString); err == nil {
		var doc difitStoreDoc
		if err := json.Unmarshal([]byte(asString), &doc); err == nil {
			return []difitStoreDoc{doc}
		}
		return nil
	}
	var doc difitStoreDoc
	if err := json.Unmarshal(v, &doc); err == nil {
		return []difitStoreDoc{doc}
	}
	return nil
}

func difitSnippet(s string) string {
	const max = 120
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// FormatDifitThreads renders harvested threads into one block, by file.
func FormatDifitThreads(threads []DifitThread) string {
	byFile := map[string][]DifitThread{}
	var files []string
	for _, t := range threads {
		if _, ok := byFile[t.FilePath]; !ok {
			files = append(files, t.FilePath)
		}
		byFile[t.FilePath] = append(byFile[t.FilePath], t)
	}
	sort.Strings(files)

	var b strings.Builder
	for i, f := range files {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## %s\n", f)
		for _, t := range byFile[f] {
			loc := f
			if t.Line > 0 {
				loc = fmt.Sprintf("%s:%d", f, t.Line)
			}
			if t.Range != "" {
				fmt.Fprintf(&b, "\n%s (%s)\n", loc, t.Range)
			} else {
				fmt.Fprintf(&b, "\n%s\n", loc)
			}
			for _, m := range t.Messages {
				author := m.Author
				if author == "" {
					author = "Unknown"
				}
				fmt.Fprintf(&b, "- %s: %s\n", author, m.Body)
			}
		}
	}
	return b.String()
}
