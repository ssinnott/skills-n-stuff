package wf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
)

// Binding produced documents back into an Obsidian vault.
//
// A workflow that produces prose declares where it lands; wf moves the file
// into the vault if it was written somewhere disposable, then writes both
// halves of the id pair — `kata-issue` in the note's frontmatter and
// `wf.doc` in the task's metadata. Nothing else is mirrored, so a bound
// note and its task can diverge in content without ever conflicting.

// DocKey is the task-side half of the note binding. The key names the
// role — a produced document — while *which* store holds it is a value on
// the binding (MetaStore), so a second prose layer is a new value rather
// than a second key with its own reader.
const DocKey = "wf.doc"

// BindOptions configures artifact binding for one run.
type BindOptions struct {
	// Vault is the Obsidian vault root. Empty disables binding entirely.
	Vault string
	// VaultDir is where produced documents land, relative to the vault.
	VaultDir string
	// WorkspaceDir is where the agent ran, used to resolve relative paths.
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
// run: an agent that named a file it did not write should not cost you the
// close, and the missing path stays visible in the run summary.
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

	// The task points at one note — the first produced — while the rest are
	// recorded in the run summary. A task with a single wf.doc stays
	// queryable; a list would not.
	if err := q.SetMeta(ctx, task.ID, DocKey, bound[0].VaultPath, SetMetaOptions{}); err != nil {
		return bound, fmt.Errorf("bind note path on %s: %w", task.ShortID, err)
	}
	return bound, nil
}

// placeInVault returns the document's path relative to the vault, moving it
// there when the agent wrote it somewhere disposable like a worktree.
func placeInVault(source string, opts BindOptions) (string, bool, error) {
	vault, err := filepath.Abs(opts.Vault)
	if err != nil {
		return "", false, fmt.Errorf("resolve vault %s: %w", opts.Vault, err)
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "", false, fmt.Errorf("resolve document %s: %w", source, err)
	}

	// Already in the vault: bind it where it lies.
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

// uniquePath avoids clobbering an existing note: two runs producing
// "plan.md" must not silently overwrite each other.
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

// moveFile renames where it can and copies across filesystems, which is the
// common case when worktrees and the vault live on different mounts.
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
