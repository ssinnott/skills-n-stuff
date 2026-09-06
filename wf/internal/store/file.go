package store

// One JSON document per task, under <root>/tasks/<id>.json.
//
// The access pattern is "load one task, all of it," which is a document read,
// and one file per task means `wf run --max 3` touches three different files
// and never contends. It stays inspectable with cat and jq, matching every
// other thing wf writes — review.json, session .jsonl, workflow markdown —
// and it holds the line the README states outright: no dependencies beyond
// the standard library. SQLite earns its place later as an index over these
// files, not as a replacement for them.

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

// tasksDir is the subdirectory under the store root, kept separate from
// review.json and config.json so a `wf gc` or a hand-cleanup can take the
// whole ledger without taking wf's settings with it.
const tasksDir = "tasks"

// ext is both the file suffix and the filter List scans with. A half-written
// temp file never carries it, so it is also what keeps a crashed writer's
// leftovers out of the listing.
const ext = ".json"

// FileStore is the JSON-file ledger. Construct it with New.
type FileStore struct {
	dir string
}

// compile-time proof the swap SQLite is deferred against is actually
// available: callers hold a Store, never this type.
var _ Store = (*FileStore)(nil)

// DefaultRoot is where the ledger lives when nothing says otherwise: the
// directory holding wf's config, the same neighborhood as review.json,
// sessions and worktrees.
func DefaultRoot() string { return filepath.Dir(config.DefaultPath()) }

// Root resolves the ledger root next to a loaded config, so `--config` moves
// the ledger with it exactly as it moves review.json. cfgPath is a Config's
// own Path field; empty means defaults.
func Root(cfgPath string) string {
	if cfgPath == "" {
		return DefaultRoot()
	}
	return filepath.Dir(cfgPath)
}

// New opens the ledger under root. An empty root means DefaultRoot. Nothing
// is created or read here: a store over a directory that does not exist yet
// is a valid, empty store, and the directory appears on the first Save.
func New(root string) *FileStore {
	if root == "" {
		root = DefaultRoot()
	}
	return &FileStore{dir: filepath.Join(root, tasksDir)}
}

// Dir is where records are written, for diagnostics and for tests that want
// to check what actually landed on disk.
func (s *FileStore) Dir() string { return s.dir }

// path is the file a record with this id occupies.
func (s *FileStore) path(id string) string { return filepath.Join(s.dir, id+ext) }

// validID refuses an id that could name something other than a file in the
// ledger directory. Identity is minted elsewhere — this is not a format
// check, only the guarantee that whatever another stage mints cannot make
// the store write outside its own directory. A leading dot is rejected too,
// since that is the shape of a temp file.
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

// Locking is per id and only within this process.
//
// What it protects: two goroutines saving the same task cannot interleave
// their temp files or race a Save against a Delete, so the file that survives
// is one whole record chosen by lock order rather than by luck.
//
// What it does not protect: anything across processes. Two `wf` invocations
// writing the same task on the same host share no mutex, and the honest
// reason that is acceptable is os.Rename — a reader on any process sees the
// old file or the new one, never a mix, so the failure mode is a lost update
// rather than a corrupt ledger. Nor does the lock make Load-then-Save atomic
// even in-process: this interface has no transaction, and a caller that needs
// read-modify-write on one task must serialize itself, which is what the
// lease in internal/wf already does for the run loop. A file lock would close
// the cross-process gap; it is not here because nothing yet needs it and an
// unused lock is a liveness bug waiting to happen.
//
// Keyed by absolute file path rather than by store instance so two FileStores
// over one root in the same process still share a lock. The map grows by one
// entry per task the process touches, which is bounded by the ledger.
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

// Load reads one record. A missing file is a *NotFoundError; a file that
// exists but will not parse is a real error, because unlike List there is no
// sibling left to salvage and answering "no such task" would be a lie.
func (s *FileStore) Load(id string) (wf.Record, error) {
	if err := validID(id); err != nil {
		return wf.Record{}, err
	}
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

// Save writes the record whole and stamps Updated. Created is left alone:
// stamping it here would quietly rewrite history for a record being updated,
// and whoever mints the id owns that field.
func (s *FileStore) Save(rec wf.Record) error {
	if err := validID(rec.ID); err != nil {
		return err
	}
	rec.Updated = time.Now().UTC()

	encoded, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encode task %s: %w", rec.ID, err)
	}
	encoded = append(encoded, '\n')

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", s.dir, err)
	}

	path := s.path(rec.ID)
	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()

	return writeAtomic(path, encoded)
}

