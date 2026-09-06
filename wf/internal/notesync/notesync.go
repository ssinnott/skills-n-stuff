// Package notesync writes a task's managed block into the note that faces
// it, and keeps the two ends of that join pointing at each other.
//
// The projection itself lives in internal/note/taskblock and is a pure
// string transform over a record. This package is everything that transform
// deliberately refused to know: which file, in which vault, whether the
// bytes actually moved, and what to do when a human renamed the note in
// Obsidian. Keeping the two apart is why the rendering can be tested
// without a vault and the vault handling without caring what a block looks
// like.
//
// Three rules govern every write here, and all three come from the note
// living in a synced vault that a human, an agent and Obsidian also write
// to:
//
//   - wf owns the delimited region and nothing else. ApplyBlock replaces
//     exactly that span; this package never truncates a note, never touches
//     prose, and refuses outright a note whose frontmatter says it belongs
//     to a different task.
//   - A write that would change nothing does not happen. Re-rendering an
//     unchanged task has to be free, because the plugin calls this on every
//     open and a rewrite of identical bytes wakes every sync client the
//     vault has.
//   - The write lands atomically, temp-then-rename — the same guarantee
//     internal/store gives a ledger record, for the same reason: a crash
//     leaves the previous note or the new one, never half of one.
package notesync

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note/taskblock"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// ErrNoVault is what every operation fails with when no vault is
// configured. wf will not guess where a vault is: writing a task note into
// a directory nobody nominated is worse than doing nothing.
var ErrNoVault = errors.New("no vault configured")

// ErrNoNote reports a task with no note to sync, for a caller that did not
// ask for one to be created. The sweep uses it to tell "nothing to do here"
// apart from "this failed".
var ErrNoNote = errors.New("task has no bound note")

// DefaultDir is where a created note lands when the config names no
// directory: a folder rather than the vault root, so a vault someone
// already organizes does not acquire task notes at its top level.
const DefaultDir = "Tasks"

// Syncer renders task records into vault notes.
type Syncer struct {
	// Vault is the Obsidian vault root, absolute. Empty is ErrNoVault.
	Vault string
	// Dir is where a created note lands, relative to the vault root.
	// Empty means DefaultDir.
	Dir string
	// Store is the ledger: read for the record, written when a note is
	// created or when the one on disk turns out to have moved.
	Store store.Store

	// indexed caches the vault scan that recovers a renamed note. It is
	// built at most once per Syncer because a sweep over a ledger of a
	// hundred tasks must not walk the vault a hundred times.
	indexed bool
	byTask  map[string]string
	dupes   map[string]bool
}

// Options steers one sync.
type Options struct {
	// Create allows a task with no note to get one. It is a per-call
	// choice rather than a Syncer setting because the two entry points
	// answer it differently — see the comment on create.
	Create bool
}

// Result reports what one sync did.
type Result struct {
	// Task is the ledger id, Handle its short form when it has one.
	Task   string
	Handle string
	// Note is the note's path relative to the vault root, slash-separated
	// the way Obsidian writes one.
	Note string
	// Created says the note did not exist and this sync wrote it.
	Created bool
	// Changed says bytes were written. False is the steady state: a task
	// that has not moved re-renders to the note already on disk.
	Changed bool
	// Bound says the ledger's note binding was written or repaired — a
	// first bind, or a note the human moved and this sync found again.
	Bound bool
}

// Skip is one thing a sweep could not do, and why. A ledger file that will
// not parse and a note that will not write are the same problem to whoever
// is reading the output: name it, and carry on with the rest.
type Skip struct {
	// Ref is the ledger file or the task the failure belongs to.
	Ref string
	Err error
}

// Sweep is the outcome of syncing every task.
type Sweep struct {
	Results []Result
	// Unbound names tasks with no note. Not a failure and not a skip:
	// most tasks never want a note, and the sweep deliberately does not
	// create one.
	Unbound []string
	Skipped []Skip
}

