package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// sample is a record with the shape the ledger actually has to survive: two
// runs, and bindings that hang off each of them plus one that predates any
// run at all.
func sample(id, handle string) wf.Record {
	started := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	ended := started.Add(20 * time.Minute)
	return wf.Record{
		ID:      id,
		Handle:  handle,
		Created: started.Add(-time.Hour),
		Runs: []wf.Run{
			{
				ID:       "run-1",
				Workflow: "plan-to-pr",
				Profile:  "coding",
				Model:    "sonnet-5",
				Host:     "wf-laptop",
				Started:  started,
				Ended:    &ended,
				Outcome:  wf.SessionEscalated,
			},
			{
				ID:       "run-2",
				Workflow: "plan-to-pr",
				Model:    "opus-5",
				Started:  started.Add(2 * time.Hour),
			},
		},
		Bindings: wf.Bindings{
			{
				Kind:  wf.KindQueue,
				Ref:   "01M1SABCDEFGHJKMNPQRSTVWXY",
				Label: "Add the parser",
				State: wf.BindingLive,
				At:    started.Add(-time.Hour),
				Meta:  map[string]string{wf.MetaBackend: "kata"},
			},
			{
				Kind:  wf.KindWorkspace,
				Ref:   "/home/me/.wf/worktrees/neck-add-parser",
				State: wf.BindingSuperseded,
				At:    started,
				Via:   "run-1",
				Host:  "wf-laptop",
				Meta: map[string]string{
					wf.MetaBranch: "wf/neck-add-parser",
					wf.MetaBase:   "main",
					wf.MetaRepo:   "/home/me/code/app",
				},
			},
			{
				Kind:  wf.KindPR,
				Ref:   "https://github.com/me/app/pull/412",
				Label: "Add the parser",
				State: wf.BindingLive,
				At:    started.Add(3 * time.Hour),
				Via:   "run-2",
			},
		},
	}
}

func TestRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	want := sample("01WFAAAAAAAAAAAAAAAAAAAAAA", "neck")

	if err := s.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load(want.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Updated is stamped by the store, so compare it separately and then
	// take it out of the way of the deep comparison.
	if got.Updated.IsZero() {
		t.Error("Updated was not stamped on save")
	}
	got.Updated = time.Time{}

	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if string(wantJSON) != string(gotJSON) {
		t.Errorf("round trip changed the record:\n want %s\n  got %s", wantJSON, gotJSON)
	}

	// Spot-check the parts a marshal comparison would let slide silently
	// if the type ever loses a json tag.
	if len(got.Runs) != 2 || got.Runs[0].Model != "sonnet-5" || !got.Runs[0].Done() || got.Runs[1].Done() {
		t.Errorf("runs did not survive: %+v", got.Runs)
	}
	ws, ok := got.Bindings.Current(wf.KindWorkspace)
	if ok {
		t.Errorf("superseded workspace came back as current: %+v", ws)
	}
	pr, ok := got.Bindings.Current(wf.KindPR)
	if !ok || pr.Via != "run-2" || pr.Ref != "https://github.com/me/app/pull/412" {
		t.Errorf("pr binding did not survive: %+v", pr)
	}
	if q, ok := got.QueueRef(); !ok || q != "01M1SABCDEFGHJKMNPQRSTVWXY" {
		t.Errorf("queue ref = %q, %v", q, ok)
	}
}

func TestUpdatedStampedOnEverySave(t *testing.T) {
	s := New(t.TempDir())
	rec := wf.Record{ID: "a", Updated: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)}

	before := time.Now().UTC().Add(-time.Second)
	if err := s.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load("a")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Updated.Before(before) {
		t.Errorf("Updated = %v, want a fresh stamp not the caller's stale one", got.Updated)
	}
}

