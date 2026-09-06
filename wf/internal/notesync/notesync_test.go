package notesync

// Every test here runs against a real vault directory and a real ledger
// under t.TempDir(). The things worth proving — that a second sync writes
// nothing, that a human's prose survives, that a renamed note is found
// again — are all statements about files, and a fake filesystem would let
// each of them pass while the real one failed.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note/taskblock"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// fixture is a vault and a ledger, both real, both empty.
func fixture(t *testing.T) (*Syncer, *store.FileStore, string) {
	t.Helper()
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	if err := os.MkdirAll(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(root, "wf"))
	return &Syncer{Vault: vault, Store: st}, st, vault
}

// record is a task with one run and one PR, which is enough for the block
// to have something to say.
func record(id, handle, title string) wf.Record {
	at := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	ended := at.Add(20 * time.Minute)
	return wf.Record{
		ID:      id,
		Handle:  handle,
		Created: at,
		Updated: ended,
		Runs: []wf.Run{{
			ID: "r1", Workflow: "plan-to-pr", Model: "sonnet-5",
			Started: at, Ended: &ended, Outcome: wf.SessionDone,
		}},
		Bindings: wf.Bindings{
			{Kind: wf.KindQueue, Ref: id, Label: title, State: wf.BindingLive, At: at,
				Meta: map[string]string{wf.MetaBackend: "kata", wf.MetaShortID: handle}},
			{Kind: wf.KindPR, Ref: "https://github.com/me/app/pull/412", State: wf.BindingLive, At: at, Via: "r1"},
		},
	}
}

