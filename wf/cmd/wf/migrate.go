package main

// wf migrate-ledger: a one-off rewrite of pre-slim ledger files.
//
// Before stage 5, wf minted its own task id and filed the tracker row as a
// `queue` binding on the record. That record lived at
// <ledger>/<wf-id>.json. Now kata's ULID is the task's only id, so a record
// lives at <ledger>/<tracker-id>.json instead. This command does the one-time
// rewrite: for every ledger file that carries a queue binding, it renames
// the file to the queue ref and drops the binding (its Ref is now the file's
// own name). A file with no queue binding predates any tracker row at all —
// `wf task new` with nothing ever adopted — and there is nothing to rekey it
// to, so it is left alone and named as unmigratable.
//
// Deliberately decoded as a bare map rather than into wf.Record: the old
// files carry `handle` and `title` fields the current type does not have at
// all, and reading them through a type that has already dropped those
// fields would either lose information silently or fail to compile against
// them. A map survives a shape wf no longer declares.
//
// Hidden from `wf help` on purpose — see DESIGN-slim.md's stage 5. Run it
// once after upgrading, or skip it and `rm -r ~/.wf/tasks` if nothing in the
// ledger matters; either is fine with one operator.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
)

func (a *app) cmdMigrateLedger(ctx context.Context, args []string) (int, error) {
	dir := store.New(store.Root(a.cfg.Path)).Dir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		fmt.Println("no ledger directory — nothing to migrate")
		return 0, nil
	}
	if err != nil {
		return 1, fmt.Errorf("read ledger %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	migrated, unmigratable, failed := 0, 0, 0
	for _, name := range names {
		path := filepath.Join(dir, name)
		switch out, err := migrateFile(dir, path); {
		case err != nil:
			fmt.Printf("failed:        %s: %v\n", path, err)
			failed++
		case out == "":
			fmt.Printf("unmigratable:  %s (no queue binding)\n", path)
			unmigratable++
		default:
			fmt.Printf("migrated:      %s -> %s\n", path, out)
			migrated++
		}
	}

	fmt.Printf("%d migrated, %d unmigratable, %d failed\n", migrated, unmigratable, failed)
	if failed > 0 {
		return 1, nil
	}
	return 0, nil
}

// migrateFile rewrites one ledger file in place if it carries a queue
// binding, and reports the path it landed at. An empty result with a nil
// error means the file has no queue binding and was left untouched.
func migrateFile(dir, path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", err
	}

	bindings, _ := rec["bindings"].([]any)
	queueRef, kept := "", bindings[:0]
	for _, b := range bindings {
		binding, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := binding["kind"].(string); kind == "queue" && queueRef == "" {
			queueRef, _ = binding["ref"].(string)
			continue
		}
		kept = append(kept, b)
	}
	if queueRef == "" {
		return "", nil
	}

	rec["id"] = queueRef
	if len(kept) > 0 {
		rec["bindings"] = kept
	} else {
		delete(rec, "bindings")
	}
	delete(rec, "handle")
	delete(rec, "title")

	encoded, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')

	dest := filepath.Join(dir, queueRef+".json")
	if dest == path {
		return dest, os.WriteFile(dest, encoded, 0o644)
	}
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("destination %s already exists", dest)
	}
	if err := os.WriteFile(dest, encoded, 0o644); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("wrote %s but could not remove %s: %w", dest, path, err)
	}
	return dest, nil
}
