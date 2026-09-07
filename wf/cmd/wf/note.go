package main

// wf bind: the task↔note join, both directions.
//
// A human creates a note; `wf bind` declares that it is a given task's own.
// Two writes, one per side of the pair: the note's frontmatter carries the
// task id under `kata-issue`, and the tracker's `wf.doc` metadata carries
// the note's path. Nothing else is written — rendering the task into the
// note is the plugin's job, not wf's.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// cmdBind binds a task to a note by hand: the id into the note's
// frontmatter, the note into the task's metadata. The frontmatter id is the
// half that survives a rename in Obsidian, and is what recovers the binding
// when a recorded path stops resolving.
func (a *app) cmdBind(ctx context.Context, args []string) (int, error) {
	fs, _ := newFlagSet("bind")
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return 1, err
	}
	if len(positionals) < 2 {
		return 1, errors.New("wf bind <ref> <note.md>")
	}
	ref, notePath := positionals[0], positionals[1]

	task, err := a.queue.Get(ctx, ref)
	if err != nil {
		return 1, err
	}

	abs, err := resolveNotePath(notePath, a.cfg.Vault)
	if err != nil {
		return 1, err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return 1, fmt.Errorf("read %s: %w", notePath, err)
	}

	text := string(raw)
	if existing := note.GetField(text, note.IssueKey); existing != "" && existing != task.ID {
		return 1, fmt.Errorf("%s is already bound to %s", notePath, existing)
	}

	updated := note.SetField(text, note.IssueKey, task.ID)
	if updated != text {
		// Atomic: a note is a file a human is probably looking at, and a
		// synced vault surfaces a half-written file to every device with it
		// open.
		if err := writeNoteFile(abs, updated); err != nil {
			return 1, err
		}
	}

	// wf.doc names the note relative to the vault, which is what resolves on
	// every device holding it. A note outside the vault (or no vault at all)
	// has no vault-relative form, so the path as given is the best wf can do.
	docPath := filepath.ToSlash(notePath)
	if a.cfg.Vault != "" {
		if rel, ok := relTo(a.cfg.Vault, abs); ok {
			docPath = rel
		}
	}

	// Publication to the tracker is one direction, never read back, and is
	// what keeps a task bound by hand visible to a client that only speaks
	// to the queue.
	if err := a.queue.SetMeta(ctx, task.ID, wf.DocKey, docPath, wf.SetMetaOptions{}); err != nil {
		return 1, fmt.Errorf("bind note path on %s: %w", task.ShortID, err)
	}

	fmt.Printf("bound %s ↔ %s\n", task.ShortID, docPath)
	return 0, nil
}

// resolveNotePath turns the argument to `wf bind` into an absolute path.
// An absolute argument is used as given; a relative one is resolved against
// the vault, since that is the only root wf knows for a note — and with no
// vault configured there is nothing to resolve it against.
func resolveNotePath(notePath, vault string) (string, error) {
	if filepath.IsAbs(notePath) {
		return notePath, nil
	}
	if vault == "" {
		return "", errors.New("no vault configured — set vault in the config or pass an absolute note path")
	}
	return filepath.Join(vault, notePath), nil
}

// relTo expresses abs as a path relative to root, slash-separated the way
// Obsidian writes one. false means abs is not under root at all.
func relTo(root, abs string) (string, bool) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	targetAbs, err := filepath.Abs(abs)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// writeNoteFile lands note text atomically: a temp file beside the target,
// then a rename over it. A crash or a full disk leaves the note that was
// there rather than a truncated one.
func writeNoteFile(p, text string) error {
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(p)+".tmp")
	if err != nil {
		return fmt.Errorf("create temp beside %s: %w", p, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, p, err)
	}
	return nil
}
