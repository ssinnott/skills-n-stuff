package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// The human half of review is a clipboard hop, not a pipe.
//
// difit keeps its own comments in browser localStorage and exposes no
// endpoint to read them back (verified against v5.0.12: /api/diff exists,
// /api/comments 404s), so `wf review comment` normally reads whatever a
// human pasted from difit's "Copy All Prompt" button and appends it to the
// task, prefixed so it reads as review feedback rather than something an
// agent wrote.
//
// `wf review comment <ref> --format difit` is the other way in: the
// Obsidian plugin harvests difit's own comment store straight out of the
// browser frame's localStorage and hands it to ParseDifitStore, so a
// human never has to click "Copy All Prompt" at all.

// CommentPrefix marks a queue comment as pasted human feedback rather than
// something an agent wrote.
const CommentPrefix = "**Human review feedback** (pasted from difit)"

// FormatComment prefixes pasted review text. The caller is responsible for
// rejecting empty input before calling this — silently producing a
// content-free comment would be a worse failure than refusing to post one.
func FormatComment(text string) string {
	return CommentPrefix + "\n\n" + strings.TrimSpace(text)
}

// difit's localStorage comment store, verified by observation against a
// live difit v5.0.12 page (Playwright + a localStorage dump) rather than
// from any published reference, which describes none of this:
//
//   - There is no bare `difit-storage-v1` key — localStorage.getItem of
//     that exact name returns null.
//   - The real keys are namespaced per repo and commit range reviewed on
//     that origin: `difit-storage-v1/<64-hex repo hash>/<base>-<target>`,
//     with a `__default__` segment in place of the hash when difit has no
//     repo to hash. One browser origin can hold several such keys.
//   - The value at each key is a JSON *string* (what localStorage always
//     stores) holding an object shaped like:
//     {"version":2,"baseCommitish":"4f8ed43","targetCommitish":"691682d",
//     "threads":[{"id":"…","filePath":"f.txt","position":{"side":"new",
//     "line":2},"messages":[{"id":"…","body":"…","createdAt":"…"}]}],
//     "viewedFiles":[],"appliedCommentImportIds":[]}
//   - A message's "author" field is absent in practice — difit fills in
//     "Unknown" at render time rather than storing one — so ingestion
//     must not depend on it being there.
//   - A --comment thread wf itself seeds lands back in this same store as
//     an ordinary thread once difit has rendered it, so harvesting after
//     a review round-trips wf's own seeded findings back onto the task.
//     That is not deduplicated here — see DESIGN.md for why the ingest is
//     a transcript of the review, not a set.
//
// The Obsidian plugin enumerates every `difit-storage-v1/...` key on the
// port a task's viewer is bound to and sends the result as one JSON object
// mapping each key to its (string) value — the shape ParseDifitStore
// prefers. Three more shapes are tolerated for a client that parses before
// sending, or a simpler harvest: the same map with values already parsed
// into objects, a single bare store object, or a bare array of threads
// with no store wrapper at all.

// DifitThread is one review thread harvested from difit's own comment
// store, flattened out of whichever localStorage shape it arrived in.
type DifitThread struct {
	FilePath string
	// Side is "old" or "new", per difit's schema — carried through as
	// recorded, not validated.
	Side string
	Line int
	// Range is "<baseCommitish>..<targetCommitish>" when the store it came
	// from recorded one. A harvest can mix several ranges: difit keeps one
	// store per repo+commit-range reviewed on an origin, and a task
	// re-reviewed after a push gets a second one.
	Range    string
	Messages []DifitMessage
}

// DifitMessage is one message within a DifitThread, in the order difit
// stored them.
type DifitMessage struct {
	Body string
	// Author is often empty — difit does not always record one (see the
	// package comment) — and FormatDifitThreads falls back the same way
	// difit's own UI does when rendering one.
	Author string
}

// difitStoreDoc mirrors the fields ingestion cares about from one
// localStorage value: a repo+commit-range's worth of threads plus the
// range itself. Threads are kept as raw JSON so one malformed element
// cannot abort the whole array — see the per-thread loop in
// ParseDifitStore.
type difitStoreDoc struct {
	BaseCommitish   string            `json:"baseCommitish"`
	TargetCommitish string            `json:"targetCommitish"`
	Threads         []json.RawMessage `json:"threads"`
}

// rawDifitThread mirrors the fields ingestion cares about from one thread.
// Fields it does not declare (id, createdAt, updatedAt, codeSnapshot) are
// dropped by json.Unmarshal rather than rejected — "ignore fields you do
// not recognize" per the ingest contract.
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

// ParseDifitStore parses difit's own comment store, harvested straight
// from the browser frame rather than pasted by hand. It accepts, in order
// of preference:
//
//  1. an object mapping each `difit-storage-v1/...` localStorage key to
//     its value as a JSON string — what a harvest actually sends, because
//     that is what localStorage itself holds;
//  2. the same shape but with values already parsed into store objects,
//     tolerated for a client that parses before sending;
//  3. a single bare store object (one key's value, unwrapped);
//  4. a bare array of threads, with no store wrapper at all.
//
// Every key present is merged into one result rather than picking one:
// difit can hold several commit ranges' worth of comments on one origin.
// A thread that fails to parse is skipped rather than failing the whole
// ingest; an input that is a recognized shape but yields nothing — or
// isn't one of the four shapes above at all — is an error naming what it
// received, since silently reporting zero comments as success would look
// exactly like "there was nothing to harvest" and hide a real parsing
// problem.
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
			// Shape 3: a single bare store object — a per-key map's own
			// keys look like "difit-storage-v1/<hash>/<base>-<target>",
			// never "threads", so this field's presence at the top level
			// is what tells the two shapes apart.
			var doc difitStoreDoc
			if err := json.Unmarshal([]byte(trimmed), &doc); err == nil {
				docs = append(docs, doc)
			}
		} else {
			// Shapes 1 & 2: a map keyed by difit's namespaced localStorage
			// keys. Sorted so a harvest with several ranges renders in a
			// stable order across runs.
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

// parseDifitStoreValue parses one entry of the key-to-value map: the value
// is either a JSON string holding the store's own JSON text (shape 1, what
// localStorage actually holds), or the store object already parsed (shape
// 2). A value matching neither yields nothing for that key rather than
// failing the whole map — one bad key must not cost every other key its
// threads.
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

// FormatDifitThreads renders harvested threads into one readable block,
// grouped by file so a reviewer scanning the task comment sees all
// feedback on a file together rather than in whatever order difit's
// stores happened to be enumerated. Reuse FormatComment on the result to
// get the same "pasted feedback" prefix plain-text comments carry.
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
					// difit's own UI shows "Unknown" for a message with no
					// recorded author rather than leaving it blank.
					author = "Unknown"
				}
				fmt.Fprintf(&b, "- %s: %s\n", author, m.Body)
			}
		}
	}
	return b.String()
}
