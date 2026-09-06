package wf

// wf mints its own task identity.
//
// This is the whole content of "a task exists whether or not a tracker row
// does": identity is available before anything is filed, and the tracker row
// that arrives later is a KindQueue binding rather than the key everything
// else hangs off. Reusing kata's ULID reads as free — no mapping table — and
// costs exactly the two things the task object is for: identity unavailable
// until a row exists, and the backend re-bound to wf at the one place hardest
// to change later.
//
// Standard library only, so no ULID package. What a ULID actually buys here
// is sortability plus collision resistance, and a fixed-width UTC stamp with
// random bytes after it buys both — while staying legible in a `ls ~/.wf/
// tasks`, which an opaque base32 blob does not.

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// stamp is fixed-width and UTC, which is what makes ids sort. Second
// granularity is deliberate: two tasks minted inside one second tie on the
// stamp and order by their random suffix instead, and the record's Created
// field is the authority when the exact order actually matters.
const stamp = "20060102T150405"

// NewTaskID mints wf's own durable task id: sortable by creation, unique
// without coordinating with anything.
//
// The "t" prefix is not decoration. It keeps an id from ever being mistaken
// for a run id (which newRunID prefixes with "r") in a log line or a ref a
// human pastes, and it guarantees the id never starts with a digit, a dot or
// a dash — the shapes store.validID refuses and argv parsing mishandles.
func NewTaskID(now time.Time) string {
	return "t" + now.UTC().Format(stamp) + "-" + randomHex(5)
}

// handleAlphabet drops the characters a human transcribes wrongly: i and l
// against 1, o against 0. A handle exists to be typed, so the cost of a
// larger alphabet is paid by the person typing it.
const handleAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// handleLen is four for the same reason kata's short id is: long enough that
// a ledger of a few hundred tasks rarely collides, short enough to type
// without looking. Collisions are still possible, so NewHandle is not
// authoritative — whoever mints a task checks the ledger and asks again.
const handleLen = 4

// NewHandle mints a short human-facing ref. It is deliberately not derived
// from the title: a title is a field a human is free to edit, and a handle
// that moves when the title does is not a handle.
func NewHandle() string {
	raw := randomBytes(handleLen)
	out := make([]byte, handleLen)
	for i, b := range raw {
		out[i] = handleAlphabet[int(b)%len(handleAlphabet)]
	}
	return string(out)
}

// ValidHandle reports whether a handle a human chose can safely be one.
//
// The rules are the intersection of three constraints that would otherwise
// each be discovered at a different call site: a handle is typed as a bare
// argv word, it is matched against ids by store.Resolve, and it is displayed
// in a fixed-width column. So: no whitespace, nothing that reads as a flag,
// nothing that could name a path, and short.
func ValidHandle(h string) error {
	switch {
	case h == "":
		return fmt.Errorf("handle is empty")
	case len(h) > 32:
		return fmt.Errorf("handle %q is longer than 32 characters", h)
	case strings.HasPrefix(h, "-"):
		return fmt.Errorf("handle %q starts with a dash, which reads as a flag", h)
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("handle %q contains %q; use letters, digits, - or _", h, string(r))
		}
	}
	return nil
}

func randomHex(n int) string { return fmt.Sprintf("%x", randomBytes(n)) }

// randomBytes is a collision guard, never a security property, so a failing
// entropy source falls back to the clock rather than failing the call. A
// task that cannot be created because /dev/urandom hiccuped would be a worse
// outcome than two ids that could theoretically collide.
func randomBytes(n int) []byte {
	out := make([]byte, n)
	if _, err := rand.Read(out); err == nil {
		return out
	}
	var clock [8]byte
	binary.BigEndian.PutUint64(clock[:], uint64(time.Now().UnixNano()))
	for i := range out {
		out[i] = clock[i%len(clock)]
	}
	return out
}
