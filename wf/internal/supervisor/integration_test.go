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
		queue: kata.New(kata.Options{Bin: kataBin, Cwd: wsDir, Project: project, Actor: "wf-test"}),
		cfg: &config.Config{
			Actor:           "wf-test",
			Vault:           vault,
			WorktreeRoot:    filepath.Join(t.TempDir(), "worktrees"),
			SessionRoot:     filepath.Join(t.TempDir(), "sessions"),
			MaxConcurrent:   2,
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
		Workspaces: func(w workflow.Workflow) wf.WorkspaceProvider {
			return &workspace.Provider{Repo: h.repo, Root: h.cfg.WorktreeRoot}
		},
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

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Add the parser", Priority: 1})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := h.supervisor().RunOnce(ctx, "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Closed {
		t.Fatalf("Applied = %+v, want closed", result.Applied)
	}

	// The issue is closed in kata itself, not just in wf's head.
	ready, err := h.queue.Ready(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range ready {
		if r.ID == created.ID {
			t.Error("the issue is still ready after a successful run")
		}
	}

	// The session binding survives in kata, so attach works afterwards.
	meta, err := h.queue.GetMeta(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding, ok := wf.BindingFromMeta(meta)
	if !ok {
		t.Fatal("no session bound to the closed task")
	}
	if binding.Path == "" || !strings.HasSuffix(binding.Path, ".jsonl") {
		t.Errorf("session path = %q", binding.Path)
	}
	if len(wf.HistoryFromMeta(meta)) != 1 {
		t.Errorf("history = %v, want one run", wf.HistoryFromMeta(meta))
	}

	// The lease and the owner are both cleared once the run settles.
	if _, held := wf.ParseLease(meta[wf.LeaseKey]); held {
		t.Error("the lease outlived the run")
	}
	task, err := h.queue.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
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

	created, err := h.queue.Create(ctx, wf.CreateInput{
		Title:  "How does kata store issues",
		Labels: []string{"research"},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.supervisor().RunOnce(ctx, "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Closed {
		t.Fatalf("Applied = %+v, want closed", result.Applied)
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

	result, err := h.supervisor().RunOnce(ctx, "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Escalated || result.Applied.Closed {
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

	if _, err := h.queue.Create(ctx, wf.CreateInput{Title: "Original work"}); err != nil {
		t.Fatal(err)
	}

	result, err := h.supervisor().RunOnce(ctx, "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Closed {
		t.Fatalf("Applied = %+v, want closed", result.Applied)
	}
	if len(result.Applied.Created) != 1 {
		t.Fatalf("Created = %v, want one follow-on task", result.Applied.Created)
	}

	// The follow-on is real, open, and carries its origin.
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
	if followOn.Meta["wf.origin"] == nil {
		t.Errorf("follow-on lacks its origin: %v", followOn.Meta)
	}
}

const bareDoneAgent = `#!/bin/sh
echo "DONE"
`

func TestE2EEvidenceFreeDoneEscalates(t *testing.T) {
	// A completion with nothing to show for it is not a completion. kata
	// refuses such a close outright, and wf agrees: the alternative would be
	// manufacturing evidence, which is the one thing that would make a
	// closed task meaningless.
	h := newHarness(t, bareDoneAgent, nil)
	ctx := context.Background()

	created, err := h.queue.Create(ctx, wf.CreateInput{Title: "Unevidenced work"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := h.supervisor().RunOnce(ctx, "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if result.Applied.Closed {
		t.Error("a DONE with no evidence must not close the task")
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

func TestE2ETerseDoneWithEvidenceCloses(t *testing.T) {
	// kata also refuses a close message under 40 characters. An agent that
	// signs off with a bare DONE but did real work must still close, with
	// the substance composed from what wf actually knows.
	h := newHarness(t, terseAgent, nil)
	ctx := context.Background()

	if _, err := h.queue.Create(ctx, wf.CreateInput{Title: "Terse but evidenced work"}); err != nil {
		t.Fatal(err)
	}

	result, err := h.supervisor().RunOnce(ctx, "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Closed {
		t.Errorf("Applied = %+v, want closed despite the terse message", result.Applied)
	}
}

func TestE2EConcurrentRunsDoNotCollide(t *testing.T) {
	h := newHarness(t, shipAgent, nil)
	ctx := context.Background()

	for _, title := range []string{"First task", "Second task", "Third task", "Fourth task"} {
		if _, err := h.queue.Create(ctx, wf.CreateInput{Title: title}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := h.supervisor().Run(ctx, 2)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("dispatched %d tasks, want 4", len(results))
	}

	// Every task closed exactly once, and each got its own worktree and
	// session — a shared checkout would have shown up as a git failure.
	ready, err := h.queue.Ready(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Errorf("ready = %+v, want an empty queue", ready)
	}

	seen := map[string]bool{}
	for _, r := range results {
		if seen[r.Session] {
			t.Errorf("two tasks shared session %s", r.Session)
		}
		seen[r.Session] = true
	}
}