// Sync renders one record into its note.
//
// The order — locate, record, render, write — is deliberate. Recording the
// binding before rendering means the record that renders is the record on
// disk, so a note created now and re-synced later produces the same bytes
// rather than one extra write to catch up with its own ledger entry.
func (s *Syncer) Sync(rec wf.Record, opts Options) (Result, error) {
	if err := s.ensureVault(); err != nil {
		return Result{}, err
	}
	if rec.ID == "" {
		return Result{}, errors.New("sync note: record has no id")
	}

	res := Result{Task: rec.ID, Handle: rec.Handle}

	rel, recorded, err := s.locate(rec)
	if err != nil {
		return res, err
	}
	if rel == "" {
		if !opts.Create {
			return res, ErrNoNote
		}
		if rel, err = s.create(rec); err != nil {
			return res, err
		}
		res.Created = true
	}
	res.Note = rel

	if !recorded {
		if err := s.Bind(rec.ID, rec.Handle, rel); err != nil {
			return res, err
		}
		res.Bound = true
		// Re-read so the block renders the record the ledger now holds.
		// The alternative is rendering the pre-bind copy and writing the
		// note a second time on the next sync for no reason a human
		// asked for.
		if fresh, err := s.Store.Load(rec.ID); err == nil {
			rec = fresh
		}
	}

	abs := s.abs(rel)
	existing, err := os.ReadFile(abs)
	if err != nil && !os.IsNotExist(err) {
		return res, fmt.Errorf("read note %s: %w", rel, err)
	}
	// A note that names another task is never overwritten. wf is one of
	// three writers into this file and the only one that can tell it is
	// about to write into the wrong one.
	if id := taskblock.ReadTaskID(string(existing)); id != "" && id != rec.ID {
		return res, fmt.Errorf("note %s is bound to task %s, not %s", rel, id, rec.ID)
	}

	seed := string(existing)
	if res.Created {
		// A new note gets a heading, so it reads as a note rather than as
		// frontmatter with a machine-written region under it.
		seed = "# " + title(rec) + "\n"
	}

	updated := taskblock.ApplyBlock(seed, forNote(rec, rel))
	if updated == string(existing) {
		return res, nil
	}
	if err := WriteNote(abs, updated); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

// SyncAll sweeps the ledger.
//
// store.List hands back the records it could read *and* a *SkipError naming
// the ones it could not, and honoring that shape is the whole point: one
// corrupt ledger file must cost its own task and no others. So an
// unreadable file becomes a Skip and the sweep carries on, and the same
// goes for a note that refuses to write.
func (s *Syncer) SyncAll() (Sweep, error) {
	if err := s.ensureVault(); err != nil {
		return Sweep{}, err
	}

	var sweep Sweep
	recs, err := s.Store.List()
	if err != nil {
		var skip *store.SkipError
		if !errors.As(err, &skip) {
			return sweep, err
		}
		for _, f := range skip.Skipped {
			sweep.Skipped = append(sweep.Skipped, Skip{Ref: f.Path, Err: f.Err})
		}
	}

	for _, rec := range recs {
		res, err := s.Sync(rec, Options{})
		switch {
		case errors.Is(err, ErrNoNote):
			sweep.Unbound = append(sweep.Unbound, ref(rec))
		case err != nil:
			sweep.Skipped = append(sweep.Skipped, Skip{Ref: ref(rec), Err: err})
		default:
			sweep.Results = append(sweep.Results, res)
		}
	}
	return sweep, nil
}

// Bind records a note as a task's own in the ledger, and is the one writer
// of that binding — `wf bind` and a sync that had to go looking both come
// through here, so the two cannot disagree about which binding is the note.
//
// A task has one note, so a move updates the existing binding in place
// rather than appending a second. Appending would leave the old path
// recorded and live, and the block would then render a wikilink to a note
// that no longer exists next to the one that does.
func (s *Syncer) Bind(taskID, handle, rel string) error {
	if s.Store == nil {
		return errors.New("bind note: no ledger")
	}
	at := time.Now().UTC()
	return s.Store.Update(taskID, func(rec *wf.Record) error {
		if rec.Handle == "" {
			rec.Handle = handle
		}
		if i, ok := noteIndex(rec.Bindings); ok {
			rec.Bindings[i].Ref = rel
			rec.Bindings[i].State = wf.BindingLive
			rec.Bindings[i].At = at
			return nil
		}
		rec.Bindings = append(rec.Bindings, wf.Binding{
			Kind:  wf.KindDoc,
			Ref:   rel,
			State: wf.BindingLive,
			At:    at,
			// The store is a value on the binding, never half of a key
			// name — and here it is also load-bearing, since it is what
			// separates a note that syncs across devices from a document
			// sitting in a worktree.
			Meta: map[string]string{wf.MetaStore: storeVault},
		})
		return nil
	})
}

// Rel expresses an absolute path as a vault-relative one, slash-separated.
// A path outside the vault reports false: it is a real file, it is simply
// not something this package can manage.
func (s *Syncer) Rel(p string) (string, bool) {
	if s.Vault == "" {
		return "", false
	}
	vault, err := filepath.Abs(s.Vault)
	if err != nil {
		return "", false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(vault, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// storeVault is the value MetaStore takes for a document in the vault. It
// is duplicated from internal/wf rather than exported from there because a
// value is what it is; the constant exists so the string is written once
// here.
const storeVault = "vault"

// locate answers "where is this task's note", and recorded says whether the
// ledger already agrees with the answer.
//
// The recorded path is tried first and the vault second, which is the
// rename story: a note moved in Obsidian leaves the binding pointing at
// nothing, and what recovers it is the durable half of the join — the task
// id in the note's own frontmatter, which survives every rename because
// Obsidian never rewrites frontmatter it does not own. That is why
// ReadTaskID is the reverse join and the path is not.
func (s *Syncer) locate(rec wf.Record) (rel string, recorded bool, err error) {
	if b, ok := noteBinding(rec.Bindings); ok {
		if st, err := os.Stat(s.abs(b.Ref)); err == nil && !st.IsDir() {
			return b.Ref, true, nil
		}
		// The recorded note is gone from that path. Fall through to the
		// scan rather than concluding there is no note.
	}

	found, err := s.find(rec.ID)
	if err != nil {
		return "", false, err
	}
	return found, false, nil
}

// create picks the path a new note takes, and guarantees nothing is there.
//
// Creating rather than refusing is the answer to the design's open question
// — "eagerly on task creation (the vault fills with stubs) or lazily on
// first open ... leaning lazy, with `wf note sync <ref>` as the explicit
// verb". This is that verb, so a human who typed it has asked for the note;
// refusing would leave them to create a file, invent a name, paste an id
// into its frontmatter and run the command again, none of which wf would
// learn anything from being told and all of which it can do correctly.
//
// The sweep is the other half of the same answer and passes Create false:
// `--all` refreshes the notes that exist and creates none, because a sweep
// that made one note per ledger record is the eager option wearing a flag,
// and that is exactly what fills a vault with stubs nobody asked for.
func (s *Syncer) create(rec wf.Record) (string, error) {
	dir := s.Dir
	if dir == "" {
		dir = DefaultDir
	}

	// The title, not the id: this file is opened by a human, and a vault of
	// `01M1S….md` is a vault nobody browses. Deriving the *name* from a
	// title is safe in a way deriving a *lookup* from one is not — the
	// mistake wf already made with branch names — because the name is
	// chosen once, at creation, and the join that finds the note again is
	// the frontmatter id rather than anything about the filename.
	base := fileName(title(rec))
	candidates := []string{base}
	// A queue full of "Fix the flaky test" is not hypothetical, so a
	// second name is tried before giving up. The handle disambiguates
	// because it is the thing a human would have typed to get here.
	if h := fileName(rec.Handle); h != "" && h != base {
		candidates = append(candidates, base+" ("+h+")")
	}

	for _, name := range candidates {
		rel := path.Join(dir, name+".md")
		if _, err := os.Stat(s.abs(rel)); os.IsNotExist(err) {
			return rel, nil
		}
	}
	return "", fmt.Errorf("cannot name a note for %s: %s already exists — bind one by hand with `wf bind`", ref(rec), path.Join(dir, base+".md"))
}

// find scans the vault for the note claiming this task.
func (s *Syncer) find(taskID string) (string, error) {
	if err := s.buildIndex(); err != nil {
		return "", err
	}
	if s.dupes[taskID] {
		// Two notes claiming one task is a human's copy-paste, and
		// picking one would silently make the other's edits invisible.
		return "", fmt.Errorf("more than one note claims task %s — remove the %s from the one that is not the task's note", taskID, taskblock.TaskKey)
	}
	return s.byTask[taskID], nil
}

// frontmatterProbe is how much of a note is read while scanning for the
// join. Frontmatter is the first thing in a file by definition, and a vault
// holds notes far larger than the handful of lines being looked for.
const frontmatterProbe = 8 << 10

// buildIndex walks the vault once, mapping task id to note path.
//
// An unreadable file or directory is skipped rather than failing the walk:
// the same tolerance the ledger applies to a corrupt record, and for the
// same reason — one unreadable note must not cost every other task its
// sync.
func (s *Syncer) buildIndex() error {
	if s.indexed {
		return nil
	}
	s.byTask = map[string]string{}
	s.dupes = map[string]bool{}
	s.indexed = true

	root := s.Vault
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory wf cannot read costs its notes, not the walk.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			// .obsidian holds the vault's own configuration and .trash
			// holds deleted notes; neither is a task note, and a deleted
			// one must never be resurrected as one.
			if p != root && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".md") {
			return nil
		}

		id := taskblock.ReadTaskID(head(p))
		if id == "" {
			return nil
		}
		rel, ok := s.Rel(p)
		if !ok {
			return nil
		}
		if existing, seen := s.byTask[id]; seen && existing != rel {
			s.dupes[id] = true
			return nil
		}
		s.byTask[id] = rel
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan vault %s: %w", root, err)
	}
	return nil
}

// head reads the front of a file, which is all the frontmatter can occupy.
// An unreadable note reads as empty, so it is simply not a candidate.
func head(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, frontmatterProbe)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return ""
	}
	return string(buf[:n])
}