func save(t *testing.T, st store.Store, rec wf.Record) wf.Record {
	t.Helper()
	if err := st.Save(rec); err != nil {
		t.Fatal(err)
	}
	saved, err := st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A task with no note gets one, named for its title, carrying the join and
// the block. This is the decision the design left open, so it is the one
// worth pinning down in a test.
func TestSyncCreatesNoteForUnboundTask(t *testing.T) {
	s, st, vault := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	res, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !res.Created || !res.Changed || !res.Bound {
		t.Fatalf("Result = %+v, want created, changed and bound", res)
	}
	if res.Note != "Tasks/Add the parser.md" {
		t.Errorf("Note = %q, want it named for the title under the default dir", res.Note)
	}

	text := read(t, filepath.Join(vault, filepath.FromSlash(res.Note)))
	if got := taskblock.ReadTaskID(text); got != rec.ID {
		t.Errorf("frontmatter task = %q, want %q", got, rec.ID)
	}
	if !strings.Contains(text, "# Add the parser") {
		t.Errorf("a created note should carry its title as a heading:\n%s", text)
	}
	if !strings.Contains(text, taskblock.BeginMarker) || !strings.Contains(text, taskblock.EndMarker) {
		t.Errorf("no managed block in:\n%s", text)
	}
	if !strings.Contains(text, "#412") {
		t.Errorf("the run's PR should be in the block:\n%s", text)
	}

	// The ledger now knows where the note is, and says it lives in the
	// vault — which is what makes it a task note rather than a document.
	after, err := st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := noteBinding(after.Bindings)
	if !ok {
		t.Fatal("no note binding recorded")
	}
	if b.Ref != res.Note || b.Get(wf.MetaStore) != storeVault || b.Via != "" {
		t.Errorf("note binding = %+v, want the vault-relative path with no run", b)
	}
}

// The steady state: syncing an unchanged task must produce the same bytes
// and must not touch the file at all. The note lives in a synced vault, so
// a rewrite of identical bytes is a real cost paid by every device.
func TestSyncTwiceIsByteIdenticalAndDoesNotRewrite(t *testing.T) {
	s, st, vault := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	first, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatalf("first Sync() error = %v", err)
	}
	path := filepath.Join(vault, filepath.FromSlash(first.Note))
	before := read(t, path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A rename lands a new inode with a new mtime, so an untouched file is
	// observable rather than inferred.
	firstMod := info.ModTime()
	time.Sleep(10 * time.Millisecond)

	// A fresh Syncer, and the record as the ledger now holds it: this is
	// what the plugin calling sync on every open actually does.
	reloaded, err := st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (&Syncer{Vault: s.Vault, Store: st}).Sync(reloaded, Options{Create: true})
	if err != nil {
		t.Fatalf("second Sync() error = %v", err)
	}

	if second.Changed || second.Created || second.Bound {
		t.Errorf("second Result = %+v, want nothing to have happened", second)
	}
	if after := read(t, path); after != before {
		t.Errorf("note changed on re-sync:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(firstMod) {
		t.Errorf("note was rewritten with identical content (mtime %v → %v)", firstMod, info.ModTime())
	}
}

// Everything outside the delimiters belongs to the human, including prose
// added after the block was first written.
func TestSyncPreservesHumanWriting(t *testing.T) {
	s, st, vault := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	first, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, filepath.FromSlash(first.Note))

	edited := read(t, path) + "\n## My own notes\n\nThe parser is the easy half.\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second PR moves the record, so the block genuinely has to be
	// rewritten rather than skipped.
	rec, err = st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec.Bindings = rec.Bindings.Upsert(wf.Binding{
		Kind: wf.KindPR, Ref: "https://github.com/me/app/pull/500",
		State: wf.BindingLive, At: time.Now().UTC(), Via: "r1",
	})
	rec = save(t, st, rec)

	res, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !res.Changed {
		t.Fatal("Changed = false, want the new PR to have been written")
	}

	text := read(t, path)
	if !strings.Contains(text, "## My own notes") || !strings.Contains(text, "The parser is the easy half.") {
		t.Errorf("human writing was lost:\n%s", text)
	}
	if !strings.Contains(text, "#500") {
		t.Errorf("the new PR is missing:\n%s", text)
	}
	if strings.Count(text, taskblock.BeginMarker) != 1 {
		t.Errorf("want exactly one managed block:\n%s", text)
	}
}

// Renaming a note in Obsidian moves the file and leaves the frontmatter
// alone. The binding has to follow it, because the alternative is a second
// note created beside the one the human is looking at.
func TestSyncFollowsARenamedNote(t *testing.T) {
	s, st, vault := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	first, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatal(err)
	}

	old := filepath.Join(vault, filepath.FromSlash(first.Note))
	moved := filepath.Join(vault, "Projects", "Parser work.md")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, moved); err != nil {
		t.Fatal(err)
	}

	reloaded, err := st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&Syncer{Vault: vault, Store: st}).Sync(reloaded, Options{Create: true})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if res.Created {
		t.Fatal("a renamed note was orphaned: sync created a second one")
	}
	if res.Note != "Projects/Parser work.md" {
		t.Errorf("Note = %q, want the note where the human put it", res.Note)
	}
	if !res.Bound {
		t.Error("Bound = false, want the binding repaired")
	}

	after, err := st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	docs := after.Bindings.ByKind(wf.KindDoc)
	if len(docs) != 1 || docs[0].Ref != "Projects/Parser work.md" {
		t.Errorf("doc bindings = %+v, want the one binding moved rather than a second added", docs)
	}
	if _, err := os.Stat(filepath.Join(vault, "Tasks", "Add the parser.md")); !os.IsNotExist(err) {
		t.Error("a duplicate note was created at the old path")
	}
}

