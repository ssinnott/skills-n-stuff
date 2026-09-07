package store

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Update exists because Load followed by Save is a lost update: the per-id
// lock serializes each Save but not the pair, so two writers that each read
// the same record and append to it end up with one of the appends. These
// tests are about that gap and nothing else.

func TestUpdateAppliesToAnExistingRecord(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(sample("01M1S")); err != nil {
		t.Fatal(err)
	}

	err := s.Update("01M1S", func(rec *wf.Record) error {
		if len(rec.Runs) != 2 {
			t.Errorf("fn saw %d runs, want the saved record", len(rec.Runs))
		}
		rec.Runs = append(rec.Runs, wf.Run{ID: "run-3", Started: time.Now().UTC()})
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := s.Load("01M1S")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 3 || got.Runs[2].ID != "run-3" {
		t.Errorf("runs = %+v, want the appended one", got.Runs)
	}
	// The rest of the record is untouched: Update mutates, it does not
	// replace.
	if len(got.Bindings) != len(sample("01M1S").Bindings) {
		t.Errorf("Update lost part of the record: %+v", got)
	}
}

func TestUpdateCreatesAMissingRecord(t *testing.T) {
	// An upsert on purpose: "record this run, filing the task if this is the
	// first wf has heard of it" must not be a load, a test and a save with
	// the race back in the middle.
	s := New(t.TempDir())

	err := s.Update("fresh", func(rec *wf.Record) error {
		if rec.ID != "fresh" {
			t.Errorf("fn saw ID %q, want the id it was called with", rec.ID)
		}
		if len(rec.Runs) != 0 || len(rec.Bindings) != 0 {
			t.Errorf("a fresh record should be empty: %+v", rec)
		}
		rec.Runs = append(rec.Runs, wf.Run{ID: "run-1", Started: time.Now().UTC()})
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := s.Load("fresh")
	if err != nil {
		t.Fatalf("Update did not create the record: %v", err)
	}
	if len(got.Runs) != 1 || got.Updated.IsZero() {
		t.Errorf("created record = %+v", got)
	}
}

func TestUpdateAbandonsTheWriteOnError(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Save(sample("01M1S")); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("nothing to do")
	err := s.Update("01M1S", func(rec *wf.Record) error {
		rec.Runs = append(rec.Runs, wf.Run{ID: "clobbered"})
		return sentinel
	})
	// Unwrapped, so a caller can decide mid-flight that there is nothing to
	// write and recognize its own signal coming back.
	if !errors.Is(err, sentinel) {
		t.Errorf("Update() error = %v, want the sentinel", err)
	}

	got, err := s.Load("01M1S")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 2 {
		t.Errorf("runs = %+v — an aborted Update must not land", got.Runs)
	}
}

func TestUpdateOnAMissingRecordThatAbortsWritesNothing(t *testing.T) {
	// The upsert only creates when fn agrees. A caller that means "only if
	// it exists" tests the record it was handed and returns an error, and
	// must not be left with a stub record for its trouble.
	s := New(t.TempDir())

	notThere := errors.New("no such task")
	err := s.Update("ghost", func(rec *wf.Record) error {
		if len(rec.Runs) == 0 && len(rec.Bindings) == 0 {
			return notThere
		}
		return nil
	})
	if !errors.Is(err, notThere) {
		t.Fatalf("Update() error = %v, want the caller's sentinel", err)
	}
	if _, err := s.Load("ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Load() error = %v, want no record to have been created", err)
	}
}

func TestUpdateKeepsTheIDItWasCalledWith(t *testing.T) {
	// The id names the lock that was taken, so letting fn move the record to
	// another file would write outside the serialization Update exists to
	// provide.
	s := New(t.TempDir())

	if err := s.Update("real", func(rec *wf.Record) error {
		rec.ID = "somewhere-else"
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := s.Load("real")
	if err != nil {
		t.Fatalf("Load(real) error = %v", err)
	}
	if got.ID != "real" {
		t.Errorf("ID = %q, want the id Update was called with", got.ID)
	}
	if _, err := os.Stat(s.path("somewhere-else")); !os.IsNotExist(err) {
		t.Error("Update wrote to a file other than the one it locked")
	}
}

func TestUpdateRefusesAnInvalidID(t *testing.T) {
	s := New(t.TempDir())
	for _, id := range []string{"", "..", "a/b", ".hidden"} {
		if err := s.Update(id, func(*wf.Record) error { return nil }); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Update(%q) error = %v, want ErrInvalidID", id, err)
		}
	}
}

func TestUpdateRefusesANilMutation(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Update("id", nil); err == nil {
		t.Error("Update(nil) must fail rather than write the record back unchanged")
	}
}

// The one that matters. Every mutator appends its own run; a lost update
// shows up as a missing run, and nothing else here would catch it. Run under
// -race.
func TestConcurrentUpdatesLoseNothing(t *testing.T) {
	s := New(t.TempDir())
	const writers, rounds = 8, 20

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				id := fmt.Sprintf("r-%d-%d", w, i)
				err := s.Update("hot", func(rec *wf.Record) error {
					rec.Runs = append(rec.Runs, wf.Run{ID: id, Started: time.Now().UTC()})
					rec.Bindings = rec.Bindings.Upsert(wf.Binding{
						Kind: wf.KindPR,
						Ref:  "https://example.test/pull/" + id,
						At:   time.Now().UTC(),
						Via:  id,
					})
					return nil
				})
				if err != nil {
					t.Errorf("update: %v", err)
					return
				}
			}
		}(w)
	}
	// Readers race the writers: a record is always whole, and it only ever
	// grows.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen := 0
			for i := 0; i < rounds*3; i++ {
				got, err := s.Load("hot")
				if errors.Is(err, ErrNotFound) {
					continue
				}
				if err != nil {
					t.Errorf("load during concurrent update: %v", err)
					return
				}
				if len(got.Runs) != len(got.Bindings) {
					t.Errorf("record is not internally whole: %d runs, %d bindings",
						len(got.Runs), len(got.Bindings))
					return
				}
				if len(got.Runs) < seen {
					t.Errorf("a record shrank from %d runs to %d", seen, len(got.Runs))
					return
				}
				seen = len(got.Runs)
			}
		}()
	}
	wg.Wait()

	got, err := s.Load("hot")
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if len(got.Runs) != writers*rounds {
		t.Fatalf("runs = %d, want %d — updates were lost", len(got.Runs), writers*rounds)
	}
	ids := make([]string, 0, len(got.Runs))
	for _, run := range got.Runs {
		ids = append(ids, run.ID)
	}
	sort.Strings(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("run %s was written twice", ids[i])
		}
	}
}

// Save and Update take the same per-id lock, so a whole-record replacement
// cannot land inside somebody else's read-modify-write.
func TestUpdateAndSaveDoNotInterleave(t *testing.T) {
	s := New(t.TempDir())
	const rounds = 60

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := s.Save(sample("hot")); err != nil {
				t.Errorf("save: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			err := s.Update("hot", func(rec *wf.Record) error {
				// A Save landing mid-mutation would show up as a partially
				// overwritten record.
				rec.Runs = append(rec.Runs, wf.Run{ID: fmt.Sprintf("u-%d", i)})
				return nil
			})
			if err != nil {
				t.Errorf("update: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	// One file, and it parses: no temp file survived and no write tore.
	got, err := s.Load("hot")
	if err != nil {
		t.Fatalf("final load: %v", err)
	}
	if got.ID != "hot" {
		t.Errorf("final record = %+v", got)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("ledger dir holds %d entries, want only hot.json", len(entries))
	}
}
