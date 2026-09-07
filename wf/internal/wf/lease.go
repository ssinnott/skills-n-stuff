package wf

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Leases are owned by the core, not by any backend; see DESIGN.md. Taking a stale lease is a decision the caller makes, never a side effect of reading.

// LeaseKey is the metadata key the lease record is stored under.
const LeaseKey = "wf.lease"

// DefaultTTL survives a slow agent turn but returns a dead worker's task soon.
const DefaultTTL = 15 * time.Minute

// Lease records which wf instance holds a task, and when it last proved alive.
type Lease struct {
	// Actor identifies a wf instance, not a human.
	Actor    string    `json:"actor"`
	Host     string    `json:"host"`
	PID      int       `json:"pid"`
	Acquired time.Time `json:"acquired"`
	Renewed  time.Time `json:"renewed"`
	// TTLSeconds lets a holder with a different config be judged by its own terms.
	TTLSeconds int `json:"ttl_seconds"`
}

// NewLease mints a lease held by actor as of now.
func NewLease(actor string, ttl time.Duration, now time.Time) Lease {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return Lease{
		Actor:      actor,
		Host:       host,
		PID:        os.Getpid(),
		Acquired:   now.UTC(),
		Renewed:    now.UTC(),
		TTLSeconds: int(ttl.Seconds()),
	}
}

// Renew stamps liveness so a long-running task is not stolen for being slow.
func (l Lease) Renew(now time.Time) Lease {
	l.Renewed = now.UTC()
	return l
}

// Encode renders the lease for storage in backend metadata.
func (l Lease) Encode() (string, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return "", fmt.Errorf("encode lease: %w", err)
	}
	return string(b), nil
}

// TTL returns the lease's own expiry window.
func (l Lease) TTL() time.Duration {
	if l.TTLSeconds <= 0 {
		return DefaultTTL
	}
	return time.Duration(l.TTLSeconds) * time.Second
}

// IsStale reports whether the holder has stopped proving liveness.
func (l Lease) IsStale(now time.Time) bool {
	return now.Sub(l.Renewed) > l.TTL()
}

// HeldBy reports whether actor is the holder.
func (l Lease) HeldBy(actor string) bool {
	return l.Actor == actor
}

// String describes the holder for `wf status` and escalation comments.
func (l Lease) String() string {
	return l.Describe(time.Now())
}

// Describe renders the lease relative to now.
func (l Lease) Describe(now time.Time) string {
	state := "live"
	if l.IsStale(now) {
		state = "stale"
	}
	age := now.Sub(l.Renewed).Round(time.Second)
	return fmt.Sprintf("%s on %s (pid %d), renewed %s ago — %s", l.Actor, l.Host, l.PID, age, state)
}

// ParseLease reads a lease out of a metadata value. Absent, malformed and
// foreign values all read as unheld, so a lease we cannot understand cannot wedge the queue.
func ParseLease(value any) (Lease, bool) {
	var raw []byte

	switch v := value.(type) {
	case nil:
		return Lease{}, false
	case string:
		if v == "" {
			return Lease{}, false
		}
		raw = []byte(v)
	case map[string]any:
		b, err := json.Marshal(v)
		if err != nil {
			return Lease{}, false
		}
		raw = b
	default:
		return Lease{}, false
	}

	var l Lease
	if err := json.Unmarshal(raw, &l); err != nil {
		return Lease{}, false
	}
	if l.Actor == "" || l.Renewed.IsZero() {
		return Lease{}, false
	}
	if l.Host == "" {
		l.Host = "unknown"
	}
	if l.Acquired.IsZero() {
		l.Acquired = l.Renewed
	}
	return l, true
}

// Claimable reports whether actor may take the task: its own lease always
// (a resume, not a steal), anyone else's only once stale.
func Claimable(value any, actor string, now time.Time) bool {
	l, ok := ParseLease(value)
	if !ok {
		return true
	}
	if l.HeldBy(actor) {
		return true
	}
	return l.IsStale(now)
}
