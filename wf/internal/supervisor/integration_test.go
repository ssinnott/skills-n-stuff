package supervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/kata"
	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
	"github.com/ssinnott/skills-n-stuff/wf/internal/runner"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workspace"
)

// End-to-end tests: a real kata daemon, a real git worktree, and a stub
// agent standing in for pi.
//
// The agent is a shell script rather than pi itself because pi needs a
// provider key and would make the test non-deterministic. Everything else is
// real, including every tracker write — which is the point: these are the
// only tests that prove the pieces fit together rather than that each piece
// matches its own mock.

type harness struct {
	queue     *kata.Backend
	cfg       *config.Config
	vault     string
	repo      string
	agent     string
	workflows *workflow.Set
	// ledger is wf's own record: the runs and the typed bindings each run
	// produced. Real rather than stubbed, because what lands on disk is the
	// thing worth checking.
	ledger *store.FileStore
}

func requireTools(t *testing.T) (kataBin string) {
	t.Helper()
	if bin := os.Getenv("KATA_BIN"); bin != "" {
		return bin
	}
	bin, err := exec.LookPath("kata")
	if err != nil {
		t.Skip("kata not installed; set KATA_BIN or put kata on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no shell available")
	}
	return bin
}

// newHarness wires the real components against throwaway directories.
func newHarness(t *testing.T, agentScript string, workflows map[string]string) *harness {
	t.Helper()
	kataBin := requireTools(t)

	home := t.TempDir()
	wsDir := t.TempDir()
	t.Setenv("KATA_HOME", home)
	t.Setenv("KATA_AUTHOR", "wf-test")

	project := "wfe2e" + strings.ToLower(filepath.Base(wsDir))
	initCmd := exec.Command(kataBin, "init", "--project", project)
	initCmd.Dir = wsDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Skipf("kata init failed: %v: %s", err, out)
	}
	t.Cleanup(func() {
		stop := exec.Command(kataBin, "daemon", "stop")
		stop.Dir = wsDir
		_ = stop.Run()
	})

	repo := initRepo(t)
	vault := t.TempDir()

	agent := filepath.Join(t.TempDir(), "stub-agent")
	if err := os.WriteFile(agent, []byte(agentScript), 0o755); err != nil {
		t.Fatal(err)
	}

	flowDir := t.TempDir()
	if workflows == nil {
		workflows = map[string]string{}
	}
	workflows["work.md"] = "---\nname: work\n---\n{{TASK_TITLE}}\n\n{{TASK_BODY}}\n"
	for name, body := range workflows {
		if err := os.WriteFile(filepath.Join(flowDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	flows, err := workflow.Load(flowDir)
	if err != nil {
		t.Fatal(err)
	}

	return &harness{
		ledger: store.New(t.TempDir()),
		queue:  kata.New(kata.Options{Bin: kataBin, Cwd: wsDir, Project: project, Actor: "wf-test"}),
		cfg: &config.Config{
			Actor:           "wf-test",
			Vault:           vault,
			WorktreeRoot:    filepath.Join(t.TempDir(), "worktrees"),
			SessionRoot:     filepath.Join(t.TempDir(), "sessions"),
			LeaseTTLSeconds: 900,
		},
		vault:     vault,
		repo:      repo,
		agent:     agent,
		workflows: flows,
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "wf@example.com"},
		{"config", "user.name", "wf"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func (h *harness) supervisor() *Supervisor {
	return &Supervisor{
		Queue:     h.queue,
		Runner:    &runner.Pi{Bin: h.agent, SessionRoot: h.cfg.SessionRoot},
		Workflows: h.workflows,
		Config:    h.cfg,
		Store:     h.ledger,
		Workspaces: func(w workflow.Workflow) wf.WorkspaceProvider {
			return &workspace.Provider{Repo: h.repo, Root: h.cfg.WorktreeRoot}
		},
	}
}

// run dispatches one task by ref under a named workflow: every run is a
// human's choice of task and recipe.
func (h *harness) run(ctx context.Context, ref, flow string) (Result, error) {
	return h.supervisor().RunOnce(ctx, ref, Dispatch{Workflow: flow})
}

// record reads the task's ledger entry, keyed by the tracker's own id.
func (h *harness) record(t *testing.T, id string) wf.Record {
	t.Helper()
	rec, err := h.ledger.Load(id)
	if err != nil {
		t.Fatalf("no ledger record for %s: %v", id, err)
	}
	return rec
}

// setAgent rewrites the stub agent in place, so a second dispatch against
// one task can behave differently from the first.
func (h *harness) setAgent(t *testing.T, script string) {
	t.Helper()
	if err := os.WriteFile(h.agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// --- tests ---------------------------------------------------------------

const shipAgent = `#!/bin/sh
echo "working..."
echo "PR: https://example.com/pr/1 — Add the parser"
echo "DONE Implemented the tolerant separator parser and covered it with tests."
`

func TestE2EClosesRealIssue(t *testing.T) {
	h := newHarness(t, shipAgent, nil)
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Add the parser"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := h.run(ctx, created.ID, "work")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Completed {
		t.Fatalf("Applied = %+v, want completed", result.Applied)
	}

	// The run completed; the task is still open in kata, waiting for a
	// human's next move, and now carries the PR for its eventual close.
	task, err := h.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Meta[wf.StateKey] != string(wf.StateReview) {
		t.Errorf("state = %v, want review", task.Meta[wf.StateKey])
	}
	if prs := wf.PRsFromMeta(task.Meta); len(prs) != 1 {
		t.Errorf("PRs recorded = %v, want the run's PR", prs)
	}

	// And `wf close` closes it in kata itself, with that evidence.
	if _, err := wf.Close(ctx, h.queue, task, ""); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	ready, err := h.queue.Ready(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ready {
		if r.ID == created.ID {
			t.Error("the issue is still ready after a close")
		}
	}

	// The session binding survives in wf's own ledger — never in kata — so
	// attach works afterwards.
	rec := h.record(t, created.ID)
	binding, ok := rec.Bindings.Current(wf.KindSession)
	if !ok {
		t.Fatal("no session bound to the closed task")
	}
	if binding.Ref == "" || !strings.HasSuffix(binding.Ref, ".jsonl") {
		t.Errorf("session path = %q", binding.Ref)
	}
	if len(rec.Runs) != 1 {
		t.Errorf("runs = %v, want one run", rec.Runs)
	}

	// The lease and the owner are both cleared once the run settles.
	meta, err := h.queue.GetMeta(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := wf.ParseLease(meta[wf.LeaseKey]); held {
		t.Error("the lease outlived the run")
	}
	if task.Owner != "" {
		t.Errorf("Owner = %q after the run, want empty", task.Owner)
	}
}

const researchAgent = `#!/bin/sh
cat > findings.md <<'EOF'
# Findings

kata stores issues in SQLite under KATA_HOME.
EOF
echo "DOC: findings.md — What kata stores"
echo "DONE Investigated kata's storage model and wrote the findings up."
`

func TestE2EBindsArtifactIntoVault(t *testing.T) {
	h := newHarness(t, researchAgent, map[string]string{
		"research.md": "---\nname: research\nlabels: research\nbind-docs: true\nvault-dir: Research\n---\nResearch {{TASK_TITLE}}\n",
	})
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "How does kata store issues"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.run(ctx, created.ID, "research")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Completed {
		t.Fatalf("Applied = %+v, want completed", result.Applied)
	}
	if len(result.Applied.Bound) != 1 {
		t.Fatalf("Bound = %+v, want one document", result.Applied.Bound)
	}

	// The document landed in the vault, under the workflow's directory.
	vaultPath := filepath.Join(h.vault, result.Applied.Bound[0].VaultPath)
	body, err := os.ReadFile(vaultPath)
	if err != nil {
		t.Fatalf("produced document is not in the vault: %v", err)
	}
	if !strings.Contains(string(body), "kata stores issues in SQLite") {
		t.Error("document content was lost in the move")
	}

	// Both halves of the binding exist: note → task, and task → note.
	if got := note.GetField(string(body), note.IssueKey); got != created.ID {
		t.Errorf("note frontmatter = %q, want the task ULID %q", got, created.ID)
	}
	meta, err := h.queue.GetMeta(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta[wf.DocKey] != result.Applied.Bound[0].VaultPath {
		t.Errorf("task metadata = %v, want the vault path", meta[wf.DocKey])
	}
	if docs := wf.DocsFromMeta(meta); len(docs) != 1 || docs[0] != result.Applied.Bound[0].VaultPath {
		t.Errorf("documents recorded = %v, want the bound one", docs)
	}

	// A workflow with no workspace still ran; nothing was left behind.
	entries, err := os.ReadDir(h.cfg.WorktreeRoot)
	if err == nil && len(entries) > 0 {
		t.Errorf("worktrees left on disk: %v", entries)
	}
}

const stuckAgent = `#!/bin/sh
echo "I need a human decision about which schema to use."
exit 0
`

func TestE2EEscalatesAndKeepsWorktree(t *testing.T) {
	h := newHarness(t, stuckAgent, nil)
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Ambiguous work"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.run(ctx, created.ID, "work")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Escalated || result.Applied.Completed {
		t.Fatalf("Applied = %+v, want escalated", result.Applied)
	}

	// It stays open and shows up in kata's own escalation query.
	escalations, err := h.queue.Escalations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(escalations) != 1 || escalations[0].ID != created.ID {
		t.Errorf("Escalations() = %+v, want the stuck task", escalations)
	}

	// The worktree survives, because it is the evidence a human needs.
	entries, err := os.ReadDir(h.cfg.WorktreeRoot)
	if err != nil {
		t.Fatalf("worktree root missing: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("worktrees on disk = %d, want the escalated one kept", len(entries))
	}

	// And the lease is released even though the run failed, so the task is
	// not stranded.
	meta, _ := h.queue.GetMeta(ctx, created.ID)
	if _, held := wf.ParseLease(meta[wf.LeaseKey]); held {
		t.Error("an escalated task must not keep its lease")
	}
}

const followOnAgent = `#!/bin/sh
echo "PR: https://example.com/pr/7 — Land the change"
echo "ISSUE: https://example.com/i/9 — Found a flaky test"
echo "NEXT: fix the flaky test in the parser suite"
echo "DONE Landed the change; filed the flake separately rather than widening this."
`

func TestE2ESpawnsLinkedFollowOn(t *testing.T) {
	h := newHarness(t, followOnAgent, nil)
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Original work"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.run(ctx, created.ID, "work")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Completed {
		t.Fatalf("Applied = %+v, want completed", result.Applied)
	}
	if len(result.Applied.Created) != 1 {
		t.Fatalf("Created = %v, want one follow-on task", result.Applied.Created)
	}

	// The follow-on is real and open.
	ready, err := h.queue.Ready(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	var followOn *wf.Task
	for i := range ready {
		if ready[i].Title == "fix the flaky test in the parser suite" {
			followOn = &ready[i]
		}
	}
	if followOn == nil {
		t.Fatalf("follow-on task is not in the queue: %+v", ready)
	}
}

const bareDoneAgent = `#!/bin/sh
echo "DONE"
`

func TestE2EOutputFreeDoneEscalates(t *testing.T) {
	// A run with nothing to show for it is not complete: the alternative
	// would be manufacturing output, which is the one thing that would make
	// a completed run meaningless.
	h := newHarness(t, bareDoneAgent, nil)
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Unevidenced work"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.run(ctx, created.ID, "work")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Applied.Completed {
		t.Error("a DONE with nothing to show must not complete")
	}
	if !result.Applied.Escalated {
		t.Errorf("Applied = %+v, want escalated", result.Applied)
	}

	escalations, err := h.queue.Escalations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(escalations) != 1 || escalations[0].ID != created.ID {
		t.Errorf("Escalations() = %+v, want the unevidenced task", escalations)
	}
}

const terseAgent = `#!/bin/sh
echo "PR: https://example.com/pr/3 — The change"
echo "DONE"
`

func TestE2ETerseDoneStillCloses(t *testing.T) {
	// kata refuses a close message under 40 characters. A task whose run
	// signed off with a bare DONE but did real work must still close, with
	// the substance composed from what wf actually knows.
	h := newHarness(t, terseAgent, nil)
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Terse but evidenced work"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.run(ctx, created.ID, "work")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Completed {
		t.Fatalf("Applied = %+v, want completed despite the terse message", result.Applied)
	}
	task, err := h.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wf.Close(ctx, h.queue, task, ""); err != nil {
		t.Errorf("Close() error = %v, want a message composed from the evidence", err)
	}
}

// --- the ledger, end to end ---------------------------------------------

const diesAgent = `#!/bin/sh
echo "starting on the parser"
exit 3
`

func TestE2ERunSurvivesACrashedAgent(t *testing.T) {
	// A run is recorded at spawn, not at completion, for the same reason the
	// session binding is: this agent produced nothing at all, so the record
	// is the only thing that knows the run happened — and it is exactly the
	// kind of run a human goes looking for.
	h := newHarness(t, diesAgent, nil)
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Work that dies"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.run(ctx, created.ID, "work")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Escalated {
		t.Fatalf("Applied = %+v, want escalated", result.Applied)
	}

	rec := h.record(t, created.ID)
	if len(rec.Runs) != 1 {
		t.Fatalf("runs = %+v, want the dead run recorded", rec.Runs)
	}
	run := rec.Runs[0]
	if !run.Done() || run.Outcome != wf.SessionEscalated {
		t.Errorf("run = %+v, want settled as escalated", run)
	}

	// Its session file is on disk and bound, so `wf attach` still works.
	session, ok := rec.Bindings.Current(wf.KindSession)
	if !ok {
		t.Fatal("the dead run left no session binding")
	}
	if session.Via != run.ID {
		t.Errorf("session Via = %q, want the run that spawned it", session.Via)
	}
	// The path, not the file: wf mints it and hands it to the runner, and
	// the stub agent standing in for pi never writes one. That the binding
	// exists at all is the property under test — it was written at spawn,
	// before this agent had produced anything, and it survived the agent
	// producing nothing.
	if !strings.HasSuffix(session.Ref, ".jsonl") {
		t.Errorf("session ref = %q, want a minted session path", session.Ref)
	}

	// And its checkout is live, recorded with the branch it is actually on.
	space, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok || space.State != wf.BindingLive {
		t.Fatalf("workspace binding = %+v, want it live", space)
	}
	if _, err := os.Stat(space.Ref); err != nil {
		t.Errorf("kept checkout is not on disk: %v", err)
	}
	branch := space.Get(wf.MetaBranch)
	if branch == "" {
		t.Fatal("the workspace binding did not record its branch")
	}
	if !workspace.BranchExists(ctx, h.repo, branch) {
		t.Errorf("recorded branch %q is not a ref in the repo", branch)
	}
}

func TestE2ERerunContinuesInTheKeptCheckout(t *testing.T) {
	// Against real kata and real git: an escalated run keeps its checkout,
	// and the re-run a human kicks off continues in it, completes, and
	// disposes of it — leaving the branch for the task's next workflow.
	h := newHarness(t, stuckAgent, nil)
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Ambiguous work"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := h.run(ctx, created.ID, "work")
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if !first.Applied.Escalated {
		t.Fatalf("Applied = %+v, want the first run escalated", first.Applied)
	}

	kept, ok := h.record(t, created.ID).Bindings.Current(wf.KindWorkspace)
	if !ok {
		t.Fatal("the escalated run recorded no checkout")
	}

	h.setAgent(t, shipAgent)
	second, err := h.run(ctx, created.ID, "work")
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if !second.Applied.Completed {
		t.Fatalf("Applied = %+v, want the re-run completed", second.Applied)
	}

	rec := h.record(t, created.ID)
	if len(rec.Runs) != 2 {
		t.Fatalf("runs = %+v, want two", rec.Runs)
	}
	spaces := rec.Bindings.ByKind(wf.KindWorkspace)
	if len(spaces) != 1 || spaces[0].Ref != kept.Ref {
		t.Fatalf("workspace bindings = %+v, want the one continued checkout %q", spaces, kept.Ref)
	}
	if spaces[0].Via != rec.Runs[1].ID || spaces[0].State != wf.BindingDisposed {
		t.Errorf("checkout = %+v, want it disposed by the re-run", spaces[0])
	}
	if _, err := os.Stat(kept.Ref); !os.IsNotExist(err) {
		t.Errorf("a completed run should have removed its checkout: %v", err)
	}
	if branch := kept.Get(wf.MetaBranch); branch == "" {
		t.Error("the checkout recorded no branch")
	} else if !workspace.BranchExists(ctx, h.repo, branch) {
		t.Errorf("branch %q was deleted with the checkout", branch)
	}
}

const producingAgent = `#!/bin/sh
cat > plan.md <<'EOF'
# Plan

Split the separator handling out first.
EOF
echo "REPO: /code/app"
echo "PR: https://example.com/pr/42 — Add the parser"
echo "ISSUE: https://example.com/i/7 — Separator handling is untested"
echo "DOC: plan.md — The parser plan"
echo "DONE Landed the parser, wrote the plan up, and filed the gap separately."
`

func TestE2EBindingsCarryTheirRunID(t *testing.T) {
	h := newHarness(t, producingAgent, map[string]string{
		"plan.md": "---\nname: plan\nlabels: plan\nbind-docs: true\nvault-dir: Research\n---\nPlan {{TASK_TITLE}}\n",
	})
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Add the parser"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.run(ctx, created.ID, "plan")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Completed {
		t.Fatalf("Applied = %+v, want completed", result.Applied)
	}

	// Only what is machine-local lands on the ledger: a workspace and a
	// session. PRs, issues, documents and the repo are shareable and live
	// only on the tracker, checked below.
	rec := h.record(t, created.ID)
	runID := rec.Runs[0].ID
	produced := rec.Bindings.From(runID)
	byKind := map[wf.Kind]wf.Binding{}
	for _, b := range produced {
		byKind[b.Kind] = b
	}
	for _, kind := range []wf.Kind{wf.KindWorkspace, wf.KindSession} {
		if _, ok := byKind[kind]; !ok {
			t.Errorf("no %s binding tagged with run %s: %+v", kind, runID, produced)
		}
	}
	for _, kind := range []wf.Kind{wf.KindPR, wf.KindIssue, wf.KindDoc, wf.KindRepo} {
		if _, ok := byKind[kind]; ok {
			t.Errorf("%s is a shareable fact and must not be in the ledger: %+v", kind, byKind[kind])
		}
	}
	if _, ok := rec.Bindings.Current(wf.KindRepo); ok {
		t.Error("the repo is a shareable fact and must not be in the ledger at all")
	}

	// Publication to the tracker is where every one of those facts lives,
	// and LoadBindings reads them back from there.
	meta, err := h.queue.GetMeta(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta[wf.RepoKey] != "/code/app" {
		t.Errorf("tracker repo metadata = %v", meta[wf.RepoKey])
	}
	if len(wf.PRsFromMeta(meta)) != 1 {
		t.Errorf("tracker PR metadata = %v", meta[wf.PRsKey])
	}
	if len(wf.IssuesFromMeta(meta)) != 1 {
		t.Errorf("tracker issue metadata = %v", meta[wf.IssuesKey])
	}
	if meta[wf.DocKey] == nil {
		t.Error("tracker doc metadata was dropped")
	}

	task, err := h.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	bindings := wf.LoadBindings(task)
	if pr, ok := bindings.Current(wf.KindPR); !ok || pr.Ref != "https://example.com/pr/42" {
		t.Errorf("LoadBindings PR = %+v", pr)
	}
	// The document binding names where the file actually ended up, not
	// where the agent wrote it inside a checkout that is now gone.
	doc, ok := bindings.Current(wf.KindDoc)
	if !ok {
		t.Fatalf("LoadBindings doc = %+v, want the bound document", doc)
	}
	if _, err := os.Stat(filepath.Join(h.vault, doc.Ref)); err != nil {
		t.Errorf("doc binding does not point at the bound document: %v", err)
	}
	if repo, ok := bindings.Current(wf.KindRepo); !ok || repo.Ref != "/code/app" {
		t.Errorf("LoadBindings repo = %+v", repo)
	}
}