// The block goes into the task's note, never into a document a run
// produced: that document is evidence the note links to.
func TestSyncIgnoresProducedDocuments(t *testing.T) {
	s, st, vault := fixture(t)
	rec := record("01TASK", "neck", "Add the parser")
	rec.Bindings = rec.Bindings.Upsert(wf.Binding{
		Kind: wf.KindDoc, Ref: "Research/parser-plan.md", Label: "Parser plan",
		State: wf.BindingLive, At: rec.Created, Via: "r1",
		Meta: map[string]string{wf.MetaStore: storeVault},
	})
	rec = save(t, st, rec)

	if err := os.MkdirAll(filepath.Join(vault, "Research"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(vault, "Research", "parser-plan.md")
	if err := os.WriteFile(plan, []byte("# Parser plan\n\nagent prose\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if res.Note == "Research/parser-plan.md" {
		t.Fatal("the block was written into a produced document")
	}
	if body := read(t, plan); strings.Contains(body, taskblock.BeginMarker) {
		t.Errorf("produced document was rewritten:\n%s", body)
	}
	// It is still linked from the task note, which is the whole point of
	// rendering documents as wikilinks.
	if text := read(t, filepath.Join(vault, filepath.FromSlash(res.Note))); !strings.Contains(text, "[[Research/parser-plan|Parser plan]]") {
		t.Errorf("produced document is not linked from the task note:\n%s", text)
	}
}

// The task note does not link to itself: a self-wikilink follows nowhere
// and puts a self-edge in the graph the backlinks were meant to serve.
func TestSyncDoesNotLinkTheNoteToItself(t *testing.T) {
	s, st, _ := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	res, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Sync(reloaded, Options{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Error("the note's own binding leaked into its block")
	}
	if strings.Contains(read(t, filepath.Join(s.Vault, filepath.FromSlash(res.Note))), "[[Tasks/Add the parser]]") {
		t.Error("the note links to itself")
	}
}

// A note claiming another task is never written, even when the ledger says
// it is this task's. wf is one of three writers into a vault and the only
// one that can tell it is about to write into the wrong file.
func TestSyncRefusesANoteBoundElsewhere(t *testing.T) {
	s, st, vault := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	first, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vault, filepath.FromSlash(first.Note))

	// Someone repointed the note at another task, leaving the binding
	// behind.
	body := "---\nwf-task: 01OTHER\n---\n# Someone else's note\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	reloaded, err := st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync(reloaded, Options{Create: true}); err == nil {
		t.Fatal("Sync() = nil error, want a refusal")
	}
	if got := read(t, path); got != body {
		t.Errorf("the other task's note was written:\n%s", got)
	}
}

// A name already taken by another task's note is stepped around rather
// than fought over: a queue full of "Fix the flaky test" is not
// hypothetical.
func TestSyncCreatesBesideAnOccupiedName(t *testing.T) {
	s, st, vault := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	taken := filepath.Join(vault, "Tasks", "Add the parser.md")
	if err := os.MkdirAll(filepath.Dir(taken), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nwf-task: 01OTHER\n---\n# Someone else's note\n"
	if err := os.WriteFile(taken, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if res.Note != "Tasks/Add the parser (neck).md" {
		t.Errorf("Note = %q, want the handle disambiguating it", res.Note)
	}
	if got := read(t, taken); got != body {
		t.Errorf("the other task's note was written:\n%s", got)
	}
}

// Without a vault there is nowhere to write, and guessing would create a
// second vault out of a typo.
func TestSyncWithoutAVaultRefuses(t *testing.T) {
	_, st, _ := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	if _, err := (&Syncer{Store: st}).Sync(rec, Options{Create: true}); !errors.Is(err, ErrNoVault) {
		t.Fatalf("Sync() error = %v, want ErrNoVault", err)
	}
}

// The sweep refreshes what exists and creates nothing: `--all` making one
// note per ledger record is the eager option the design warned fills a
// vault with stubs.
func TestSyncAllRefreshesWithoutCreating(t *testing.T) {
	s, st, vault := fixture(t)
	bound := save(t, st, record("01BOUND", "neck", "Add the parser"))
	save(t, st, record("01FREE", "ankle", "Rename the thing"))

	if _, err := s.Sync(bound, Options{Create: true}); err != nil {
		t.Fatal(err)
	}

	sweep, err := (&Syncer{Vault: vault, Store: st}).SyncAll()
	if err != nil {
		t.Fatalf("SyncAll() error = %v", err)
	}
	if len(sweep.Results) != 1 || sweep.Results[0].Task != "01BOUND" {
		t.Errorf("Results = %+v, want only the bound task", sweep.Results)
	}
	if len(sweep.Unbound) != 1 || sweep.Unbound[0] != "ankle" {
		t.Errorf("Unbound = %v, want the task with no note named", sweep.Unbound)
	}
	if len(sweep.Skipped) != 0 {
		t.Errorf("Skipped = %+v, want none", sweep.Skipped)
	}
	if entries, err := os.ReadDir(filepath.Join(vault, "Tasks")); err != nil || len(entries) != 1 {
		t.Errorf("the sweep created a note: %v %v", entries, err)
	}
}

// store.List returns good records *alongside* a *SkipError. Honoring that
// is the difference between one corrupt ledger file costing its own task
// and costing every task.
func TestSyncAllContinuesPastAnUnreadableLedgerFile(t *testing.T) {
	s, st, vault := fixture(t)
	good := save(t, st, record("01GOOD", "neck", "Add the parser"))
	if _, err := s.Sync(good, Options{Create: true}); err != nil {
		t.Fatal(err)
	}

	// Half a JSON document, exactly as a crashed writer or a bad sync
	// would leave it.
	broken := filepath.Join(st.Dir(), "01BROKEN.json")
	if err := os.WriteFile(broken, []byte(`{"id":"01BROKEN","bindings":[`), 0o644); err != nil {
		t.Fatal(err)
	}

	// A change the good task's note has to pick up, so "the rest were
	// synced" means more than "the sweep returned".
	good, err := st.Load(good.ID)
	if err != nil {
		t.Fatal(err)
	}
	good.Bindings = good.Bindings.Upsert(wf.Binding{
		Kind: wf.KindPR, Ref: "https://github.com/me/app/pull/500",
		State: wf.BindingLive, At: time.Now().UTC(), Via: "r1",
	})
	save(t, st, good)

	sweep, err := (&Syncer{Vault: vault, Store: st}).SyncAll()
	if err != nil {
		t.Fatalf("SyncAll() error = %v, want the sweep to survive one bad file", err)
	}
	if len(sweep.Skipped) != 1 || !strings.Contains(sweep.Skipped[0].Ref, "01BROKEN") {
		t.Fatalf("Skipped = %+v, want the unreadable file named", sweep.Skipped)
	}
	if len(sweep.Results) != 1 || !sweep.Results[0].Changed {
		t.Fatalf("Results = %+v, want the readable task synced anyway", sweep.Results)
	}
	if text := read(t, filepath.Join(vault, "Tasks", "Add the parser.md")); !strings.Contains(text, "#500") {
		t.Errorf("the good task's note was not refreshed:\n%s", text)
	}
}

// A title is not a filename. The name is chosen once and has to survive
// every filesystem and Obsidian's own link syntax.
func TestFileName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Add the parser", "Add the parser"},
		{"fix auth/session bug", "fix auth session bug"},
		{"  spaced   out  ", "spaced out"},
		{`why "this" [breaks]`, "why this breaks"},
		{"trailing dots...", "trailing dots"},
		{strings.Repeat("x", 200), strings.Repeat("x", maxName)},
		{"", ""},
	}
	for _, tt := range tests {
		if got := fileName(tt.in); got != tt.want {
			t.Errorf("fileName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A task with no title at all still gets a note, named for the handle.
func TestSyncNamesANotelessTaskByHandle(t *testing.T) {
	s, st, _ := fixture(t)
	rec := save(t, st, wf.Record{ID: "01BARE", Handle: "wrist", Created: time.Now().UTC()})

	res, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if res.Note != "Tasks/wrist.md" {
		t.Errorf("Note = %q, want it named for the handle", res.Note)
	}
	if text := read(t, filepath.Join(s.Vault, filepath.FromSlash(res.Note))); !strings.Contains(text, "_No runs or bindings yet._") {
		t.Errorf("a task with no history should say so:\n%s", text)
	}
}

// Two notes claiming one task is a human's copy-paste. Picking one would
// make the other's edits invisible.
func TestSyncRefusesTwoNotesClaimingOneTask(t *testing.T) {
	s, st, vault := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	for _, name := range []string{"one.md", "two.md"} {
		if err := os.WriteFile(filepath.Join(vault, name), []byte("---\nwf-task: 01TASK\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.Sync(rec, Options{Create: true}); err == nil {
		t.Fatal("Sync() = nil error, want the ambiguity reported")
	}
}

// The scan skips the vault's own machinery. A note in .trash is a deleted
// note, and resurrecting one as a task's face would be the worst possible
// recovery.
func TestFindSkipsDotDirectories(t *testing.T) {
	s, _, vault := fixture(t)
	trash := filepath.Join(vault, ".trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trash, "deleted.md"), []byte("---\nwf-task: 01TASK\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	found, err := s.find("01TASK")
	if err != nil {
		t.Fatalf("find() error = %v", err)
	}
	if found != "" {
		t.Errorf("find() = %q, want nothing found in a dot directory", found)
	}
}

// Bind is the one writer of the note binding, so `wf bind` and a sync that
// went looking cannot disagree about which binding is the note.
func TestBindRecordsTheNoteBinding(t *testing.T) {
	s, st, _ := fixture(t)
	rec := save(t, st, record("01TASK", "neck", "Add the parser"))

	if err := s.Bind(rec.ID, rec.Handle, "Projects/By hand.md"); err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	after, err := st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, ok := noteBinding(after.Bindings)
	if !ok || b.Ref != "Projects/By hand.md" {
		t.Fatalf("note binding = %+v, %v", b, ok)
	}

	// And a sync then uses it rather than inventing a note of its own.
	if err := os.MkdirAll(filepath.Join(s.Vault, "Projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	handwritten := filepath.Join(s.Vault, "Projects", "By hand.md")
	if err := os.WriteFile(handwritten, []byte("# By hand\n\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err = st.Load(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Sync(after, Options{Create: true})
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if res.Created || res.Note != "Projects/By hand.md" {
		t.Errorf("Result = %+v, want the hand-bound note used as it is", res)
	}
	if text := read(t, handwritten); !strings.Contains(text, "mine") || !strings.Contains(text, taskblock.BeginMarker) {
		t.Errorf("hand-bound note:\n%s", text)
	}
}

// Rel is what decides whether a note is wf's to manage.
func TestRel(t *testing.T) {
	s, _, vault := fixture(t)
	if rel, ok := s.Rel(filepath.Join(vault, "Tasks", "a.md")); !ok || rel != "Tasks/a.md" {
		t.Errorf("Rel(inside) = %q, %v", rel, ok)
	}
	if rel, ok := s.Rel(filepath.Join(filepath.Dir(vault), "elsewhere.md")); ok {
		t.Errorf("Rel(outside) = %q, true; want false", rel)
	}
}

// A note whose text was rendered but whose write failed must never leave a
// truncated file behind. The rename is what guarantees it, so this asserts
// the write really is a rename and not a truncate-in-place.
func TestWriteNoteReplacesRatherThanTruncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := WriteNote(path, "replaced\n"); err != nil {
		t.Fatalf("WriteNote() error = %v", err)
	}
	if got := read(t, path); got != "replaced\n" {
		t.Errorf("content = %q", got)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Error("the note was written in place; a crash mid-write would truncate it")
	}
	// And no temp file is left in the directory to show up in the vault.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("leftovers beside the note: %v", entries)
	}
}

// A task that was never filed still has a title, and its note should be
// named for the work rather than for the handle a human would have typed.
func TestNoteIsNamedForTheWorkOnATrackerlessTask(t *testing.T) {
	s, ledger, vault := fixture(t)

	rec := wf.Record{ID: "t20260101T000000-abc", Handle: "knww", Title: "Add the parser"}
	if err := ledger.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := s.Sync(rec, Options{Create: true})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !strings.Contains(res.Note, "Add the parser") {
		t.Errorf("note path = %q, want it named for the title, not the handle", res.Note)
	}

	text, err := os.ReadFile(filepath.Join(vault, filepath.FromSlash(res.Note)))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.Contains(string(text), "# Add the parser") {
		t.Errorf("heading missing from:\n%s", text)
	}
}
