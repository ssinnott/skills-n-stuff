package kata

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Integration tests against a real kata daemon.
//
// These are the tests that matter most for this adapter: everything else in
// wf is exercised against fakes, but kata's JSON shapes and flag semantics
// are only knowable by running it. They skip when kata is not installed, so
// `go test ./...` still works on a machine without it — set KATA_BIN or put
// kata on PATH to turn them on.
//
// Four assumptions taken from kata's published reference turned out to be
// wrong, and each has a test below holding the correction in place.

func kataBin(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("KATA_BIN"); bin != "" {
		return bin
	}
	bin, err := exec.LookPath("kata")
	if err != nil {
		t.Skip("kata not installed; set KATA_BIN or put kata on PATH")
	}
	return bin
}

// newProject gives each test its own KATA_HOME and project, so tests cannot
// see each other's issues.
func newProject(t *testing.T) *Backend {
	t.Helper()
	bin := kataBin(t)

	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("KATA_HOME", home)
	t.Setenv("KATA_AUTHOR", "wf-test")

	project := "wftest" + strings.ToLower(filepath.Base(workspace))
	cmd := exec.Command(bin, "init", "--project", project)
	cmd.Dir = workspace
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("kata init failed, skipping integration: %v: %s", err, out)
	}

	t.Cleanup(func() {
		stop := exec.Command(bin, "daemon", "stop")
		stop.Dir = workspace
		_ = stop.Run()
	})

	return New(Options{Bin: bin, Cwd: workspace, Project: project, Actor: "wf-test"})
}

func mustCreate(t *testing.T, b *Backend, in wf.CreateInput) wf.Task {
	t.Helper()
	task, err := b.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("Create(%q) error = %v", in.Title, err)
	}
	return task
}

func TestIntegrationCreateAndGet(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()

	created := mustCreate(t, b, wf.CreateInput{
		Title:    "Add the parser",
		Body:     "It should be tolerant of separators.",
		Labels:   []string{"plan-to-pr"},
		Priority: 1,
	})

	// The regression this whole adapter turns on: kata issues carry BOTH a
	// per-project integer `id` and a 26-character `uid` ULID. Taking the
	// first present key would bind every note and session to the integer.
	if len(created.ID) != 26 {
		t.Errorf("ID = %q, want the 26-character ULID, not the integer id", created.ID)
	}
	if created.ShortID == "" || len(created.ShortID) > 8 {
		t.Errorf("ShortID = %q, want a short ref", created.ShortID)
	}

	got, err := b.Get(ctx, created.ShortID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != created.ID || got.Title != "Add the parser" {
		t.Errorf("Get() = %+v, want the created issue", got)
	}
	if got.Body != "It should be tolerant of separators." {
		t.Errorf("Body = %q", got.Body)
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
	if got.Rev == "" {
		t.Error("Rev is empty; kata's integer revision should be carried through")
	}

	// Labels arrive as objects from `show` and as bare strings from `list`.
	if len(got.Labels) != 1 || got.Labels[0] != "plan-to-pr" {
		t.Errorf("Labels from show = %v, want [plan-to-pr]", got.Labels)
	}
}

func TestIntegrationReadyListsLabels(t *testing.T) {
	b := newProject(t)
	mustCreate(t, b, wf.CreateInput{Title: "Ready work", Labels: []string{"research"}, Priority: 2})

	tasks, err := b.Ready(context.Background(), 10)
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("Ready() returned nothing")
	}

	var found *wf.Task
	for i := range tasks {
		if tasks[i].Title == "Ready work" {
			found = &tasks[i]
		}
	}
	if found == nil {
		t.Fatalf("Ready() did not include the new issue: %+v", tasks)
	}
	if len(found.ID) != 26 {
		t.Errorf("ID = %q, want a ULID", found.ID)
	}
	if len(found.Labels) != 1 || found.Labels[0] != "research" {
		t.Errorf("Labels from ready = %v, want [research]", found.Labels)
	}
}

func TestIntegrationMetadataRoundTrip(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()
	task := mustCreate(t, b, wf.CreateInput{Title: "Metadata carrier"})

	if err := b.SetMeta(ctx, task.ID, "wf.state", "running", wf.SetMetaOptions{}); err != nil {
		t.Fatalf("SetMeta() error = %v", err)
	}

	meta, err := b.GetMeta(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetMeta() error = %v", err)
	}
	if meta["wf.state"] != "running" {
		t.Errorf("metadata = %v, want wf.state=running", meta)
	}

	if err := b.UnsetMeta(ctx, task.ID, "wf.state"); err != nil {
		t.Fatalf("UnsetMeta() error = %v", err)
	}
	meta, _ = b.GetMeta(ctx, task.ID)
	if _, present := meta["wf.state"]; present {
		t.Errorf("metadata = %v, want the key gone", meta)
	}

	// Unsetting an absent key must not fail: release runs on every exit
	// path, including ones where nothing was ever written.
	if err := b.UnsetMeta(ctx, task.ID, "never.set"); err != nil {
		t.Errorf("UnsetMeta() on an absent key error = %v, want nil", err)
	}
}

func TestIntegrationLeaseSurvivesKata(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()
	task := mustCreate(t, b, wf.CreateInput{Title: "Leased work"})

	lease := wf.NewLease("wf-test", 15*time.Minute, time.Now())
	encoded, err := lease.Encode()
	if err != nil {
		t.Fatal(err)
	}

	// The lease is a JSON object stored under one key. If kata mangled it,
	// every stale-reclaim decision downstream would be wrong.
	if err := b.SetMeta(ctx, task.ID, wf.LeaseKey, encoded, wf.SetMetaOptions{JSON: true}); err != nil {
		t.Fatalf("SetMeta(lease) error = %v", err)
	}

	meta, err := b.GetMeta(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetMeta() error = %v", err)
	}
	got, ok := wf.ParseLease(meta[wf.LeaseKey])
	if !ok {
		t.Fatalf("lease did not survive the round trip: %#v", meta[wf.LeaseKey])
	}
	if got.Actor != "wf-test" || got.TTLSeconds != 900 {
		t.Errorf("lease = %+v, want the one written", got)
	}
	if wf.Claimable(meta[wf.LeaseKey], "someone-else", time.Now()) {
		t.Error("a live lease read back from kata should not be claimable")
	}
}

func TestIntegrationClaimRefusesOwnedIssue(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()
	task := mustCreate(t, b, wf.CreateInput{Title: "Contested work"})

	if err := b.Claim(ctx, task.ID, "wf-test"); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}

	// kata's reference documents --if-unowned; it does not exist. A plain
	// claim already refuses an owned issue, which is the semantics wf wants.
	err := b.Claim(ctx, task.ID, "someone-else")
	if err == nil {
		t.Fatal("Claim() on an owned issue succeeded; it must refuse")
	}
	if !strings.Contains(err.Error(), "already_claimed") && !strings.Contains(err.Error(), "already claimed") {
		t.Errorf("Claim() error = %v, want an already-claimed conflict", err)
	}

	got, err := b.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "wf-test" {
		t.Errorf("Owner = %q, want the first claimant", got.Owner)
	}
}