// ensureVault refuses early rather than at the first write. A missing vault
// directory is a configuration mistake — a typo, an unmounted drive — and
// creating it would turn that into a silent second vault.
func (s *Syncer) ensureVault() error {
	if s.Vault == "" {
		return ErrNoVault
	}
	if s.Store == nil {
		return errors.New("sync note: no ledger")
	}
	st, err := os.Stat(s.Vault)
	if err != nil {
		return fmt.Errorf("vault %s: %w", s.Vault, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("vault %s is not a directory", s.Vault)
	}
	return nil
}

func (s *Syncer) abs(rel string) string {
	return filepath.Join(s.Vault, filepath.FromSlash(rel))
}

// noteBinding is the task's note: the newest live doc binding that lives in
// the vault and that no run produced.
//
// Both halves of that are load-bearing. `store: vault` because a DOC the
// agent wrote inside a worktree is a path about to be deleted, not a note.
// An empty Via because that is already what the design means by "the task's
// own" — a note bound by hand, as against evidence a run produced. A run's
// document is something the note *links to*; writing a task's whole history
// into the middle of a research document it happened to produce would be
// the wrong file every time.
// noteBinding defers to the type, so this package and the two CLI readers
// cannot drift about which document is the task's note. noteIndex stays
// because binding it in place needs the position, not the value.
func noteBinding(bs wf.Bindings) (wf.Binding, bool) { return bs.Note() }

// noteIndex is noteBinding by position, so Bind can update the binding in
// place instead of appending a rival to it.
func noteIndex(bs wf.Bindings) (int, bool) {
	best := -1
	for i, b := range bs {
		if b.Kind != wf.KindDoc || b.Via != "" || !b.IsLive() {
			continue
		}
		if b.Get(wf.MetaStore) != storeVault {
			continue
		}
		if best < 0 || !b.At.Before(bs[best].At) {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// forNote is the record as its own note should render it: everything except
// the binding naming that note.
//
// A note does not link to itself. The line would be a wikilink nobody can
// follow anywhere, and it would put a self-edge in the graph — in the one
// place the design wanted doc→task backlinks to mean something. It also
// makes the sync idempotent across the first bind: the block a fresh note
// gets is the block it keeps, rather than one that grows a line the moment
// the ledger learns where the note went.
func forNote(rec wf.Record, rel string) wf.Record {
	out := rec
	out.Bindings = make(wf.Bindings, 0, len(rec.Bindings))
	for _, b := range rec.Bindings {
		if b.Kind == wf.KindDoc && b.Via == "" && b.Ref == rel {
			continue
		}
		out.Bindings = append(out.Bindings, b)
	}
	return out
}

// title is the best name wf has for a task.
//
// The ledger deliberately does not mirror the tracker's title — the two are
// disjoint by construction, and publication never syncs back — so what is
// available is the label the queue binding recorded when the row was last
// seen. A task that was never filed falls back to its handle and then its
// id, which is honest rather than invented.
func title(rec wf.Record) string {
	if b, ok := rec.Bindings.Current(wf.KindQueue); ok && b.Label != "" {
		return b.Label
	}
	return ref(rec)
}

// ref is how a task is named in output: the handle a human would type, or
// the id when there is none.
func ref(rec wf.Record) string {
	if rec.Handle != "" {
		return rec.Handle
	}
	return rec.ID
}

// unsafeName is what a filename may not contain: the separators and the
// characters Windows and macOS reject, plus the ones Obsidian gives meaning
// to in a wikilink.
const unsafeName = `/\:*?"<>|[]#^` + "\x00"

// fileName turns a title into a note filename. Conservative on purpose — a
// name is chosen once and lived with, and a note that will not open on
// another device is worse than a plain one.
func fileName(s string) string {
	mapped := strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(unsafeName, r) {
			return ' '
		}
		return r
	}, s)
	name := strings.Join(strings.Fields(mapped), " ")
	name = strings.Trim(name, " .")
	// Cut on a rune boundary: a filename ending in half a character is not
	// a filename on every filesystem.
	if runes := []rune(name); len(runes) > maxName {
		name = strings.TrimSpace(string(runes[:maxName]))
	}
	return name
}

// maxName keeps a long title from colliding with the filesystem's own
// limit, which is 255 bytes on most and less on some.
const maxName = 80

// WriteNote lands note text atomically: a temp file beside the target, then
// a rename over it.
//
// Deliberately the same scheme as internal/store's record write, and
// deliberately a second copy of it rather than an export from there — the
// ledger's writer is private to the ledger, and a note is not a record.
// What matters is the guarantee, which is that a crash or a full disk
// leaves the note that was there rather than a truncated one. The dot
// prefix keeps the temp file out of a listing and out of the vault scan,
// and Obsidian ignores dotfiles for the same reason.
func WriteNote(p, text string) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(p)+".tmp")
	if err != nil {
		return fmt.Errorf("create temp beside %s: %w", p, err)
	}
	tmpName := tmp.Name()
	// Every early return past here leaves the temp file behind unless it
	// is removed; after a successful rename the name is gone and this is a
	// no-op.
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	// Flush before renaming: a durable rename over undurable bytes is the
	// one way this scheme could still surface a truncated note.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	// CreateTemp makes the file 0600; a note is readable like everything
	// else in the vault.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, p, err)
	}
	return nil
}
