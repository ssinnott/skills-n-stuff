package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
)

// --- fakes ---------------------------------------------------------------

// basicLabel routes a task to the one workflow every test has loaded
// unless it says otherwise: a run needs a recipe, and nothing here is about
// which one.
const basicLabel = "work"

type fakeQueue struct {
	mu       sync.Mutex
	tasks    map[string]*wf.Task
	comments []string
	closed   map[string]wf.CloseResult
	claims   []string
	releases []string
	created  []wf.CreateInput
}

// newQueue holds the given tasks. A task with no labels is routed to the
// basic workflow, so a test that is not about selection need not say.
func newQueue(tasks ...wf.Task) *fakeQueue {
	q := &fakeQueue{tasks: map[string]*wf.Task{}, closed: map[string]wf.CloseResult{}}
	for i := range tasks {
		t := tasks[i]
		if t.Meta == nil {
			t.Meta = map[string]any{}
		}
		if len(t.Labels) == 0 {
			t.Labels = []string{basicLabel}
		}
		q.tasks[t.ID] = &t
	}
	return q
}

func (q *fakeQueue) Ready(_ context.Context, limit int) ([]wf.Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []wf.Task
	for _, t := range q.tasks {
		if _, done := q.closed[t.ID]; done || len(out) >= limit {
			continue
		}
		out = append(out, *t)
	}
	return out, nil
}

func (q *fakeQueue) Get(_ context.Context, ref string) (wf.Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.tasks[ref]
	if !ok {
		return wf.Task{}, fmt.Errorf("no such task %s", ref)
	}
	return *t, nil
}

func (q *fakeQueue) Claim(_ context.Context, ref, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.claims = append(q.claims, ref)
	return nil
}

func (q *fakeQueue) Release(_ context.Context, ref string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.releases = append(q.releases, ref)
	delete(q.tasks[ref].Meta, wf.LeaseKey)
	return nil
}

func (q *fakeQueue) Comment(_ context.Context, _, body string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.comments = append(q.comments, body)
	return nil
}

func (q *fakeQueue) Close(_ context.Context, ref string, r wf.CloseResult) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed[ref] = r
	return nil
}

func (q *fakeQueue) Create(_ context.Context, in wf.CreateInput) (wf.Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.created = append(q.created, in)
	return wf.Task{ID: "new", ShortID: "new1", Title: in.Title}, nil
}

func (q *fakeQueue) SetMeta(_ context.Context, ref, key, value string, _ wf.SetMetaOptions) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t, ok := q.tasks[ref]; ok {
		t.Meta[key] = value
	}
	return nil
}

func (q *fakeQueue) UnsetMeta(_ context.Context, ref, key string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if t, ok := q.tasks[ref]; ok {
		delete(t.Meta, key)
	}
	return nil
}

func (q *fakeQueue) meta(ref, key string) any {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.tasks[ref].Meta[key]
}

type fakeRun struct {
	transcript string
	err        error
	path       string
	release    chan struct{}
}

func (r *fakeRun) SessionID() string   { return "sess-" + r.path }
func (r *fakeRun) SessionPath() string { return r.path }

func (r *fakeRun) Wait(context.Context) (wf.RunResult, error) {
	if r.release != nil {
		<-r.release
	}
	return wf.RunResult{TranscriptTail: r.transcript}, r.err
}

type fakeRunner struct {
	mu         sync.Mutex
	transcript string
	err        error
	prompts    []string
	profiles   []string
	models     []string
	cwds       []string
	release    chan struct{}
}

func (r *fakeRunner) Start(_ context.Context, opts wf.RunOptions) (wf.RunHandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompts = append(r.prompts, opts.Prompt)
	r.profiles = append(r.profiles, opts.ProfileDir)
	r.models = append(r.models, opts.Model)
	r.cwds = append(r.cwds, opts.Cwd)
	return &fakeRun{
		transcript: r.transcript,
		err:        r.err,
		path:       fmt.Sprintf("/sessions/run-%d.jsonl", len(r.prompts)),
		release:    r.release,
	}, nil
}

type fakeWorkspace struct {
	path     string
	branch   string
	disposed bool
	kept     bool
}

func (w *fakeWorkspace) Path() string   { return w.path }
func (w *fakeWorkspace) Branch() string { return w.branch }
func (w *fakeWorkspace) Keep()          { w.kept = true }
func (w *fakeWorkspace) Dispose(context.Context) error {
	if w.kept {
		return nil
	}
	w.disposed = true
	return nil
}