// writeAtomic writes through a temp file in the same directory and renames
// over the target, so a crash leaves either the previous record or the new
// one and never a truncated file. Same directory because rename is only
// atomic within a filesystem, and a temp dir may be on another one.
func writeAtomic(path string, data []byte) error {
	dir, base := filepath.Dir(path), filepath.Base(path)
	// The dot prefix keeps a leftover out of a `ls`, and the missing
	// .json suffix keeps it out of List.
	tmp, err := os.CreateTemp(dir, "."+base+".tmp")
	if err != nil {
		return fmt.Errorf("create temp beside %s: %w", path, err)
	}
	tmpName := tmp.Name()
	// Every early return past this point leaves a temp file behind unless
	// it is cleaned up here; after a successful rename the name no longer
	// exists and the remove is a harmless no-op.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	// Flush before renaming: on a crash a rename can otherwise be durable
	// while the bytes it points at are not, which is the one way this
	// scheme could still surface a truncated record. The containing
	// directory is deliberately not synced — that guards the rename
	// itself, and losing the whole update is already an outcome the
	// tolerance rules cover, where half a record is not.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	// CreateTemp makes the file 0600; the ledger is readable like the rest
	// of what wf writes beside it.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}

// List returns every readable record, ordered by id. A wholly missing
// directory is an empty store rather than an error — no task has been
// recorded yet — and a file that will not read or parse is skipped and named
// in a *SkipError so one bad record never costs the rest.
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
	// ReadDir sorts by filename, and a filename is its id plus the
	// extension, so the result is already id-ordered.
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ext) || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(s.dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			// A file that vanished between the listing and the read
			// was deleted concurrently, which is not a corruption to
			// report — it is simply no longer a record.
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

// Delete removes a record. An already-absent record is not an error, for the
// same reason ClearState tolerates one: deleting twice must not fail the
// second time.
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

// Resolve finds the task a human meant. The ref forms are wf's own id, the
// short handle, and the ref of any queue binding — which is how a tracker id
// still finds the task after DESIGN-task.md moved identity off it.
//
// Matching runs in passes from strict to loose, and an earlier pass wins
// outright: an exact hit is never overruled by something a later pass also
// matched, so a task handled "neck" resolves even alongside "necklace".
// Within a pass, more than one hit is an error naming every candidate.
//
// A corrupt sibling cannot make Resolve fail — it resolves against what is
// readable — which does mean a ref whose only match is the unreadable file
// reads as not-found. That is the tolerance rule applied honestly: the bad
// file is reported by List, and a caller that wants to know uses it.
func (s *FileStore) Resolve(ref string) (wf.Record, error) {
	if ref == "" {
		return wf.Record{}, &NotFoundError{Ref: ref}
	}
	recs, err := s.List()
	if err != nil {
		var skip *SkipError
		if !errors.As(err, &skip) {
			return wf.Record{}, err
		}
	}

	for _, match := range []func(wf.Record, string) bool{matchExact, matchFold, matchPartial} {
		var hits []wf.Record
		for _, rec := range recs {
			if match(rec, ref) {
				hits = append(hits, rec)
			}
		}
		switch len(hits) {
		case 0:
			continue
		case 1:
			return hits[0], nil
		default:
			return wf.Record{}, &AmbiguousError{Ref: ref, Candidates: describe(hits)}
		}
	}
	return wf.Record{}, &NotFoundError{Ref: ref}
}

// matchExact is the ref as typed against the three ref forms.
func matchExact(rec wf.Record, ref string) bool {
	if rec.ID == ref || (rec.Handle != "" && rec.Handle == ref) {
		return true
	}
	for _, b := range rec.Bindings.ByKind(wf.KindQueue) {
		if b.Ref == ref {
			return true
		}
	}
	return false
}

// matchFold is the same, ignoring case. Tracker ids are ULIDs, which humans
// copy in whichever case the surface they copied from displayed.
func matchFold(rec wf.Record, ref string) bool {
	if strings.EqualFold(rec.ID, ref) || (rec.Handle != "" && strings.EqualFold(rec.Handle, ref)) {
		return true
	}
	for _, b := range rec.Bindings.ByKind(wf.KindQueue) {
		if strings.EqualFold(b.Ref, ref) {
			return true
		}
	}
	return false
}

// minPartial keeps a single stray character from resolving a lone task. Two
// is short enough for every abbreviation anyone actually types and long
// enough that a fat-fingered ref misses rather than hits.
const minPartial = 2

// matchPartial is the abbreviation pass: a prefix of an id or handle, or
// either end of a tracker ref. The tail matters because a tracker's short id
// is not always a prefix of its long one — kata's is the ULID's last four
// characters — so a prefix-only rule would fail the most common thing a human
// has in front of them.
func matchPartial(rec wf.Record, ref string) bool {
	if len(ref) < minPartial {
		return false
	}
	ref = strings.ToLower(ref)
	if strings.HasPrefix(strings.ToLower(rec.ID), ref) {
		return true
	}
	if rec.Handle != "" && strings.HasPrefix(strings.ToLower(rec.Handle), ref) {
		return true
	}
	for _, b := range rec.Bindings.ByKind(wf.KindQueue) {
		bref := strings.ToLower(b.Ref)
		if strings.HasPrefix(bref, ref) || strings.HasSuffix(bref, ref) {
			return true
		}
	}
	return false
}

// describe renders candidates for an ambiguity message: the id a caller would
// have to pass to disambiguate, plus the handle that makes it recognizable.
func describe(recs []wf.Record) []string {
	out := make([]string, 0, len(recs))
	for _, rec := range recs {
		if rec.Handle != "" {
			out = append(out, fmt.Sprintf("%s (%s)", rec.ID, rec.Handle))
			continue
		}
		out = append(out, rec.ID)
	}
	return out
}
