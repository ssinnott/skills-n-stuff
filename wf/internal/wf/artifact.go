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

// DocKey is the task-side half of the note binding: the one note a task is
// bound to. `wf bind` writes it by hand; a run writes it only when the task
// has none yet, so the first document produced becomes the task's note and
// later ones are recorded as documents (DocsKey) without displacing it.
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

	for _, o := range DocPaths(outcomes) {
		source := o
		if !filepath.IsAbs(source) && opts.WorkspaceDir != "" {
			source = filepath.Join(opts.WorkspaceDir, source)
		}
		if _, err := os.Stat(source); err != nil {
			continue
		}

		vaultPath, err := placeInVault(source, opts)
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

		bound = append(bound, BoundDoc{Source: o, VaultPath: vaultPath})
	}

	if len(bound) == 0 {
		return nil, nil
	}

	if metaString(task.Meta, DocKey) == "" {
		if err := q.SetMeta(ctx, task.ID, DocKey, bound[0].VaultPath, SetMetaOptions{}); err != nil {
			return bound, fmt.Errorf("bind note path on %s: %w", task.ShortID, err)
		}
	}
	return bound, nil
}

// placeInVault returns the document's path relative to the vault, moving it
// there if needed.
func placeInVault(source string, opts BindOptions) (string, error) {
	vault, err := filepath.Abs(opts.Vault)
	if err != nil {
		return "", fmt.Errorf("resolve vault %s: %w", opts.Vault, err)
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("resolve document %s: %w", source, err)
	}

	if rel, err := filepath.Rel(vault, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return rel, nil
	}

	destDir := filepath.Join(vault, opts.VaultDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create vault directory %s: %w", destDir, err)
	}

	dest := uniquePath(filepath.Join(destDir, filepath.Base(abs)))
	if err := moveFile(abs, dest); err != nil {
		return "", err
	}

	rel, err := filepath.Rel(vault, dest)
	if err != nil {
		return "", fmt.Errorf("resolve vault path for %s: %w", dest, err)
	}
	return rel, nil
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