// fakeProvider behaves the way the real one does: a run handed a branch
// that a kept checkout still holds continues in that checkout; a run
// handed a branch nothing holds gets a new directory on that branch; a run
// handed nothing gets a fresh directory and a fresh branch.
type fakeProvider struct {
	mu     sync.Mutex
	runs   int
	spaces []*fakeWorkspace
	// hints records the branch each Create was handed.
	hints []string
}

func (p *fakeProvider) Create(_ context.Context, task wf.Task, branch string) (wf.Workspace, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hints = append(p.hints, branch)
	p.runs++
	if branch != "" {
		for _, space := range p.spaces {
			if space.branch == branch && space.kept && !space.disposed {
				space.kept = false
				return space, nil
			}
		}
	}
	suffix := ""
	if p.runs > 1 {
		suffix = fmt.Sprintf("-%d", p.runs)
	}
	space := &fakeWorkspace{path: "/work/" + task.ShortID + suffix, branch: branch}
	if space.branch == "" {
		space.branch = "wf/" + task.ShortID + suffix
	}
	p.spaces = append(p.spaces, space)
	return space, nil
}

// basicFlows is the one recipe a test that is not about workflows needs.
func basicFlows(t *testing.T) *workflow.Set {
	t.Helper()
	return loadFlows(t, map[string]string{
		"work.md": "---\nname: work\nlabels: " + basicLabel + "\n---\n{{TASK_TITLE}}\n\n{{TASK_BODY}}\n",
	})
}

func newSupervisor(q *fakeQueue, r *fakeRunner, p *fakeProvider, flows *workflow.Set) *Supervisor {
	return &Supervisor{
		Queue:      q,
		Runner:     r,
		Workflows:  flows,
		Config:     &config.Config{Actor: "wf-test", LeaseTTLSeconds: 900},
		Workspaces: func(workflow.Workflow) wf.WorkspaceProvider { return p },
	}
}

func loadFlows(t *testing.T, sources map[string]string) *workflow.Set {
	t.Helper()
	dir := t.TempDir()
	for name, body := range sources {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	set, err := workflow.Load(dir)
	if err != nil {
		t.Fatalf("workflow.Load() error = %v", err)
	}
	return set
}

// run dispatches one task by ref under whatever its labels select.
func run(s *Supervisor, ref string) (Result, error) {
	return s.RunOnce(context.Background(), ref, Dispatch{})
}

// --- tests ---------------------------------------------------------------

func TestRunOnceCompletesAndLeavesTheTaskOpen(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser"})
	r := &fakeRunner{transcript: "PR: https://a/1 — Add it\nDONE Shipped\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, basicFlows(t))

	result, err := run(s, "01HZ")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if !result.Applied.Completed || result.Applied.Escalated {
		t.Errorf("Applied = %+v, want completed", result.Applied)
	}
	// A run never closes the task: it is one step of a longer unit of
	// work, and a human closes that. The task waits in review.
	if _, closed := q.closed["01HZ"]; closed {
		t.Error("a run must not close the task")
	}
	if q.meta("01HZ", wf.StateKey) != string(wf.StateReview) {
		t.Errorf("state = %v, want review", q.meta("01HZ", wf.StateKey))
	}
	if prs := wf.PRsFromMeta(q.tasks["01HZ"].Meta); len(prs) != 1 {
		t.Errorf("PR not recorded on the task for a later close: %v", prs)
	}
	// The lease is always released, and the workspace disposed on completion.
	if len(q.releases) != 1 {
		t.Errorf("releases = %v, want exactly one", q.releases)
	}
	if !p.spaces[0].disposed {
		t.Error("a completed run should dispose its worktree")
	}
}

func TestRunOnceEscalatesWithoutDone(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser"})
	r := &fakeRunner{transcript: "I got confused and stopped.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, basicFlows(t))

	result, err := run(s, "01HZ")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if !result.Applied.Escalated || result.Applied.Completed {
		t.Errorf("Applied = %+v, want escalated", result.Applied)
	}
	if q.meta("01HZ", wf.AttentionKey) != "needs-human" {
		t.Error("escalated task must be flagged for a human")
	}
	// The checkout survives: it is the evidence a human needs.
	if p.spaces[0].disposed {
		t.Error("an escalated run must keep its worktree")
	}
	if len(q.releases) != 1 {
		t.Error("the lease must be released even when escalating")
	}
}

func TestRunOnceEscalatesOnAgentFailure(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser"})
	r := &fakeRunner{transcript: "boom\n", err: errors.New("process died")}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))

	result, err := run(s, "01HZ")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Escalated {
		t.Errorf("Applied = %+v, want escalated", result.Applied)
	}
	if !strings.Contains(strings.Join(q.comments, "\n"), "process died") {
		t.Errorf("failure reason should reach the task: %v", q.comments)
	}
}

func TestSessionIsBoundBeforeTheRunFinishes(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Slow work"})
	release := make(chan struct{})
	r := &fakeRunner{transcript: "DONE\n", release: release}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))
	ledger := withLedger(t, s)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := run(s, "01HZ"); err != nil {
			t.Errorf("RunOnce() error = %v", err)
		}
	}()

	// While the agent is still working, the binding must already be
	// readable in the ledger — a session is machine-local and never on the
	// tracker — because a hung run is exactly the one you need to attach to.
	deadline := time.After(2 * time.Second)
	for {
		if rec, err := ledger.Load("01HZ"); err == nil {
			if session, ok := rec.Bindings.Current(wf.KindSession); ok && session.Ref != "" {
				break
			}
		}
		select {
		case <-deadline:
			t.Fatal("session was not bound while the run was in flight")
		case <-time.After(5 * time.Millisecond):
		}
	}

	close(release)
	<-done
}

