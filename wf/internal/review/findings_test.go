package review

import (
	"encoding/json"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func TestFindingsFromIssuesKeepsOnlyAnchoredOnes(t *testing.T) {
	issues := []wf.IssueRecord{
		{URL: "https://a/i1", Title: "internal/foo.go:42 nil check on the error path"},
		{URL: "https://a/i2", Title: "Flaky test in CI"}, // no file:line — must be skipped
		{URL: "https://a/i3", Title: "cmd/wf/main.go:7 — unused import"},
	}

	got := FindingsFromIssues(issues)
	if len(got) != 2 {
		t.Fatalf("FindingsFromIssues() = %d findings, want 2 anchored ones: %+v", len(got), got)
	}

	if got[0].FilePath != "internal/foo.go" || got[0].Line != 42 || got[0].Side != "new" {
		t.Errorf("finding[0] = %+v", got[0])
	}
	if got[0].Body != "nil check on the error path (https://a/i1)" {
		t.Errorf("finding[0].Body = %q", got[0].Body)
	}

	if got[1].FilePath != "cmd/wf/main.go" || got[1].Line != 7 {
		t.Errorf("finding[1] = %+v", got[1])
	}
}

func TestFindingsFromIssuesEmptyWhenNoneAnchored(t *testing.T) {
	got := FindingsFromIssues([]wf.IssueRecord{{URL: "https://a/i1", Title: "Just a title"}})
	if len(got) != 0 {
		t.Errorf("FindingsFromIssues() = %v, want none", got)
	}
}

func TestFindingsFromIssuesNilInputIsFine(t *testing.T) {
	if got := FindingsFromIssues(nil); len(got) != 0 {
		t.Errorf("FindingsFromIssues(nil) = %v, want empty", got)
	}
}

func TestCommentFlagsMatchesDifitSchema(t *testing.T) {
	findings := []Finding{
		{FilePath: "f.txt", Side: "new", Line: 2, Body: "looks off"},
	}
	args := CommentFlags(findings)
	if len(args) != 2 || args[0] != "--comment" {
		t.Fatalf("CommentFlags() = %v, want one --comment pair", args)
	}

	var thread map[string]any
	if err := json.Unmarshal([]byte(args[1]), &thread); err != nil {
		t.Fatalf("--comment payload is not JSON: %v", err)
	}
	if thread["type"] != "thread" || thread["filePath"] != "f.txt" || thread["body"] != "looks off" {
		t.Errorf("thread = %+v", thread)
	}
	pos, ok := thread["position"].(map[string]any)
	if !ok {
		t.Fatalf("position missing or wrong shape: %+v", thread)
	}
	if pos["side"] != "new" || pos["line"].(float64) != 2 {
		t.Errorf("position = %+v", pos)
	}
}

func TestCommentFlagsPreservesOrderAndCount(t *testing.T) {
	findings := []Finding{
		{FilePath: "a.go", Side: "new", Line: 1, Body: "one"},
		{FilePath: "b.go", Side: "old", Line: 2, Body: "two"},
		{FilePath: "c.go", Side: "new", Line: 3, Body: "three"},
	}
	args := CommentFlags(findings)
	if len(args) != 6 {
		t.Fatalf("CommentFlags() = %d args, want 2 per finding", len(args))
	}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "--comment" {
			t.Errorf("args[%d] = %q, want --comment", i, args[i])
		}
	}
}

func TestCommentFlagsEmptyForNoFindings(t *testing.T) {
	if got := CommentFlags(nil); len(got) != 0 {
		t.Errorf("CommentFlags(nil) = %v, want empty", got)
	}
}
