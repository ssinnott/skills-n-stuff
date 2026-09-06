package review

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatCommentCarriesPrefixAndText(t *testing.T) {
	got := FormatComment("  paste from difit's Copy All Prompt  \n")
	if !strings.HasPrefix(got, CommentPrefix) {
		t.Errorf("FormatComment() = %q, want it to start with the prefix", got)
	}
	if !strings.Contains(got, "paste from difit's Copy All Prompt") {
		t.Errorf("FormatComment() lost the pasted text: %q", got)
	}
	if strings.Contains(got, "  \n") {
		t.Error("FormatComment() should trim the pasted text")
	}
}

// oneThreadStoreJSON is one difit store's JSON text — the value a real
// localStorage key holds — with a single thread with the given filePath.
// Verified by observation against a live difit v5.0.12 page.
func oneThreadStoreJSON(filePath string) string {
	return `{"version":2,"baseCommitish":"4f8ed43","targetCommitish":"691682d",` +
		`"createdAt":"2026-09-06T00:00:00Z","lastModifiedAt":"2026-09-06T00:00:00Z",` +
		`"threads":[{"id":"t1","filePath":"` + filePath + `","createdAt":"2026-09-06T00:00:00Z",` +
		`"updatedAt":"2026-09-06T00:00:00Z","position":{"side":"new","line":2},` +
		`"messages":[{"id":"m1","body":"needs a nil check","createdAt":"2026-09-06T00:00:00Z"}]}],` +
		`"viewedFiles":[],"appliedCommentImportIds":[]}`
}