func TestLeaseHeldDuringRunAndReleasedAfter(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Work"})
	release := make(chan struct{})
	r := &fakeRunner{transcript: "DONE\n", release: release}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = run(s, "01HZ")
	}()

	deadline := time.After(2 * time.Second)
	for {
		if _, held := wf.ParseLease(q.meta("01HZ", wf.LeaseKey)); held {
			break
		}
		select {
		case <-deadline:
			t.Fatal("lease was never written")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if q.meta("01HZ", wf.StateKey) != string(wf.StateRunning) {
		t.Errorf("state during the run = %v, want running", q.meta("01HZ", wf.StateKey))
	}

	close(release)
	<-done

	if _, held := wf.ParseLease(q.meta("01HZ", wf.LeaseKey)); held {
		t.Error("the lease must be gone once the run settles")
	}
}

func TestRunOnceReclaimsStaleLease(t *testing.T) {
	stale := wf.NewLease("dead-worker", time.Minute, time.Now().Add(-time.Hour))
	encoded, err := stale.Encode()
	if err != nil {
		t.Fatal(err)
	}

	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Abandoned", Meta: map[string]any{wf.LeaseKey: encoded}})
	s := newSupervisor(q, &fakeRunner{transcript: "DONE\n"}, &fakeProvider{}, basicFlows(t))

	// A killed wf must not hold its task forever.
	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("RunOnce() error = %v, want the stale lease reclaimed", err)
	}
}

func TestRunOnceRefusesALeasedRef(t *testing.T) {
	lease := wf.NewLease("someone-else", 15*time.Minute, time.Now())
	encoded, _ := lease.Encode()
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Taken", Meta: map[string]any{wf.LeaseKey: encoded}})
	r := &fakeRunner{transcript: "DONE\n"}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))

	if _, err := run(s, "01HZ"); err == nil {
		t.Error("targeting a live-leased task must fail rather than steal it")
	}
	if len(r.prompts) != 0 {
		t.Error("no agent should have been started")
	}
}

func TestRunOnceRequiresARef(t *testing.T) {
	// Every run is a human's choice of task; there is no queue to pick from.
	r := &fakeRunner{transcript: "DONE\n"}
	s := newSupervisor(newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Work"}), r, &fakeProvider{}, basicFlows(t))
	if _, err := run(s, ""); err == nil {
		t.Fatal("RunOnce() with no ref must fail")
	}
	if len(r.prompts) != 0 {
		t.Error("no agent should have been started")
	}
}

func TestRunOnceRunsATaskAwaitingAHuman(t *testing.T) {
	// A task flagged needs-human is waiting for exactly this: a human
	// deciding to run it again. The flag clears once the run starts.
	q := newQueue(wf.Task{
		ID: "01HZ", ShortID: "abc4", Title: "Was stuck",
		Meta: map[string]any{wf.AttentionKey: "needs-human", wf.StateKey: string(wf.StateNeedsHuman)},
	})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))

	result, err := run(s, "01HZ")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Completed {
		t.Errorf("Applied = %+v, want completed", result.Applied)
	}
	if attention := q.meta("01HZ", wf.AttentionKey); attention != nil {
		t.Errorf("attention = %v, want cleared by a completed run", attention)
	}
}

