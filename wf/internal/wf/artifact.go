package wf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
)

// Binding produced documents back into an Obsidian vault: moves DOC-outcome
// files in, then writes both halves of the id pair. See DESIGN.md.

// DocKey is the task-side half of the note binding.
const DocKey = "wf.doc"

// BindOptions configures artifact binding for one run.
type BindOptions struct {
	// Vault is the Obsidian vault root. Empty disables binding entirely.
	Vault string
	// VaultDir is where produced documents land, relative to the vault.
	VaultDir string
	// WorkspaceDir resolves relative document paths.
	WorkspaceDir string
}

// BoundDoc records one artifact that made it into the vault.
type BoundDoc struct {
	// Source is where the agent wrote it.
	Source string
	// VaultPath is the path relative to the vault root.
	VaultPath string
	Moved     bool
}

// BindArtifacts moves DOC artifacts into the vault and links them to the
// task. Documents that cannot be found are skipped rather than failing the
// run.
func BindArtifacts(
	ctx context.Context,
	q Queue,
	task Task,
	outcomes []Outcome,
	opts BindOptions,
) ([]BoundDoc, error) {
	if opts.Vault == "" {
		return nil, nil
	}

	var bound []BoundDoc

	for _, o := range docPaths(outcomes) {
		source := o
		if !filepath.IsAbs(source) && opts.WorkspaceDir != "" {
			source = filepath.Join(opts.WorkspaceDir, source)
		}
		if _, err := os.Stat(source); err != nil {
			continue
		}

		vaultPath, moved, err := placeInVault(source, opts)
		if err != nil {
			return bound, err
		}

		text, err := os.ReadFile(filepath.Join(opts.Vault, vaultPath))
		if err != nil {
			return bound, fmt.Errorf("read bound note %s: %w", vaultPath, err)
		}
		updated := note.SetField(string(text), note.IssueKey, task.ID)
		if err := os.WriteFile(filepath.Join(opts.Vault, vaultPath), []byte(updated), 0o644); err != nil {
			return bound, fmt.Errorf("write bound note %s: %w", vaultPath, err)
		}

		bound = append(bound, BoundDoc{Source: o, VaultPath: vaultPath, Moved: moved})
	}

	if len(bound) == 0 {
		return nil, nil
	}

	// The task points at one note — the first produced — the rest are in the
	// run summary.
	if err := q.SetMeta(ctx, task.ID, DocKey, bound[0].VaultPath, SetMetaOptions{}); err != nil {
		return bound, fmt.Errorf("bind note path on %s: %w", task.ShortID, err)
	}
	return bound, nil
}

// placeInVault returns the document's path relative to the vault, moving it
// there if needed.
func placeInVault(source string, opts BindOptions) (string, bool, error) {
	vault, err := filepath.Abs(opts.Vault)
	if err != nil {
		return "", false, fmt.Errorf("resolve vault %s: %w", opts.Vault, err)
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", false, fmt.Errorf("resolve document %s: %w", source, err)
	}

	if rel, err := filepath.Rel(vault, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return rel, false, nil
	}

	destDir := filepath.Join(vault, opts.VaultDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", false, fmt.Errorf("create vault directory %s: %w", destDir, err)
	}

	dest := uniquePath(filepath.Join(destDir, filepath.Base(abs)))
	if err := moveFile(abs, dest); err != nil {
		return "", false, err
	}

	rel, err := filepath.Rel(vault, dest)
	if err != nil {
		return "", false, fmt.Errorf("resolve vault path for %s: %w", dest, err)
	}
	return rel, true, nil
}

// uniquePath avoids clobbering an existing note.
func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}

// moveFile renames where it can and copies across filesystems.
func moveFile(src, dest string) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove %s after copy: %w", src, err)
	}
	return nil
}

func docPaths(outcomes []Outcome) []string {
	var paths []string
	for _, o := range outcomes {
		switch o.Verb {
		case VerbDoc:
			paths = appendUnique(paths, o.Path)
		case VerbDone:
			if o.Path != "" {
				paths = appendUnique(paths, o.Path)
			}
		}
	}
	return paths
}