// The store persists the id it is handed. Minting is a later stage's job and
// must not leak in here.
func TestSaveMintsNoID(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(wf.Record{ID: "given-id"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.Load("given-id")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.ID != "given-id" {
		t.Errorf("ID = %q, want the one it was given", got.ID)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "given-id.json")); err != nil {
		t.Errorf("record did not land at <root>/tasks/<id>.json: %v", err)
	}
}

func TestLoadMissingIsTypedNotFound(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.Load("nope")
	if err == nil {
		t.Fatal("loading a missing record succeeded")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want it to unwrap to ErrNotFound", err)
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) || nf.Ref != "nope" {
		t.Errorf("err = %v, want a *NotFoundError naming the ref", err)
	}
}

func TestMissingDirectoryIsAnEmptyStore(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "never-created"))

	recs, err := s.List()
	if err != nil || len(recs) != 0 {
		t.Errorf("List over a missing dir = %v, %v; want empty and no error", recs, err)
	}
	if _, err := s.Load("x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load over a missing dir = %v, want not-found", err)
	}
	if _, err := s.Resolve("x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve over a missing dir = %v, want not-found", err)
	}
	if err := s.Delete("x"); err != nil {
		t.Errorf("Delete over a missing dir = %v, want nil", err)
	}
}

func TestListSkipsCorruptFileAndKeepsSiblings(t *testing.T) {
	s := New(t.TempDir())
	for _, id := range []string{"aaa", "ccc"} {
		if err := s.Save(wf.Record{ID: id}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	// Sits alphabetically between the two good ones, so a naive
	// implementation that bails on the first bad file loses "ccc".
	bad := filepath.Join(s.Dir(), "bbb.json")
	if err := os.WriteFile(bad, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	recs, err := s.List()
	if len(recs) != 2 || recs[0].ID != "aaa" || recs[1].ID != "ccc" {
		t.Fatalf("List = %+v, want both good records", recs)
	}
	var skip *SkipError
	if !errors.As(err, &skip) {
		t.Fatalf("err = %v, want a *SkipError", err)
	}
	if len(skip.Paths()) != 1 || skip.Paths()[0] != bad {
		t.Errorf("skipped = %v, want just %s", skip.Paths(), bad)
	}
	if !strings.Contains(skip.Error(), "bbb.json") {
		t.Errorf("error message %q does not name the bad file", skip.Error())
	}

	// The corrupt file must not take Resolve down with it either.
	if got, err := s.Resolve("ccc"); err != nil || got.ID != "ccc" {
		t.Errorf("Resolve past a corrupt sibling = %+v, %v", got, err)
	}
}

// Loading a corrupt record by id is a real error: there is no sibling to
// salvage, and answering "no such task" would be a lie.
func TestLoadCorruptIsNotNotFound(t *testing.T) {
	s := New(t.TempDir())
	if err := os.MkdirAll(s.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "bad.json"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := s.Load("bad")
	if err == nil {
		t.Fatal("loading a corrupt record succeeded")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want a parse error rather than not-found", err)
	}
}

func TestDelete(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(wf.Record{ID: "gone"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Delete("gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Load("gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("load after delete = %v, want not-found", err)
	}
	if err := s.Delete("gone"); err != nil {
		t.Errorf("second delete = %v, want nil", err)
	}
}

func TestInvalidIDsAreRefused(t *testing.T) {
	s := New(t.TempDir())
	for _, id := range []string{"", ".", "..", "../escape", "a/b", ".hidden"} {
		if err := s.Save(wf.Record{ID: id}); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Save(%q) = %v, want ErrInvalidID", id, err)
		}
		if _, err := s.Load(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Load(%q) = %v, want ErrInvalidID", id, err)
		}
		if err := s.Delete(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Delete(%q) = %v, want ErrInvalidID", id, err)
		}
	}
}

func TestResolveByEachRefForm(t *testing.T) {
	s := New(t.TempDir())
	rec := sample("01WFAAAAAAAAAAAAAAAAAAAAAA", "neck")
	if err := s.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A second task so a resolver that just returns the only record fails.
	other := sample("01WFZZZZZZZZZZZZZZZZZZZZZZ", "shin")
	other.Bindings[0].Ref = "01M1SZZZZZZZZZZZZZZZZZZZZZ"
	if err := s.Save(other); err != nil {
		t.Fatalf("save other: %v", err)
	}

	cases := []struct{ name, ref string }{
		{"wf id", "01WFAAAAAAAAAAAAAAAAAAAAAA"},
		{"wf id, lowercased by a copy-paste", "01wfaaaaaaaaaaaaaaaaaaaaaa"},
		{"wf id prefix", "01WFA"},
		{"handle", "neck"},
		{"queue binding ref (kata ULID)", "01M1SABCDEFGHJKMNPQRSTVWXY"},
		{"queue short id (the ULID's tail)", "wxy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Resolve(tc.ref)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.ref, err)
			}
			if got.ID != rec.ID {
				t.Errorf("Resolve(%q) = %s, want %s", tc.ref, got.ID, rec.ID)
			}
		})
	}

	if _, err := s.Resolve("no-such-ref"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve of an unknown ref = %v, want not-found", err)
	}
	if _, err := s.Resolve(""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve(\"\") = %v, want not-found", err)
	}
}

func TestResolveAmbiguousNamesCandidates(t *testing.T) {
	s := New(t.TempDir())
	for _, r := range []wf.Record{
		{ID: "01WFAAAA", Handle: "neck"},
		{ID: "01WFBBBB", Handle: "next"},
		{ID: "01WFCCCC", Handle: "shin"},
	} {
		if err := s.Save(r); err != nil {
			t.Fatalf("save %s: %v", r.ID, err)
		}
	}

	_, err := s.Resolve("ne")
	if err == nil {
		t.Fatal("an ambiguous ref resolved silently")
	}
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("err = %v, want an *AmbiguousError", err)
	}
	if len(amb.Candidates) != 2 {
		t.Fatalf("candidates = %v, want the two matches", amb.Candidates)
	}
	msg := amb.Error()
	for _, want := range []string{"01WFAAAA", "neck", "01WFBBBB", "next"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not name %q", msg, want)
		}
	}
	if strings.Contains(msg, "shin") {
		t.Errorf("message %q names a task that did not match", msg)
	}
}

// A ref that hits one task exactly must not be dragged into ambiguity by a
// longer task it happens to prefix.
func TestResolveExactBeatsPartial(t *testing.T) {
	s := New(t.TempDir())
	for _, r := range []wf.Record{{ID: "a1", Handle: "neck"}, {ID: "a2", Handle: "necklace"}} {
		if err := s.Save(r); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	got, err := s.Resolve("neck")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "a1" {
		t.Errorf("Resolve(\"neck\") = %s, want the exact match a1", got.ID)
	}
}

// A single stray character must not resolve a lone task.
func TestResolveRejectsAOneCharacterRef(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(wf.Record{ID: "abcdef", Handle: "neck"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := s.Resolve("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resolve(\"a\") = %v, want not-found", err)
	}
}

// `wf run --max 3` puts several runs in one process. Different tasks are
// different files and never contend; two writers on the *same* task must
// still leave one whole record. Run this under -race.
func TestConcurrentSaveSameID(t *testing.T) {
	s := New(t.TempDir())
	const writers, rounds = 8, 25

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				rec := wf.Record{
					ID:     "hot",
					Handle: fmt.Sprintf("writer-%d", w),
					Bindings: wf.Bindings{{
						Kind: wf.KindPR,
						Ref:  fmt.Sprintf("https://example.test/pull/%d-%d", w, i),
						At:   time.Now().UTC(),
					}},
				}
				if err := s.Save(rec); err != nil {
					t.Errorf("save: %v", err)
					return
				}
			}
		}(w)
	}
	// Readers race the writers: every Load must see a whole record, never
	// a half-written one.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds*2; i++ {
				got, err := s.Load("hot")
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					t.Errorf("load during concurrent save: %v", err)
					return
				}
				if got.ID != "hot" || len(got.Bindings) != 1 || got.Updated.IsZero() {
					t.Errorf("load saw a partial record: %+v", got)
					return
				}
			}
		}()
	}
	wg.Wait()

	got, err := s.Load("hot")
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if !strings.HasPrefix(got.Handle, "writer-") || len(got.Bindings) != 1 {
		t.Errorf("final record is not one whole write: %+v", got)
	}
	// One task is one file, plus nothing: every temp file was renamed away
	// or cleaned up.
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "hot.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("ledger dir = %v, want only hot.json", names)
	}
}

