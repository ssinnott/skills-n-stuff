// Package store is wf's local task ledger: a per-host cache of runs and
// machine-local bindings, keyed by the tracker's own id. A missing ledger
// reads as an empty store, never an error. Callers depend on Store, never on
// FileStore. See DESIGN-slim.md.
package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Store is the whole of what a ledger has to do: four verbs, no transaction.
type Store interface {
	// Load returns one record by the tracker's id; a record not there is a *NotFoundError.
	Load(id string) (wf.Record, error)
	// Update is read-modify-write on one record: an upsert, since a missing record arrives as a zero Record. See the lock comment in file.go for its guarantees.
	Update(id string, fn func(*wf.Record) error) error
	// List returns every readable record; an unreadable one is skipped and named in a *SkipError.
	List() ([]wf.Record, error)
	// Delete removes a record. Removing one already gone is not an error.
	Delete(id string) error
}

// ErrNotFound is what a missing record unwraps to, for errors.Is.
var ErrNotFound = errors.New("task not found")

// ErrInvalidID rejects an id that cannot safely name a file.
var ErrInvalidID = errors.New("invalid task id")

// NotFoundError names the ref that found nothing.
type NotFoundError struct{ Ref string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("task %q: %s", e.Ref, ErrNotFound) }

// Unwrap makes errors.Is(err, ErrNotFound) work.
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// SkippedFile is one ledger entry that could not be read, and why.
type SkippedFile struct {
	Path string
	Err  error
}

// SkipError reports the entries List could not read, alongside the records that did read.
type SkipError struct{ Skipped []SkippedFile }

func (e *SkipError) Error() string {
	parts := make([]string, 0, len(e.Skipped))
	for _, s := range e.Skipped {
		parts = append(parts, fmt.Sprintf("%s: %v", s.Path, s.Err))
	}
	return fmt.Sprintf("skipped %d unreadable task file(s): %s", len(e.Skipped), strings.Join(parts, "; "))
}