func TestWorkflowSelectionDrivesPromptAndProfile(t *testing.T) {
	flows := loadFlows(t, map[string]string{
		"research.md": "---\nname: research\nprofile: writer\nmodel: claude-opus-5\nworkspace: none\nlabels: research\n---\nResearch {{TASK_TITLE}} thoroughly.\n",
	})

	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Kata internals", Labels: []string{"research"}})
	r := &fakeRunner{transcript: "DOC: notes.md — Notes\nDONE\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, flows)
	s.Config.Profiles = map[string]string{"writer": "/profiles/writer"}
	s.Config.DefaultModel = "claude-sonnet-5"
	s.Config.Vault = "/vault"

	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if !strings.Contains(r.prompts[0], "Research Kata internals thoroughly.") {
		t.Errorf("workflow prompt not used:\n%s", r.prompts[0])
	}
	// The seed preamble rides along, so the agent knows its ref and verbs.
	if !strings.Contains(r.prompts[0], "abc4") || !strings.Contains(r.prompts[0], "DONE") {
		t.Errorf("seed preamble missing:\n%s", r.prompts[0])
	}
	if r.profiles[0] != "/profiles/writer" {
		t.Errorf("profile = %q, want the workflow's", r.profiles[0])
	}
	// The workflow names a model explicitly, so it wins over the configured
	// default rather than being overridden by it.
	if r.models[0] != "claude-opus-5" {
		t.Errorf("model = %q, want the workflow's", r.models[0])
	}
	// workspace: none runs in the vault, not a checkout.
	if len(p.spaces) != 0 {
		t.Error("workspace: none must not create a worktree")
	}
	if r.cwds[0] != "/vault" {
		t.Errorf("cwd = %q, want the vault", r.cwds[0])
	}
}

func TestWorkflowWithNoModelFallsBackToConfigDefault(t *testing.T) {
	flows := loadFlows(t, map[string]string{
		"research.md": "---\nname: research\nworkspace: none\nlabels: research\n---\nResearch {{TASK_TITLE}}\n",
	})

	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Kata internals", Labels: []string{"research"}})
	r := &fakeRunner{transcript: "DONE\n"}
	s := newSupervisor(q, r, &fakeProvider{}, flows)
	s.Config.DefaultModel = "claude-sonnet-5"
	s.Config.Vault = "/vault"

	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if r.models[0] != "claude-sonnet-5" {
		t.Errorf("model = %q, want the configured default", r.models[0])
	}
}

func TestUnknownWorkflowFailsWithoutRunning(t *testing.T) {
	// A human is at the terminal for every run, so a workflow that is not
	// loaded is their error to see, not the task's to carry: no comment,
	// no needs-human flag, no default recipe run in its place.
	flows := loadFlows(t, map[string]string{
		"research.md": "---\nname: research\nlabels: research\n---\nResearch {{TASK_TITLE}}\n",
	})

	q := newQueue(wf.Task{
		ID: "01HZ", ShortID: "abc4", Title: "Work",
		Meta: map[string]any{workflow.MetaKey: "does-not-exist"},
	})
	r := &fakeRunner{transcript: "DONE\n"}
	s := newSupervisor(q, r, &fakeProvider{}, flows)

	if _, err := run(s, "01HZ"); err == nil || !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("RunOnce() error = %v, want it to name the missing workflow", err)
	}
	if len(r.prompts) != 0 {
		t.Error("no agent should have been started")
	}
	if attention := q.meta("01HZ", wf.AttentionKey); attention != nil {
		t.Errorf("attention = %v, want the task left alone", attention)
	}
	if len(q.comments) != 0 {
		t.Errorf("comments = %v, want none", q.comments)
	}
}

func TestNoSelectableWorkflowFails(t *testing.T) {
	// Nothing routes this task and nobody named a recipe: refuse, and say
	// how to fix it, rather than run some default quietly.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Unrouted", Labels: []string{"nothing-routes-this"}})
	r := &fakeRunner{transcript: "DONE\n"}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))

	_, err := run(s, "01HZ")
	if err == nil || !strings.Contains(err.Error(), "--workflow") {
		t.Errorf("RunOnce() error = %v, want a hint to pass --workflow", err)
	}
	if len(r.prompts) != 0 {
		t.Error("no agent should have been started")
	}
}

func TestALaterRunIsHandedTheEarlierRunsBranch(t *testing.T) {
	// A task runs under several workflows over its life, and the fix step
	// has to land on the branch the earlier step pushed a PR from.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Fix the bug"})
	r := &fakeRunner{transcript: "PR: https://a/1 — The fix\nDONE Opened the PR.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, basicFlows(t))
	withLedger(t, s)

	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	if len(p.hints) != 2 || p.hints[0] != "" {
		t.Fatalf("branch hints = %v, want none for the first run", p.hints)
	}
	if p.hints[1] != p.spaces[0].branch {
		t.Errorf("second run was handed %q, want the first run's branch %q", p.hints[1], p.spaces[0].branch)
	}
}