func TestIntegrationReleaseClearsLeaseAndOwner(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()
	task := mustCreate(t, b, wf.CreateInput{Title: "Released work"})

	lease := wf.NewLease("wf-test", time.Minute, time.Now())
	encoded, _ := lease.Encode()
	if err := b.SetMeta(ctx, task.ID, wf.LeaseKey, encoded, wf.SetMetaOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}
	if err := b.Claim(ctx, task.ID, "wf-test"); err != nil {
		t.Fatal(err)
	}

	// kata documents no release verb; `edit --owner ""` is the one that
	// works — `assign` rejects an empty owner outright.
	if err := b.Release(ctx, task.ID); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	got, err := b.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "" {
		t.Errorf("Owner = %q after release, want empty", got.Owner)
	}
	if _, held := wf.ParseLease(got.Meta[wf.LeaseKey]); held {
		t.Error("the lease should be gone after release")
	}

	// A released issue is claimable again, by anyone.
	if err := b.Claim(ctx, task.ID, "another-worker"); err != nil {
		t.Errorf("Claim() after release error = %v", err)
	}
}

func TestIntegrationCloseWithMultipleEvidence(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()
	task := mustCreate(t, b, wf.CreateInput{Title: "Work with evidence"})

	// kata's --pr and --commit sugar take a single value, so a run that
	// opened two PRs has to go through repeated --evidence.
	// kata refuses a close message under 40 characters — see
	// wf.MinCloseMessage, which is why Apply composes one.
	result := wf.CloseResult{
		Message: "Shipped both halves of the parser change; unit tests green.",
		PRs:     []string{"https://example.com/pr/1", "https://example.com/pr/2"},
		Docs:    []string{"Research/plan.md"},
		Tests:   []string{"go test ./..."},
	}
	if err := b.Close(ctx, task.ID, result, "wf-close-key"); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	got, err := b.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != task.ID {
		t.Errorf("Get() after close = %+v", got)
	}

	// A closed issue drops out of ready.
	ready, err := b.Ready(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ready {
		if r.ID == task.ID {
			t.Error("a closed issue is still in ready")
		}
	}
}

func TestIntegrationCloseRequiresMessage(t *testing.T) {
	b := newProject(t)
	task := mustCreate(t, b, wf.CreateInput{Title: "Needs a message"})

	// wf never sends an empty message — ToCloseResult defaults it — and this
	// pins the reason why.
	err := b.Close(context.Background(), task.ID, wf.CloseResult{}, "")
	if err == nil {
		t.Error("Close() with no message succeeded; kata should demand one")
	}
}

func TestIntegrationEscalationsQuery(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()

	flagged := mustCreate(t, b, wf.CreateInput{Title: "Stuck work"})
	mustCreate(t, b, wf.CreateInput{Title: "Fine work"})

	if err := wf.SetState(ctx, b, flagged.ID, wf.StateNeedsHuman); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}

	// The escalation queue is a plain kata metadata query, which is why it
	// also renders in kata's own CLI, TUI and web UI.
	got, err := b.Escalations(ctx)
	if err != nil {
		t.Fatalf("Escalations() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != flagged.ID {
		t.Errorf("Escalations() = %+v, want just the flagged issue", got)
	}

	// Clearing the state clears the flag.
	if err := wf.SetState(ctx, b, flagged.ID, wf.StateRunning); err != nil {
		t.Fatal(err)
	}
	got, _ = b.Escalations(ctx)
	if len(got) != 0 {
		t.Errorf("Escalations() = %+v, want empty after the flag is cleared", got)
	}
}

func TestIntegrationCreateLinkedFollowOn(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()

	parent := mustCreate(t, b, wf.CreateInput{Title: "Original work"})
	child, err := b.Create(ctx, wf.CreateInput{
		Title:          "Follow-on work",
		Body:           "Spawned by a NEXT outcome.",
		RelatedTo:      parent.ID,
		Meta:           map[string]string{"wf.origin": parent.ID},
		IdempotencyKey: "wf-next-" + parent.ID,
	})
	if err != nil {
		t.Fatalf("Create(follow-on) error = %v", err)
	}

	meta, err := b.GetMeta(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta["wf.origin"] != parent.ID {
		t.Errorf("origin metadata = %v, want the parent ULID", meta["wf.origin"])
	}

	// The same idempotency key with the same input must not file a second
	// issue: a retried run otherwise duplicates every follow-up it proposed.
	retry := wf.CreateInput{
		Title:          "Follow-on work",
		Body:           "Spawned by a NEXT outcome.",
		RelatedTo:      parent.ID,
		Meta:           map[string]string{"wf.origin": parent.ID},
		IdempotencyKey: "wf-next-" + parent.ID,
	}
	again, err := b.Create(ctx, retry)
	if err != nil {
		t.Fatalf("Create(retry) error = %v", err)
	}
	if again.ID != child.ID {
		t.Errorf("retry created a second issue (%s vs %s)", again.ID, child.ID)
	}

	// kata guards the key against a *different* payload, so a key reused
	// for other work is a conflict rather than a silent wrong answer.
	changed := retry
	changed.Title = "Different work under the same key"
	if _, err := b.Create(ctx, changed); err == nil {
		t.Error("reusing an idempotency key with different input should conflict")
	}
}

func TestIntegrationCommentAppears(t *testing.T) {
	b := newProject(t)
	ctx := context.Background()
	task := mustCreate(t, b, wf.CreateInput{Title: "Commented work"})

	if err := b.Comment(ctx, task.ID, "**Needs a human** — the run stopped early"); err != nil {
		t.Fatalf("Comment() error = %v", err)
	}
	if err := b.Comment(ctx, task.ID, "second note"); err != nil {
		t.Fatalf("Comment() error = %v", err)
	}
}

func TestIntegrationErrorsAreLegible(t *testing.T) {
	b := newProject(t)

	// kata reports failures as JSON on stdout; a raw exit status would give
	// the operator nothing to act on.
	_, err := b.Get(context.Background(), "zzzz")
	if err == nil {
		t.Fatal("Get() on a missing ref succeeded")
	}
	if strings.Contains(err.Error(), "exit status") {
		t.Errorf("error = %v, want kata's own message rather than an exit status", err)
	}
}
