package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

const planToPR = `---
name: plan-to-pr
description: Turn an issue into a reviewed pull request
profile: coding
model: claude-opus-5
workspace: worktree
labels: plan-to-pr, feature
resources: /vault/templates/plan.md, /vault/checklists/pr.md
bind-docs: true
vault-dir: Research
---

Plan and implement {{TASK_TITLE}}.

Context:
{{TASK_BODY}}

Working in {{WORKSPACE}}. Reference material:
{{RESOURCES}}
`

func TestParse(t *testing.T) {
	w, err := Parse(planToPR)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if w.Name != "plan-to-pr" || w.Profile != "coding" || w.Model != "claude-opus-5" {
		t.Errorf("scalars not parsed: %+v", w)
	}
	if len(w.Labels) != 2 || w.Labels[0] != "plan-to-pr" {
		t.Errorf("Labels = %v", w.Labels)
	}
	if len(w.Resources) != 2 {
		t.Errorf("Resources = %v", w.Resources)
	}
	if !w.BindDocs || w.VaultDir != "Research" {
		t.Errorf("artifact binding not parsed: bind=%v dir=%q", w.BindDocs, w.VaultDir)
	}
	if !strings.Contains(w.Prompt, "{{TASK_TITLE}}") {
		t.Error("prompt body lost")
	}
	if !w.NeedsWorkspace() {
		t.Error("worktree workspace should need a checkout")
	}
}

func TestParseNoWorkspace(t *testing.T) {
	w, err := Parse("---\nname: research\nworkspace: none\n---\nResearch {{TASK_TITLE}}\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if w.NeedsWorkspace() {
		t.Error(`workspace: none must not get a checkout`)
	}
}

func TestParseWithoutFrontmatter(t *testing.T) {
	w, err := Parse("Just do {{TASK_TITLE}}\n")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if w.Prompt != "Just do {{TASK_TITLE}}" {
		t.Errorf("Prompt = %q", w.Prompt)
	}
}

func TestParseRejectsEmptyPrompt(t *testing.T) {
	if _, err := Parse("---\nname: empty\n---\n\n"); err == nil {
		t.Error("a workflow with no prompt body must not parse")
	}
}

func TestParseUnclosedFrontmatter(t *testing.T) {
	if _, err := Parse("---\nname: broken\nstill going\n"); err == nil {
		t.Error("unclosed frontmatter must be an error")
	}
}

func TestRender(t *testing.T) {
	w, err := Parse(planToPR)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	task := wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser", Body: "It should be tolerant."}
	got := w.Render(task, "/work/abc4")

	for _, want := range []string{"Add the parser", "It should be tolerant.", "/work/abc4", "/vault/templates/plan.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "{{") {
		t.Errorf("unexpanded placeholder remains:\n%s", got)
	}
}

func TestRenderLeavesUnknownPlaceholders(t *testing.T) {
	w := Workflow{Prompt: "Do {{TASK_TITLE}} with {{SOMETHING_ELSE}}"}
	got := w.Render(wf.Task{Title: "it"}, "")
	if !strings.Contains(got, "{{SOMETHING_ELSE}}") {
		t.Error("unknown placeholders should survive, not be blanked")
	}
}

func writeWorkflows(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"plan-to-pr.md": planToPR,
		"research.md":   "---\nname: research\nlabels: research\nworkspace: none\n---\nResearch {{TASK_TITLE}}\n",
		"notes.txt":     "not a workflow",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoad(t *testing.T) {
	set, err := Load(writeWorkflows(t))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := set.Names(); len(got) != 2 {
		t.Errorf("Names() = %v, want two workflows (.txt ignored)", got)
	}
	if _, ok := set.Get("plan-to-pr"); !ok {
		t.Error("plan-to-pr not loaded")
	}
}

func TestLoadMissingDirectoryIsNotAnError(t *testing.T) {
	set, err := Load(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("Load() error = %v, want nil for a missing directory", err)
	}
	if len(set.Names()) != 0 {
		t.Error("expected an empty set")
	}
}

func TestLoadNamesFromFilename(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "triage.md"), []byte("Triage {{TASK_TITLE}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, _ := Load(dir)
	if _, ok := set.Get("triage"); !ok {
		t.Errorf("workflow should take its name from the filename: %v", set.Names())
	}
}

func TestSelectByMetadata(t *testing.T) {
	set, _ := Load(writeWorkflows(t))

	task := wf.Task{Meta: map[string]any{MetaKey: "research"}, Labels: []string{"plan-to-pr"}}
	got, ok := set.Select(task)
	if !ok || got.Name != "research" {
		t.Errorf("Select() = %q ok=%v; metadata must win over labels", got.Name, ok)
	}
}

func TestSelectByLabel(t *testing.T) {
	set, _ := Load(writeWorkflows(t))

	got, ok := set.Select(wf.Task{Labels: []string{"Feature"}})
	if !ok || got.Name != "plan-to-pr" {
		t.Errorf("Select() = %q ok=%v, want a case-insensitive label match", got.Name, ok)
	}
}

func TestSelectUnknownWorkflowIsNotAFallback(t *testing.T) {
	set, _ := Load(writeWorkflows(t))

	// A task naming a workflow we do not have must not silently run under
	// a default — that would do the wrong work quietly.
	got, ok := set.Select(wf.Task{Meta: map[string]any{MetaKey: "does-not-exist"}})
	if ok {
		t.Error("Select() accepted an unknown workflow name")
	}
	if got.Name != "does-not-exist" {
		t.Errorf("Select() should report the missing name for escalation, got %q", got.Name)
	}
}

func TestSelectNoMatch(t *testing.T) {
	set, _ := Load(writeWorkflows(t))
	got, ok := set.Select(wf.Task{Labels: []string{"unrelated"}})
	if ok || got.Name != "" {
		t.Errorf("Select() = %q ok=%v, want no match", got.Name, ok)
	}
}

func TestLoadRejectsDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md"} {
		body := "---\nname: same\n---\nBody\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(dir); err == nil {
		t.Error("two workflows with one name must be an error")
	}
}
