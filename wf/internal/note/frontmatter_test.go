package note

import (
	"strings"
	"testing"
)

func TestGetFieldMissing(t *testing.T) {
	for _, text := range []string{
		"# Plain note\n",
		"---\nstatus: draft\n---\n# Note\n",
		"---\nkata-issue: —\n---\n",
		"---\nkata-issue:\n---\n",
	} {
		if got := GetField(text, IssueKey); got != "" {
			t.Errorf("GetField(%q) = %q, want empty", text, got)
		}
	}
}

func TestSetFieldCreatesFrontmatter(t *testing.T) {
	got := SetField("# Note\n\nbody\n", IssueKey, "01HZNQ")
	if !strings.HasPrefix(got, "---\nkata-issue: 01HZNQ\n---\n") {
		t.Errorf("SetField() did not create frontmatter:\n%s", got)
	}
	if GetField(got, IssueKey) != "01HZNQ" {
		t.Error("field is not readable after being written")
	}
	if !strings.Contains(got, "# Note") {
		t.Error("body was lost")
	}
}

func TestSetFieldPreservesOtherKeys(t *testing.T) {
	got := SetField("---\nstatus: draft\ntags: [a]\n---\n# Note\n", IssueKey, "01HZNQ")
	for _, want := range []string{"status: draft", "tags: [a]", "kata-issue: 01HZNQ", "# Note"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestSetFieldReplacesInPlace(t *testing.T) {
	once := SetField("---\nstatus: draft\n---\n# Note\n", IssueKey, "first")
	twice := SetField(once, IssueKey, "second")

	if GetField(twice, IssueKey) != "second" {
		t.Errorf("GetField() = %q, want %q", GetField(twice, IssueKey), "second")
	}
	if n := strings.Count(twice, "kata-issue:"); n != 1 {
		t.Errorf("key appears %d times, want 1:\n%s", n, twice)
	}
	if !strings.Contains(twice, "status: draft") {
		t.Error("rebinding dropped an unrelated key")
	}
}

func TestSetFieldOverwritesPlaceholder(t *testing.T) {
	// pi-tasks notes ship with an em-dash placeholder for an unbound value.
	got := SetField("---\nkata-issue: —\n---\n# Note\n", IssueKey, "01HZNQ")
	if GetField(got, IssueKey) != "01HZNQ" {
		t.Errorf("placeholder was not replaced:\n%s", got)
	}
	if strings.Contains(got, "—") {
		t.Error("placeholder survived the write")
	}
}

func TestGetFieldStripsQuotes(t *testing.T) {
	if got := GetField(`---`+"\n"+`kata-issue: "01HZNQ"`+"\n"+`---`+"\n", IssueKey); got != "01HZNQ" {
		t.Errorf("GetField() = %q, want %q", got, "01HZNQ")
	}
}

func TestCRLFFrontmatter(t *testing.T) {
	text := "---\r\nstatus: draft\r\n---\r\n# Note\r\n"
	if got := GetField(text, "status"); got != "draft" {
		t.Errorf("GetField() = %q on CRLF note, want %q", got, "draft")
	}
}