func TestParseDifitStoreAcceptsAllFourShapes(t *testing.T) {
	key := "difit-storage-v1/265e1fe390cfecad15be464fa19536c61e7db4c68bb0b238bfd75a943e735458/4f8ed43-691682d"
	storeJSON := oneThreadStoreJSON("f.txt")

	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "map of key to JSON string — what a real harvest sends",
			raw:  `{"` + key + `":` + jsonString(storeJSON) + `}`,
		},
		{
			name: "map of key to a pre-parsed store object",
			raw:  `{"` + key + `":` + storeJSON + `}`,
		},
		{
			name: "a single bare store object",
			raw:  storeJSON,
		},
		{
			name: "a bare array of threads with no store wrapper",
			raw:  `[{"id":"t1","filePath":"f.txt","position":{"side":"new","line":2},"messages":[{"id":"m1","body":"needs a nil check"}]}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDifitStore(tt.raw)
			if err != nil {
				t.Fatalf("ParseDifitStore() error = %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("ParseDifitStore() = %d threads, want 1: %+v", len(got), got)
			}
			if got[0].FilePath != "f.txt" || got[0].Line != 2 {
				t.Errorf("thread = %+v", got[0])
			}
			if len(got[0].Messages) != 1 || got[0].Messages[0].Body != "needs a nil check" {
				t.Errorf("messages = %+v", got[0].Messages)
			}
		})
	}
}

func TestParseDifitStoreMergesMultipleKeys(t *testing.T) {
	raw := `{
		"difit-storage-v1/aaa/1-2": ` + jsonString(oneThreadStoreJSON("a.go")) + `,
		"difit-storage-v1/bbb/3-4": ` + jsonString(oneThreadStoreJSON("b.go")) + `
	}`

	got, err := ParseDifitStore(raw)
	if err != nil {
		t.Fatalf("ParseDifitStore() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParseDifitStore() = %d threads, want 2 (one per key)", len(got))
	}
	files := map[string]bool{got[0].FilePath: true, got[1].FilePath: true}
	if !files["a.go"] || !files["b.go"] {
		t.Errorf("threads = %+v, want one from each key", got)
	}
}

func TestParseDifitStoreCarriesTheCommitRange(t *testing.T) {
	got, err := ParseDifitStore(oneThreadStoreJSON("f.txt"))
	if err != nil {
		t.Fatalf("ParseDifitStore() error = %v", err)
	}
	if got[0].Range != "4f8ed43..691682d" {
		t.Errorf("Range = %q, want the store's base..target", got[0].Range)
	}
}

func TestParseDifitStoreSkipsAMalformedThreadButKeepsTheRest(t *testing.T) {
	// One good thread and one where "messages" is a string instead of an
	// array — the bad one must not cost the good one its place.
	raw := `[
		{"filePath":"good.go","position":{"side":"new","line":1},"messages":[{"body":"fine"}]},
		{"filePath":"bad.go","position":{"side":"new","line":1},"messages":"not an array"}
	]`

	got, err := ParseDifitStore(raw)
	if err != nil {
		t.Fatalf("ParseDifitStore() error = %v", err)
	}
	if len(got) != 1 || got[0].FilePath != "good.go" {
		t.Errorf("ParseDifitStore() = %+v, want only the well-formed thread", got)
	}
}

func TestParseDifitStoreSkipsAKeyWhoseValueIsAStringOfInvalidJSON(t *testing.T) {
	raw := `{
		"difit-storage-v1/aaa/1-2": "not valid json at all {{{",
		"difit-storage-v1/bbb/3-4": ` + jsonString(oneThreadStoreJSON("b.go")) + `
	}`

	got, err := ParseDifitStore(raw)
	if err != nil {
		t.Fatalf("ParseDifitStore() error = %v", err)
	}
	if len(got) != 1 || got[0].FilePath != "b.go" {
		t.Errorf("ParseDifitStore() = %+v, want only the well-formed key's thread", got)
	}
}

func TestParseDifitStoreEmptyInputIsAnError(t *testing.T) {
	if _, err := ParseDifitStore(""); err == nil {
		t.Fatal("ParseDifitStore(\"\") = nil error, want one")
	}
	if _, err := ParseDifitStore("   "); err == nil {
		t.Fatal("ParseDifitStore(whitespace) = nil error, want one")
	}
}

func TestParseDifitStoreUnrecognizedShapeIsAnError(t *testing.T) {
	if _, err := ParseDifitStore("not json at all"); err == nil {
		t.Fatal("ParseDifitStore(garbage) = nil error, want one")
	}
	if _, err := ParseDifitStore(`"just a string"`); err == nil {
		t.Fatal("ParseDifitStore(bare string) = nil error, want one")
	}
	if _, err := ParseDifitStore("42"); err == nil {
		t.Fatal("ParseDifitStore(bare number) = nil error, want one")
	}
}

func TestParseDifitStoreZeroRecoverableThreadsIsAnError(t *testing.T) {
	// Every key present but nothing in it survives — a thread with no
	// filePath, an empty messages list, and a value that is invalid JSON.
	raw := `{
		"difit-storage-v1/aaa/1-2": "garbage {{{",
		"difit-storage-v1/bbb/3-4": ` + jsonString(`{"threads":[{"filePath":"","messages":[]}]}`) + `
	}`
	_, err := ParseDifitStore(raw)
	if err == nil {
		t.Fatal("ParseDifitStore() = nil error, want one naming zero recoverable threads")
	}
	if !strings.Contains(err.Error(), "no recoverable threads") {
		t.Errorf("error = %v, want it to name what happened", err)
	}
}

func TestFormatDifitThreadsGroupsByFileAndShowsLocationAndMessages(t *testing.T) {
	threads := []DifitThread{
		{FilePath: "b.go", Line: 5, Range: "aaa..bbb", Messages: []DifitMessage{{Body: "second file", Author: "pat"}}},
		{FilePath: "a.go", Line: 10, Messages: []DifitMessage{{Body: "no author"}}},
		{FilePath: "a.go", Line: 1, Messages: []DifitMessage{{Body: "another thread on a.go", Author: "sam"}}},
	}

	got := FormatDifitThreads(threads)

	// a.go's threads come before b.go's (grouped, sorted by file).
	if strings.Index(got, "a.go") > strings.Index(got, "b.go") {
		t.Errorf("output not grouped/sorted by file: %s", got)
	}
	if !strings.Contains(got, "a.go:10") || !strings.Contains(got, "a.go:1") {
		t.Errorf("output missing path:line anchors: %s", got)
	}
	if !strings.Contains(got, "b.go:5 (aaa..bbb)") {
		t.Errorf("output missing the commit range on b.go's thread: %s", got)
	}
	if !strings.Contains(got, "sam: another thread on a.go") {
		t.Errorf("output missing an authored message: %s", got)
	}
	if !strings.Contains(got, "Unknown: no author") {
		t.Errorf("output missing the fallback author for an unauthored message: %s", got)
	}
}

func TestFormatCommentWrapsFormattedDifitThreads(t *testing.T) {
	threads := []DifitThread{{FilePath: "f.txt", Line: 2, Messages: []DifitMessage{{Body: "fix this"}}}}
	got := FormatComment(FormatDifitThreads(threads))
	if !strings.HasPrefix(got, CommentPrefix) {
		t.Errorf("FormatComment(FormatDifitThreads(...)) = %q, want the standard prefix", got)
	}
	if !strings.Contains(got, "f.txt:2") || !strings.Contains(got, "fix this") {
		t.Errorf("got = %q, want the rendered thread content", got)
	}
}

// jsonString encodes s as a JSON string literal — used to build the "map
// of key to JSON string" shape difit's harvest actually sends, where each
// value is localStorage's own string content, not a nested object.
func jsonString(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
