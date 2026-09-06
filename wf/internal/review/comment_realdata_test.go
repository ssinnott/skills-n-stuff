package review

import (
	"encoding/json"
	"strings"
	"testing"
)

// A real payload, captured from a live difit v5.0.12 page by driving it
// with a headless browser and dumping localStorage.
//
// difit documents none of this: not the key format, not the value shape.
// The key is namespaced per repo hash and per commit range, so the obvious
// reading — one fixed "difit-storage-v1" key — returns nil, and a parser
// written against the docs would silently harvest nothing. This pins the
// observed reality the same way NormalizeIssue pins kata's, so a difit
// upgrade that moves it fails here rather than in a review that quietly
// loses a human's comments.
const realValue = `{"version":2,"baseCommitish":"4f8ed43","targetCommitish":"691682d","createdAt":"2026-09-06T12:20:36.915Z","lastModifiedAt":"2026-09-06T12:20:36.915Z","threads":[{"id":"3q31qz2on5lv50hb","filePath":"f.txt","createdAt":"2026-09-06T12:20:20.959Z","updatedAt":"2026-09-06T12:20:20.959Z","position":{"side":"new","line":2},"messages":[{"id":"3q31qz2on5lv50hb","body":"Seeded by wf: agent was unsure about this rename.","createdAt":"2026-09-06T12:20:20.959Z","updatedAt":"2026-09-06T12:20:20.959Z"}]}],"viewedFiles":[],"appliedCommentImportIds":[]}`

const realKey = "difit-storage-v1/265e1fe390cfecad15be464fa19536c61e7db4c68bb0b238bfd75a943e735458/4f8ed43-691682d"

func TestParseDifitStoreOnCapturedPayload(t *testing.T) {
	payload, err := json.Marshal(map[string]string{realKey: realValue})
	if err != nil {
		t.Fatal(err)
	}
	threads, err := ParseDifitStore(string(payload))
	if err != nil {
		t.Fatalf("ParseDifitStore on real data: %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	out := FormatDifitThreads(threads)
	t.Logf("threads=%d\n--- rendered ---\n%s", len(threads), out)
	for _, want := range []string{"f.txt", "Seeded by wf"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}
