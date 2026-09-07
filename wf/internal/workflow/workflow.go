// Package workflow holds canned workflows: named recipes that pick a pi
// profile and model, seed a prompt, and say where a task's artifacts land.
// A workflow is dispatch wiring, not agent judgment. Files are markdown with
// flat frontmatter, matching the skill and command files they sit alongside.
// See DESIGN.md.
package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// MetaKey is the task metadata naming a workflow explicitly. It wins over
// label matching.
const MetaKey = "wf.workflow"

// Workflow is one canned recipe.
type Workflow struct {
	Name        string
	Description string
	// Profile names a pi profile in config; empty runs the default.
	Profile string
	// Model names the model pi should run this workflow under, passed
	// straight through as pi's --model value; empty runs the configured
	// default.
	Model string
	// Workspace is "worktree" (default) or "none" for tasks that need no
	// checkout, such as research that only writes to the vault.
	Workspace string
	Repo      string
	Base      string
	// Labels select this workflow for tasks carrying any of them.
	Labels []string
	// Resources are files put in front of the agent by absolute path,
	// referenced rather than copied.
	Resources []string
	// BindDocs makes DOC artifacts bind back into the Obsidian vault.
	BindDocs bool
	// VaultDir is where produced documents land, relative to the vault root.
	VaultDir string
	// Prompt is the markdown body, with placeholders expanded per task.
	Prompt string
	// Path is where this workflow was loaded from.
	Path string
}

// NeedsWorkspace reports whether a run under this workflow gets a checkout.
func (w Workflow) NeedsWorkspace() bool {
	return !strings.EqualFold(w.Workspace, "none")
}

// Render expands the prompt for a task. Unknown placeholders are left
// alone: a prompt that mentions {{SOMETHING}} we do not know about is more
// useful to a human reading it than a silently blanked line.
func (w Workflow) Render(task wf.Task, workspaceDir string) string {
	replacements := []string{
		"{{TASK_REF}}", task.ShortID,
		"{{TASK_ID}}", task.ID,
		"{{TASK_TITLE}}", task.Title,
		"{{TASK_BODY}}", task.Body,
		"{{WORKSPACE}}", workspaceDir,
		"{{RESOURCES}}", strings.Join(w.Resources, "\n"),
		"{{WORKFLOW}}", w.Name,
	}

	body := strings.NewReplacer(replacements...).Replace(w.Prompt)
	return strings.TrimSpace(body)
}

// Set is a loaded collection of workflows.
type Set struct {
	byName map[string]Workflow
}

// Load reads every .md file in dir as a workflow. A missing directory is
// not an error — running with no canned workflows is a valid setup.
func Load(dir string) (*Set, error) {
	set := &Set{byName: map[string]Workflow{}}
	if dir == "" {
		return set, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return set, nil
		}
		return nil, fmt.Errorf("read workflow directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read workflow %s: %w", path, err)
		}

		w, err := Parse(string(raw))
		if err != nil {
			return nil, fmt.Errorf("parse workflow %s: %w", path, err)
		}
		if w.Name == "" {
			w.Name = strings.TrimSuffix(entry.Name(), ".md")
		}
		w.Path = path

		if existing, clash := set.byName[w.Name]; clash {
			return nil, fmt.Errorf("workflow %q defined twice: %s and %s", w.Name, existing.Path, path)
		}
		set.byName[w.Name] = w
	}

	return set, nil
}

// Names lists loaded workflows in a stable order.
func (s *Set) Names() []string {
	names := make([]string, 0, len(s.byName))
	for name := range s.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get returns a workflow by name.
func (s *Set) Get(name string) (Workflow, bool) {
	w, ok := s.byName[name]
	return w, ok
}

// All returns every workflow, ordered by name.
func (s *Set) All() []Workflow {
	out := make([]Workflow, 0, len(s.byName))
	for _, name := range s.Names() {
		out = append(out, s.byName[name])
	}
	return out
}

// Select picks the workflow for a task: explicit metadata first, then the
// first label match in name order. Returns false when nothing matches, so
// the caller can fall back to a default rather than guessing.
func (s *Set) Select(task wf.Task) (Workflow, bool) {
	if name, ok := task.Meta[MetaKey].(string); ok && name != "" {
		if w, found := s.byName[name]; found {
			return w, true
		}
		return Workflow{Name: name}, false
	}

	labels := map[string]bool{}
	for _, l := range task.Labels {
		labels[strings.ToLower(l)] = true
	}
	for _, name := range s.Names() {
		w := s.byName[name]
		for _, l := range w.Labels {
			if labels[strings.ToLower(l)] {
				return w, true
			}
		}
	}

	return Workflow{}, false
}

// Parse reads a workflow from markdown with flat frontmatter.
func Parse(text string) (Workflow, error) {
	fields, body, err := splitFrontmatter(text)
	if err != nil {
		return Workflow{}, err
	}

	w := Workflow{
		Name:        fields["name"],
		Description: fields["description"],
		Profile:     fields["profile"],
		Model:       fields["model"],
		Workspace:   fields["workspace"],
		Repo:        fields["repo"],
		Base:        fields["base"],
		Labels:      splitList(fields["labels"]),
		Resources:   splitList(fields["resources"]),
		VaultDir:    fields["vault-dir"],
		Prompt:      strings.TrimSpace(body),
		BindDocs:    isTrue(fields["bind-docs"]),
	}

	if w.Prompt == "" {
		return Workflow{}, fmt.Errorf("workflow has no prompt body")
	}
	return w, nil
}

func splitFrontmatter(text string) (map[string]string, string, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		// No frontmatter is legal: the whole file is the prompt.
		return map[string]string{}, text, nil
	}

	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter is not closed")
	}

	block := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\n")

	fields := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(trimmed, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		fields[key] = value
	}

	return fields, body, nil
}

func splitList(s string) []string {
	s = strings.Trim(strings.TrimSpace(s), "[]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.Trim(strings.TrimSpace(p), `"'`); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1", "on":
		return true
	}
	return false
}
