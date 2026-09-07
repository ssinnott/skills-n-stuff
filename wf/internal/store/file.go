package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// One JSON document per task, under <root>/tasks/<id>.json.
const tasksDir = "tasks"
const ext = ".json"

// FileStore is the JSON-file ledger. Construct it with New.
type FileStore struct {
	dir string
}

var _ Store = (*FileStore)(nil)

// DefaultRoot is the directory holding wf's config, beside review.json, sessions and worktrees.
func DefaultRoot() string { return filepath.Dir(config.DefaultPath()) }

// Root resolves the ledger root next to a loaded config; empty means DefaultRoot.
func Root(cfgPath string) string {
	if cfgPath == "" {
		return DefaultRoot()
	}
	return filepath.Dir(cfgPath)
}

// New opens the ledger under root (empty means DefaultRoot); a directory that does not exist yet is a valid, empty store.
func New(root string) *FileStore {
	if root == "" {
		root = DefaultRoot()
	}
	return &FileStore{dir: filepath.Join(root, tasksDir)}
}

// Dir is where records are written, for diagnostics and tests.
func (s *FileStore) Dir() string { return s.dir }

func (s *FileStore) path(id string) string { return filepath.Join(s.dir, id+ext) }

// validID refuses an id that could name something other than a file in the ledger directory, or the shape of a temp file (a leading dot).
func validID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("%w: empty", ErrInvalidID)
	case id == "." || id == "..":
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	case strings.HasPrefix(id, "."):
		return fmt.Errorf("%w: %q begins with a dot", ErrInvalidID, id)
	case strings.ContainsAny(id, `/\`) || strings.ContainsRune(id, os.PathSeparator):
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidID, id)
	case strings.ContainsAny(id, "\x00\n\r"):
		return fmt.Errorf("%w: %q contains a control character", ErrInvalidID, id)
	}
	return nil
}

// Locking is per id and only within this process. It protects two goroutines
// saving the same task from interleaving their temp files or racing a Save
// against a Delete, so the file that survives is one whole record.
// It protects nothing across processes: two `wf` invocations writing the same
// task share no mutex, which is acceptable because of os.Rename — a reader
// anywhere sees the old file or the new one, never a mix, so the failure mode
// is a lost update, never a corrupt ledger. Update holds this same lock
// across its whole load-mutate-write and deliberately does not use the
// lease in internal/wf for it, since `wf bind` and a running dispatch both
// write bindings and only one takes a lease. A file lock would close the
// cross-process gap; it is not here because nothing yet needs it, and an
// unused lock is a liveness bug waiting to happen.
// Keyed by absolute path so two FileStores over one root still share a lock.
var (
	locksMu sync.Mutex
	locks   = map[string]*sync.Mutex{}
)

func lockFor(path string) *sync.Mutex {
	locksMu.Lock()
	defer locksMu.Unlock()
	m, ok := locks[path]
	if !ok {
		m = &sync.Mutex{}
		locks[path] = m
	}
	return m
}

// Load reads one record; unlike List, a parse failure is a real error since there is no sibling left to salvage.
func (s *FileStore) Load(id string) (wf.Record, error) {
	if err := validID(id); err != nil {
		return wf.Record{}, err
	}
	// Deliberately unlocked: os.Rename makes a read atomic.
	return s.read(id)
}

// read is Load without the id check, for Update to reuse inside its lock.
func (s *FileStore) read(id string) (wf.Record, error) {
	path := s.path(id)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return wf.Record{}, &NotFoundError{Ref: id}
		}
		return wf.Record{}, fmt.Errorf("read task %s: %w", path, err)
	}
	var rec wf.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return wf.Record{}, fmt.Errorf("parse task %s: %w", path, err)
	}
	return rec, nil
}

// Save writes the record whole and stamps Updated.
func (s *FileStore) Save(rec wf.Record) error {
	if err := validID(rec.ID); err != nil {
		return err
	}
	mu := lockFor(s.path(rec.ID))
	mu.Lock()
	defer mu.Unlock()

	return s.write(rec)
}

// Update reads, mutates and writes one record under the per-id lock, held for the whole read-mutate-write — see the lock comment above for its guarantees.
func (s *FileStore) Update(id string, fn func(*wf.Record) error) error {
	if err := validID(id); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("update task %s: no mutation given", id)
	}

	mu := lockFor(s.path(id))
	mu.Lock()
	defer mu.Unlock()

	rec, err := s.read(id)
	if err != nil {
		var missing *NotFoundError
		if !errors.As(err, &missing) {
			return err
		}
		rec = wf.Record{ID: id}
	}

	if err := fn(&rec); err != nil {
		return err
	}
	// fn does not get to move the record to another file.
	rec.ID = id

	return s.write(rec)
}

// write encodes and lands a record; callers hold the per-id lock.
func (s *FileStore) write(rec wf.Record) error {
	rec.Updated = time.Now().UTC()

	encoded, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode task %s: %w", rec.ID, err)
	}
	encoded = append(encoded, '\n')

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", s.dir, err)
	}
	return writeAtomic(s.path(rec.ID), encoded)
}

// writeAtomic writes through a temp file in the same directory (rename is only atomic within a filesystem), so a crash never leaves a truncated record.
func writeAtomic(path string, data []byte) error {
	dir, base := filepath.Dir(path), filepath.Base(path)
	// Dot prefix keeps it out of `ls`; missing .json suffix keeps it out of List.
	tmp, err := os.CreateTemp(dir, "."+base+".tmp")
	if err != nil {
		return fmt.Errorf("create temp beside %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	// Flush, or a crash can make the rename durable while its bytes are not.
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
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}

// List returns every readable record, ordered by id. A wholly missing directory is an empty store; an unreadable file is skipped and named in a *SkipError.
func (s *FileStore) List() ([]wf.Record, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read ledger %s: %w", s.dir, err)
	}

	var (
		out     []wf.Record
		skipped []SkippedFile
	)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ext) || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(s.dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			// Vanished between listing and read: not a corruption.
			if !os.IsNotExist(err) {
				skipped = append(skipped, SkippedFile{Path: path, Err: err})
			}
			continue
		}
		var rec wf.Record
		if err := json.Unmarshal(raw, &rec); err != nil {
			skipped = append(skipped, SkippedFile{Path: path, Err: err})
			continue
		}
		out = append(out, rec)
	}

	if len(skipped) > 0 {
		return out, &SkipError{Skipped: skipped}
	}
	return out, nil
}

// Delete removes a record; an already-absent record is not an error.
func (s *FileStore) Delete(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	path := s.path(id)
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove task %s: %w", path, err)
	}
	return nil
}
