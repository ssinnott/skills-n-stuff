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

type fakeQueue struct {
	mu       sync.Mutex
	tasks    map[string]*wf.Task
	order    []string
	comments []string
	closed   map[string]wf.CloseResult
	claims   []string
	releases []string
}

func newQueue(tasks ...wf.Task) *fakeQueue {
	q := &fakeQueue{tasks: map[string]*wf.Task{}, closed: map[string]wf.CloseResult{}}
	for i := range tasks {
		t := tasks[i]
		if t.Meta == nil {
			t.Meta = map[string]any{}
		}
		q.tasks[t.ID] = &t
		q.order = append(q.order, t.ID)
	}
	return q
}

func (q *fakeQueue) Name() string { return "fake" }

func (q *fakeQueue) Ready(_ context.Context, limit int) ([]wf.Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []wf.Task
	for _, id := range q.order {
		if _, done := q.closed[id]; done {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, *q.tasks[id])
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

func (q *fakeQueue) Close(_ context.Context, ref string, r wf.CloseResult, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed[ref] = r
	return nil
}

func (q *fakeQueue) Create(_ context.Context, in wf.CreateInput) (wf.Task, error) {
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

func (q *fakeQueue) GetMeta(_ context.Context, ref string) (map[string]any, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := map[string]any{}
	if t, ok := q.tasks[ref]; ok {
		for k, v := range t.Meta {
			out[k] = v
		}
	}
	return out, nil
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
	cwd        string
	release    chan struct{}
	owner      *fakeRunner
}

func (r *fakeRun) SessionID() string   { return "sess-" + r.path }
func (r *fakeRun) SessionPath() string { return r.path }
func (r *fakeRun) Cwd() string         { return r.cwd }
func (r *fakeRun) Abort() error        { return nil }

func (r *fakeRun) Wait(context.Context) (wf.RunResult, error) {
	if r.release != nil {
		<-r.release
	}
	r.owner.finish()
	if r.err != nil {
		return wf.RunResult{TranscriptTail: r.transcript}, r.err
	}
	return wf.RunResult{OK: true, TranscriptTail: r.transcript}, nil
}

type fakeRunner struct {
	mu         sync.Mutex
	transcript string
	err        error
	prompts    []string
	profiles   []string
	cwds       []string
	release    chan struct{}
	concurrent int
	maxSeen    int
}

func (r *fakeRunner) Name() string { return "fake" }

func (r *fakeRunner) Start(_ context.Context, opts wf.RunOptions) (wf.RunHandle, error) {
	r.mu.Lock()
	r.prompts = append(r.prompts, opts.Prompt)
	r.profiles = append(r.profiles, opts.ProfileDir)
	r.cwds = append(r.cwds, opts.Cwd)
	r.concurrent++
	if r.concurrent > r.maxSeen {
		r.maxSeen = r.concurrent
	}
	n := len(r.prompts)
	r.mu.Unlock()

	return &fakeRun{
		transcript: r.transcript,
		err:        r.err,
		path:       fmt.Sprintf("/sessions/run-%d.jsonl", n),
		cwd:        opts.Cwd,
		release:    r.release,
		owner:      r,
	}, nil
}

func (r *fakeRunner) finish() {
	r.mu.Lock()
	r.concurrent--
	r.mu.Unlock()
}

type fakeWorkspace struct {
	path     string
	disposed bool
	kept     bool
}

func (w *fakeWorkspace) Path() string { return w.path }
func (w *fakeWorkspace) Keep()        { w.kept = true }
func (w *fakeWorkspace) Dispose(context.Context) error {
	if w.kept {
		return nil
	}
	w.disposed = true
	return nil
}

type fakeProvider struct {
	mu     sync.Mutex
	spaces []*fakeWorkspace
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Create(_ context.Context, task wf.Task) (wf.Workspace, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	space := &fakeWorkspace{path: "/work/" + task.ShortID}
	p.spaces = append(p.spaces, space)
	return space, nil
}

func newSupervisor(q *fakeQueue, r *fakeRunner, p *fakeProvider, flows *workflow.Set) *Supervisor {
	return &Supervisor{
		Queue:      q,
		Runner:     r,
		Workflows:  flows,
		Config:     &config.Config{Actor: "wf-test", MaxConcurrent: 1, LeaseTTLSeconds: 900},
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

// --- tests ---------------------------------------------------------------

func TestRunOnceClosesOnSuccess(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser"})
	r := &fakeRunner{transcript: "PR: https://a/1 — Add it\nDONE Shipped\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, nil)

	result, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if !result.Applied.Closed {
		t.Errorf("Applied = %+v, want closed", result.Applied)
	}
	if closed, ok := q.closed["01HZ"]; !ok || len(closed.PRs) != 1 {
		t.Errorf("task not closed with its PR evidence: %+v", closed)
	}
	// The lease is always released, and the workspace disposed on success.
	if len(q.releases) != 1 {
		t.Errorf("releases = %v, want exactly one", q.releases)
	}
	if !p.spaces[0].disposed {
		t.Error("a clean run should dispose its worktree")
	}
	if result.Session == "" {
		t.Error("the run's session must be reported")
	}
}

func TestRunOnceEscalatesWithoutDone(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Add the parser"})
	r := &fakeRunner{transcript: "I got confused and stopped.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, nil)

	result, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if !result.Applied.Escalated || result.Applied.Closed {
		t.Errorf("Applied = %+v, want escalated", result.Applied)
	}
	if _, closed := q.closed["01HZ"]; closed {
		t.Error("an incomplete run must not close the task")
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
	s := newSupervisor(q, r, &fakeProvider{}, nil)

	result, err := s.RunOnce(context.Background(), "")
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
	s := newSupervisor(q, r, &fakeProvider{}, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.RunOnce(context.Background(), ""); err != nil {
			t.Errorf("RunOnce() error = %v", err)
		}
	}()

	// While the agent is still working, the binding must already be readable:
	// a hung run is exactly the one you need to attach to.
	deadline := time.After(2 * time.Second)
	for {
		if path, ok := q.meta("01HZ", wf.SessionPathKey).(string); ok && path != "" {
			break
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
	s := newSupervisor(q, r, &fakeProvider{}, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.RunOnce(context.Background(), "")
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

	close(release)
	<-done

	if _, held := wf.ParseLease(q.meta("01HZ", wf.LeaseKey)); held {
		t.Error("the lease must be gone once the run settles")
	}
}

func TestRunOnceSkipsLeasedTasks(t *testing.T) {
	lease := wf.NewLease("someone-else", 15*time.Minute, time.Now())
	encoded, err := lease.Encode()
	if err != nil {
		t.Fatal(err)
	}

	held := wf.Task{ID: "01A", ShortID: "aaa1", Title: "Taken", Meta: map[string]any{wf.LeaseKey: encoded}}
	free := wf.Task{ID: "01B", ShortID: "bbb2", Title: "Available"}
	q := newQueue(held, free)
	r := &fakeRunner{transcript: "DONE\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)

	result, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Task.ID != "01B" {
		t.Errorf("dispatched %s, want the unleased task", result.Task.ID)
	}
}

func TestRunOnceReclaimsStaleLease(t *testing.T) {
	stale := wf.NewLease("dead-worker", time.Minute, time.Now().Add(-time.Hour))
	encoded, err := stale.Encode()
	if err != nil {
		t.Fatal(err)
	}

	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Abandoned", Meta: map[string]any{wf.LeaseKey: encoded}})
	s := newSupervisor(q, &fakeRunner{transcript: "DONE\n"}, &fakeProvider{}, nil)

	// A killed worker must not hold its task forever.
	if _, err := s.RunOnce(context.Background(), ""); err != nil {
		t.Fatalf("RunOnce() error = %v, want the stale lease reclaimed", err)
	}
}

func TestRunOnceRefusesALeasedRef(t *testing.T) {
	lease := wf.NewLease("someone-else", 15*time.Minute, time.Now())
	encoded, _ := lease.Encode()
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Taken", Meta: map[string]any{wf.LeaseKey: encoded}})
	s := newSupervisor(q, &fakeRunner{transcript: "DONE\n"}, &fakeProvider{}, nil)

	if _, err := s.RunOnce(context.Background(), "01HZ"); err == nil {
		t.Error("targeting a live-leased task must fail rather than steal it")
	}
}

func TestRunOnceNothingReady(t *testing.T) {
	s := newSupervisor(newQueue(), &fakeRunner{}, &fakeProvider{}, nil)
	if _, err := s.RunOnce(context.Background(), ""); !errors.Is(err, ErrNothingReady) {
		t.Errorf("RunOnce() error = %v, want ErrNothingReady", err)
	}
}

func TestWorkflowSelectionDrivesPromptAndProfile(t *testing.T) {
	flows := loadFlows(t, map[string]string{
		"research.md": "---\nname: research\nprofile: writer\nworkspace: none\nlabels: research\n---\nResearch {{TASK_TITLE}} thoroughly.\n",
	})

	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Kata internals", Labels: []string{"research"}})
	r := &fakeRunner{transcript: "DONE\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, flows)
	s.Config.Profiles = map[string]string{"writer": "/profiles/writer"}
	s.Config.Vault = "/vault"

	if _, err := s.RunOnce(context.Background(), ""); err != nil {
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
	// workspace: none runs in the vault, not a checkout.
	if len(p.spaces) != 0 {
		t.Error("workspace: none must not create a worktree")
	}
	if r.cwds[0] != "/vault" {
		t.Errorf("cwd = %q, want the vault", r.cwds[0])
	}
}

func TestUnknownWorkflowEscalatesRatherThanRunning(t *testing.T) {
	flows := loadFlows(t, map[string]string{
		"research.md": "---\nname: research\nlabels: research\n---\nResearch {{TASK_TITLE}}\n",
	})

	q := newQueue(wf.Task{
		ID: "01HZ", ShortID: "abc4", Title: "Work",
		Meta: map[string]any{workflow.MetaKey: "does-not-exist"},
	})
	r := &fakeRunner{transcript: "DONE\n"}
	s := newSupervisor(q, r, &fakeProvider{}, flows)

	if _, err := s.RunOnce(context.Background(), ""); err == nil {
		t.Error("a task naming an unknown workflow must not run under a default")
	}
	if len(r.prompts) != 0 {
		t.Error("no agent should have been started")
	}
	if q.meta("01HZ", wf.AttentionKey) != "needs-human" {
		t.Error("the task should be flagged for a human")
	}
}

func TestRunRespectsConcurrencyCap(t *testing.T) {
	var tasks []wf.Task
	for i := 0; i < 6; i++ {
		tasks = append(tasks, wf.Task{
			ID:      fmt.Sprintf("id-%d", i),
			ShortID: fmt.Sprintf("t%d", i),
			Title:   fmt.Sprintf("Task %d", i),
		})
	}

	q := newQueue(tasks...)
	r := &fakeRunner{transcript: "PR: https://a/1 — Did it\nDONE Landed the change and verified it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)

	results, err := s.Run(context.Background(), 2)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 6 {
		t.Errorf("dispatched %d tasks, want 6", len(results))
	}
	if r.maxSeen > 2 {
		t.Errorf("ran %d agents at once, want at most 2", r.maxSeen)
	}
	for _, task := range tasks {
		if _, ok := q.closed[task.ID]; !ok {
			t.Errorf("%s was never closed", task.ShortID)
		}
	}
}

func TestRunStopsWhenQueueDrains(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Only task"})
	s := newSupervisor(q, &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it.\n"}, &fakeProvider{}, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.Run(context.Background(), 2); err != nil {
			t.Errorf("Run() error = %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return once the queue drained")
	}
}

func TestRunDoesNotRetryEscalatedTasks(t *testing.T) {
	// An escalated task stays open and its lease is released, so without an
	// attempted-set the drain loop picks it straight back up — forever.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Stuck work"})
	r := &fakeRunner{transcript: "I got confused.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.Run(context.Background(), 1); err != nil {
			t.Errorf("Run() error = %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() spun on an escalated task instead of moving on")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.prompts) != 1 {
		t.Errorf("the task was dispatched %d times, want once", len(r.prompts))
	}
}

func TestRunSkipsTasksAwaitingAHuman(t *testing.T) {
	// A task already flagged for a human is not ours to retry.
	q := newQueue(wf.Task{
		ID: "01HZ", ShortID: "abc4", Title: "Waiting on a person",
		Meta: map[string]any{wf.AttentionKey: "needs-human"},
	})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)

	results, err := s.Run(context.Background(), 1)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 0 || len(r.prompts) != 0 {
		t.Errorf("dispatched a task awaiting a human: %d results, %d runs", len(results), len(r.prompts))
	}
}

func TestPickPrefersHigherPriority(t *testing.T) {
	// kata returns ready newest-first; which ready task to run is wf's call.
	q := newQueue(
		wf.Task{ID: "low", ShortID: "low1", Title: "Low priority", Priority: 4},
		wf.Task{ID: "high", ShortID: "hi1", Title: "High priority", Priority: 0},
		wf.Task{ID: "mid", ShortID: "mid1", Title: "Middling", Priority: 2},
	)
	s := newSupervisor(q, &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it.\n"}, &fakeProvider{}, nil)

	result, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Task.ID != "high" {
		t.Errorf("dispatched %q, want the priority-0 task", result.Task.ID)
	}
}
