package wf

import (
	"sort"
	"strings"
	"testing"
	"time"
)

func TestTaskIDsSortByCreation(t *testing.T) {
	// Sortability is the property the format is chosen for: a directory of
	// records listed by filename is listed in the order the work started.
	base := time.Date(2026, 9, 6, 10, 11, 12, 0, time.UTC)
	var ids []string
	for i := 0; i < 20; i++ {
		ids = append(ids, NewTaskID(base.Add(time.Duration(i)*time.Second)))
	}

	shuffled := append([]string{}, ids...)
	sort.Sort(sort.Reverse(sort.StringSlice(shuffled)))
	sort.Strings(shuffled)

	for i := range ids {
		if shuffled[i] != ids[i] {
			t.Fatalf("lexical order != creation order at %d: %q vs %q", i, shuffled[i], ids[i])
		}
	}
}

func TestTaskIDsAreUniqueWithinASecond(t *testing.T) {
	now := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		id := NewTaskID(now)
		if seen[id] {
			t.Fatalf("task id %q was minted twice from one instant", id)
		}
		seen[id] = true
	}
}

func TestTaskIDCannotBeMistakenForARunID(t *testing.T) {
	// Both are stamps with random suffixes and both turn up in log lines and
	// in refs a human pastes. The prefix is what keeps them apart.
	now := time.Now()
	if !strings.HasPrefix(NewTaskID(now), "t") {
		t.Errorf("task id %q does not carry its prefix", NewTaskID(now))
	}
	if strings.HasPrefix(NewTaskID(now), "r") {
		t.Error("a task id must not look like a run id")
	}
}

func TestHandlesAreTypeableAndValid(t *testing.T) {
	for i := 0; i < 200; i++ {
		h := NewHandle()
		if err := ValidHandle(h); err != nil {
			t.Fatalf("NewHandle() produced %q, which ValidHandle refuses: %v", h, err)
		}
		if strings.ContainsAny(h, "il01o") {
			t.Fatalf("handle %q contains a character that reads as another", h)
		}
	}
}

func TestValidHandleRefusesWhatArgvAndTheResolverCannotTake(t *testing.T) {
	bad := map[string]string{
		"empty":         "",
		"a flag":        "-neck",
		"a path":        "a/b",
		"a space":       "two words",
		"too long":      strings.Repeat("n", 33),
		"a wf-id shape": "t2026-09-06T10:11:12",
	}
	for name, h := range bad {
		if err := ValidHandle(h); err == nil {
			t.Errorf("ValidHandle(%q) accepted %s", h, name)
		}
	}
	for _, h := range []string{"neck", "wrist-2", "plan_b", "a1"} {
		if err := ValidHandle(h); err != nil {
			t.Errorf("ValidHandle(%q) = %v, want it accepted", h, err)
		}
	}
}