// Concurrent saves to *different* tasks touch different files, which is the
// property the one-file-per-task layout was chosen for.
func TestConcurrentSaveDifferentIDs(t *testing.T) {
	s := New(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("task-%02d", i)
			if err := s.Save(wf.Record{ID: id, Handle: id}); err != nil {
				t.Errorf("save %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	recs, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 12 {
		t.Errorf("List = %d records, want 12", len(recs))
	}
}

// A crash mid-write must leave the previous record intact, never a truncated
// one. The write goes to a temp file and is renamed over the target, so the
// target is only ever replaced whole — and a temp file a crashed writer left
// behind is invisible to the ledger.
func TestCrashMidWriteLeavesNoPartialRecord(t *testing.T) {
	s := New(t.TempDir())
	original := sample("01WFAAAA", "neck")
	if err := s.Save(original); err != nil {
		t.Fatalf("save: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(s.Dir(), "01WFAAAA.json"))
	if err != nil {
		t.Fatal(err)
	}

	// Stand in for a process killed between opening its temp file and
	// renaming it: the temp file is there, the target is untouched.
	leftover := filepath.Join(s.Dir(), ".01WFAAAA.json.tmp917")
	if err := os.WriteFile(leftover, []byte(`{"id":"01WFAAAA","bindings":[{"ki`), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := s.Load("01WFAAAA")
	if err != nil {
		t.Fatalf("load after a crashed write: %v", err)
	}
	if len(got.Bindings) != len(original.Bindings) || len(got.Runs) != len(original.Runs) {
		t.Errorf("the crashed write damaged the record: %+v", got)
	}
	after, err := os.ReadFile(filepath.Join(s.Dir(), "01WFAAAA.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the record changed even though the write never renamed")
	}

	// A half-written temp file is not a corrupt record: List must neither
	// return it nor report it as one.
	recs, err := s.List()
	if err != nil {
		t.Errorf("List reported the leftover temp file: %v", err)
	}
	if len(recs) != 1 || recs[0].ID != "01WFAAAA" {
		t.Errorf("List = %+v, want just the intact record", recs)
	}

	// And the next successful save renames over it cleanly.
	if err := s.Save(original); err != nil {
		t.Fatalf("save after a crashed write: %v", err)
	}
	if _, err := s.Load("01WFAAAA"); err != nil {
		t.Errorf("load after recovery: %v", err)
	}
}

// Every write lands via rename, so the target inode is replaced rather than
// truncated in place — which is the mechanism that makes a partial read
// impossible for any reader holding the old file open.
func TestSaveReplacesRatherThanTruncates(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(wf.Record{ID: "x", Handle: "first"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	path := filepath.Join(s.Dir(), "x.json")
	open, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()

	big := wf.Record{ID: "x", Handle: "second"}
	for i := 0; i < 200; i++ {
		big.Bindings = append(big.Bindings, wf.Binding{Kind: wf.KindDoc, Ref: fmt.Sprintf("doc-%d", i)})
	}
	if err := s.Save(big); err != nil {
		t.Fatalf("second save: %v", err)
	}

	// The handle still open sees the record as it was, whole.
	raw, err := os.ReadFile("/proc/self/fd/" + fmt.Sprint(open.Fd()))
	if err != nil {
		// Not Linux, or /proc is unavailable — the rename semantics are
		// still exercised by the readers in TestConcurrentSaveSameID.
		t.Skipf("cannot re-read the open handle: %v", err)
	}
	var held wf.Record
	if err := json.Unmarshal(raw, &held); err != nil {
		t.Fatalf("the file a reader held open was truncated under it: %v", err)
	}
	if held.Handle != "first" {
		t.Errorf("held record handle = %q, want the pre-rename %q", held.Handle, "first")
	}
}

func TestRootDefaultsBesideConfig(t *testing.T) {
	if got, want := Root("/somewhere/.wf/config.json"), "/somewhere/.wf"; got != want {
		t.Errorf("Root = %q, want %q", got, want)
	}
	if Root("") != DefaultRoot() {
		t.Errorf("Root(\"\") = %q, want DefaultRoot %q", Root(""), DefaultRoot())
	}
	if got, want := New("/somewhere/.wf").Dir(), filepath.Join("/somewhere/.wf", "tasks"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	if New("").Dir() != filepath.Join(DefaultRoot(), "tasks") {
		t.Errorf("New(\"\") = %q, want it under DefaultRoot", New("").Dir())
	}
}

// The ledger is meant to be readable with cat and jq, like everything else
// wf writes beside it.
func TestRecordOnDiskIsPlainIndentedJSON(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(sample("01WFAAAA", "neck")); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir(), "01WFAAAA.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\n  \"handle\": \"neck\"") {
		t.Errorf("file is not indented JSON:\n%s", raw)
	}
	if !strings.HasSuffix(string(raw), "}\n") {
		t.Error("file does not end in a newline")
	}
	info, err := os.Stat(filepath.Join(s.Dir(), "01WFAAAA.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %v, want 0644 like the rest of what wf writes", perm)
	}
}

// A tracker's short ref resolves when the queue binding recorded it. kata
// derives its short id from the ULID's last four characters, so this is the
// case no prefix rule can reach and the reason MetaShortID exists.
func TestResolveByRecordedShortID(t *testing.T) {
	s := New(t.TempDir())

	rec := wf.Record{
		ID:     "01JQZK9T7WPX3RMBVCN8YD4EFG",
		Handle: "parser",
		Bindings: wf.Bindings{{
			Kind: wf.KindQueue,
			Ref:  "01M1SZXQ9V4KBHT2NPRDWCF7EG",
			Meta: map[string]string{
				wf.MetaBackend: "kata",
				wf.MetaShortID: "f7eg",
			},
		}},
	}
	if err := s.Save(rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Resolve("f7eg")
	if err != nil {
		t.Fatalf("Resolve(f7eg): %v", err)
	}
	if got.ID != rec.ID {
		t.Errorf("Resolve(f7eg).ID = %q, want %q", got.ID, rec.ID)
	}

	// And in whichever case the surface it was copied from displayed it —
	// kata lowercases its short ids where its web UI does not.
	got, err = s.Resolve("F7EG")
	if err != nil {
		t.Fatalf("Resolve(F7EG): %v", err)
	}
	if got.ID != rec.ID {
		t.Errorf("Resolve(F7EG).ID = %q, want %q", got.ID, rec.ID)
	}
}

// The recorded short id must not out-rank a record whose own id is what was
// typed: an exact id is never ambiguous with someone else's short ref.
func TestResolveShortIDDoesNotShadowAnExactID(t *testing.T) {
	s := New(t.TempDir())

	if err := s.Save(wf.Record{ID: "abcd", Handle: "direct"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	other := wf.Record{
		ID: "01JQZK9T7WPX3RMBVCN8YD4EFG",
		Bindings: wf.Bindings{{
			Kind: wf.KindQueue,
			Ref:  "01M1SZXQ9V4KBHT2NPRDWCABCD",
			Meta: map[string]string{wf.MetaShortID: "abcd"},
		}},
	}
	if err := s.Save(other); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Both match in the strict pass, so this is a genuine collision and
	// must be reported rather than silently picked.
	_, err := s.Resolve("abcd")
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("Resolve(abcd) err = %v, want AmbiguousError naming both", err)
	}
	if len(amb.Candidates) != 2 {
		t.Errorf("candidates = %v, want both records named", amb.Candidates)
	}
}
