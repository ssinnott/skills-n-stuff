package wf

import (
	"testing"
	"time"
)

var base = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func TestLeaseRoundTrip(t *testing.T) {
	lease := NewLease("wf-1", 10*time.Minute, base)
	encoded, err := lease.Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	got, ok := ParseLease(encoded)
	if !ok {
		t.Fatal("ParseLease() failed on a lease we just encoded")
	}
	if got.Actor != "wf-1" || got.TTLSeconds != 600 {
		t.Errorf("round trip lost fields: %+v", got)
	}
	if !got.Renewed.Equal(base) {
		t.Errorf("Renewed = %v, want %v", got.Renewed, base)
	}
}

func TestParseLeaseRejectsGarbage(t *testing.T) {
	// A lease we cannot understand must read as unheld, never wedge the queue.
	for _, value := range []any{nil, "", "not json", "{}", `{"actor":""}`, 42, []string{"x"}} {
		if _, ok := ParseLease(value); ok {
			t.Errorf("ParseLease(%#v) = held, want unheld", value)
		}
	}
}

func TestParseLeaseAcceptsDecodedObject(t *testing.T) {
	// Backends that hand back parsed JSON rather than a string still work.
	value := map[string]any{"actor": "wf-2", "renewed": base.Format(time.RFC3339), "ttl_seconds": 60}
	got, ok := ParseLease(value)
	if !ok {
		t.Fatal("ParseLease() rejected a decoded object")
	}
	if got.Actor != "wf-2" {
		t.Errorf("Actor = %q, want %q", got.Actor, "wf-2")
	}
	if got.Host != "unknown" {
		t.Errorf("Host = %q, want the unknown fallback", got.Host)
	}
}

func TestIsStale(t *testing.T) {
	lease := NewLease("wf-1", time.Minute, base)

	if lease.IsStale(base.Add(30 * time.Second)) {
		t.Error("a lease inside its TTL is not stale")
	}
	if !lease.IsStale(base.Add(90 * time.Second)) {
		t.Error("a lease past its TTL is stale")
	}
	// Renewal is what proves liveness, so a long task is not stolen.
	renewed := lease.Renew(base.Add(50 * time.Second))
	if renewed.IsStale(base.Add(90 * time.Second)) {
		t.Error("a renewed lease must not be stale")
	}
}

func TestClaimable(t *testing.T) {
	lease := NewLease("wf-1", time.Minute, base)
	encoded, err := lease.Encode()
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	if !Claimable(nil, "wf-2", base) {
		t.Error("an unheld task is claimable")
	}
	if Claimable(encoded, "wf-2", base.Add(10*time.Second)) {
		t.Error("a live lease held by someone else is not claimable")
	}
	if !Claimable(encoded, "wf-1", base.Add(10*time.Second)) {
		t.Error("resuming one's own live lease is claimable")
	}
	if !Claimable(encoded, "wf-2", base.Add(2*time.Minute)) {
		t.Error("a stale lease is claimable by anyone")
	}
}

func TestLeaseTTLFallback(t *testing.T) {
	// A record written without a TTL is judged by the default, not by zero —
	// otherwise every such lease reads as instantly stale.
	got, ok := ParseLease(`{"actor":"wf-1","renewed":"2026-09-05T12:00:00Z"}`)
	if !ok {
		t.Fatal("ParseLease() failed")
	}
	if got.TTL() != DefaultTTL {
		t.Errorf("TTL() = %v, want %v", got.TTL(), DefaultTTL)
	}
	if got.IsStale(base.Add(time.Minute)) {
		t.Error("a TTL-less lease must not read as immediately stale")
	}
}
