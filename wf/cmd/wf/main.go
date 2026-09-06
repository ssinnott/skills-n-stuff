// Command wf runs agent work off a queue.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/kata"
	"github.com/ssinnott/skills-n-stuff/wf/internal/runner"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/supervisor"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workspace"
)

const usage = `wf — workflow CLI over pluggable queues

  wf ready [--limit N]         actionable work, top of queue first
  wf show <ref>                one task: its runs and what each produced
  wf escalations               tasks flagged needs-human
  wf workflows                 canned workflows loaded from the workflow dir
  wf run [--once] [--ref R]    dispatch work to agents
         [--max N] [--repo P]
         [--workflow W]
  wf attach <ref>              open the task's pi session
  wf bind <ref> <note.md>      bind a task to an Obsidian note, both ways
  wf ui <ref>                  print the web UI deep link for a task
  wf review <ref>              resolve the task's diff and open it in difit
  wf review <ref> --stop       stop the running viewer
  wf review comment <ref>      read a pasted review prompt from stdin
  wf gc [--delete]              report ledger bindings whose referent is gone; --delete drops dead records

A <ref> is anything ` + "`kata show`" + ` accepts: the issue's ULID or its short id.

Add --json to ready, show, escalations, workflows, run and review for
machine-readable output; that is the protocol both the pi extension and the
Obsidian plugin speak.

Config: ~/.wf/config.json (override with --config). KATA_BIN, PI_BIN and
DIFIT_BIN override binaries that are off PATH.`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code, err := run(ctx, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

type app struct {
	cfg   *config.Config
	queue *kata.Backend
	// ledger is wf's own task record. It lives beside the config, so
	// --config moves it exactly as it moves review.json, and opening it
	// reads nothing — a ledger that does not exist yet is an empty one.
	ledger    store.Store
	workflows *workflow.Set
}

func newApp(args []string) (*app, error) {
	cfg, err := config.Load(flagValue(args, "--config"))
	if err != nil {
		return nil, err
	}
	if v := os.Getenv("KATA_BIN"); v != "" {
		cfg.KataBin = v
	}
	if v := os.Getenv("PI_BIN"); v != "" {
		cfg.PiBin = v
	}
	if v := os.Getenv("DIFIT_BIN"); v != "" {
		cfg.DifitCommand = v
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve working directory: %w", err)
	}

	flows, err := workflow.Load(cfg.WorkflowDir)
	if err != nil {
		return nil, err
	}

	return &app{
		cfg:       cfg,
		queue:     kata.New(kata.Options{Bin: cfg.KataBin, Cwd: cwd, Actor: cfg.Actor}),
		ledger:    store.New(store.Root(cfg.Path)),
		workflows: flows,
	}, nil
}

func run(ctx context.Context, argv []string) (int, error) {
	if len(argv) == 0 || argv[0] == "help" || argv[0] == "--help" || argv[0] == "-h" {
		fmt.Println(usage)
		return 0, nil
	}

	cmd, rest := argv[0], argv[1:]
	a, err := newApp(rest)
	if err != nil {
		return 1, err
	}

	switch cmd {
	case "ready":
		return a.cmdReady(ctx, rest)
	case "show":
		return a.cmdShow(ctx, rest)
	case "escalations":
		return a.cmdEscalations(ctx, rest)
	case "workflows":
		return a.cmdWorkflows(rest)
	case "run":
		return a.cmdRun(ctx, rest)
	case "attach":
		return a.cmdAttach(ctx, rest)
	case "bind":
		return a.cmdBind(ctx, rest)
	case "ui":
		return a.cmdUI(ctx, rest)
	case "review":
		return a.cmdReview(ctx, rest)
	case "gc":
		return a.cmdGC(ctx, rest)
	case "migrate-ledger":
		// Hidden: a one-off migration, not part of the command surface.
		// See cmd/wf/migrate.go.
		return a.cmdMigrateLedger(ctx, rest)
	default:
		return 1, fmt.Errorf("unknown command: %s\n\n%s", cmd, usage)
	}
}

func (a *app) cmdReady(ctx context.Context, args []string) (int, error) {
	limit := 20
	if v := flagValue(args, "--limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 1, fmt.Errorf("--limit: %w", err)
		}
		limit = n
	}

	tasks, err := a.queue.Ready(ctx, limit)
	if err != nil {
		return 1, err
	}
	if hasFlag(args, "--json") {
		return 0, emit("tasks", a.tasksToJSON(tasks))
	}
	return a.printTasks(tasks, "nothing ready"), nil
}

func (a *app) cmdEscalations(ctx context.Context, args []string) (int, error) {
	tasks, err := a.queue.Escalations(ctx)
	if err != nil {
		return 1, err
	}
	if hasFlag(args, "--json") {
		return 0, emit("tasks", a.tasksToJSON(tasks))
	}
	return a.printTasks(tasks, "no escalations"), nil
}

func (a *app) printTasks(tasks []wf.Task, empty string) int {
	if len(tasks) == 0 {
		fmt.Println(empty)
		return 0
	}
	for _, t := range tasks {
		fmt.Println(a.summary(t))
	}
	return 0
}

func (a *app) cmdWorkflows(args []string) (int, error) {
	flows := a.workflows.All()
	if hasFlag(args, "--json") {
		return 0, emit("workflows", workflowsToJSON(flows))
	}
	if len(flows) == 0 {
		fmt.Printf("no workflows in %s\n", a.cfg.WorkflowDir)
		return 0, nil
	}
	for _, w := range flows {
		labels := ""
		if len(w.Labels) > 0 {
			labels = "  labels: " + strings.Join(w.Labels, ", ")
		}
		fmt.Printf("%-16s %s%s\n", w.Name, w.Description, labels)
	}
	return 0, nil
}

func (a *app) cmdRun(ctx context.Context, args []string) (int, error) {
	repo := flagValue(args, "--repo")
	if repo == "" {
		repo = a.cfg.Repo
	}
	repo = config.Expand(repo)

	sup := &supervisor.Supervisor{
		Queue:     a.queue,
		Runner:    &runner.Pi{Bin: a.cfg.PiBin, SessionRoot: a.cfg.SessionRoot},
		Workflows: a.workflows,
		Config:    a.cfg,
		Store:     a.ledger,
		Log:       func(format string, v ...any) { fmt.Printf(format+"\n", v...) },
		Workspaces: func(w workflow.Workflow) wf.WorkspaceProvider {
			r := repo
			if w.Repo != "" {
				r = config.Expand(w.Repo)
			}
			base := a.cfg.Base
			if w.Base != "" {
				base = w.Base
			}
			return &workspace.Provider{Repo: r, Root: a.cfg.WorktreeRoot, Base: base}
		},
	}

	asJSON := hasFlag(args, "--json")
	if asJSON {
		// Progress goes to stderr so stdout stays a single JSON document.
		sup.Log = func(format string, v ...any) { fmt.Fprintf(os.Stderr, format+"\n", v...) }
	}

	// Two ways to name a task and one meaning: `wf run <ref>` is the form
	// the design settled on, `--ref` is what already exists and what the
	// clients call.
	ref := flagValue(args, "--ref")
	if ref == "" {
		ref = firstPositional(args)
	}

	if hasFlag(args, "--once") || ref != "" {
		result, err := sup.RunOnceWith(ctx, ref,
			supervisor.Dispatch{Workflow: flagValue(args, "--workflow")})
		if errors.Is(err, supervisor.ErrNothingReady) {
			if asJSON {
				return 0, emit("results", []jsonRunResult{})
			}
			fmt.Println("nothing ready")
			return 0, nil
		}
		if err != nil {
			return 1, err
		}
		if asJSON {
			return 0, emit("results", []jsonRunResult{a.runResultToJSON(result)})
		}
		report(result)
		return 0, nil
	}

	max := a.cfg.MaxConcurrent
	if v := flagValue(args, "--max"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 1, fmt.Errorf("--max: %w", err)
		}
		max = n
	}

	results, err := sup.Run(ctx, max)
	if err != nil && !errors.Is(err, context.Canceled) {
		return 1, err
	}
	if asJSON {
		out := make([]jsonRunResult, 0, len(results))
		for _, result := range results {
			out = append(out, a.runResultToJSON(result))
		}
		return 0, emit("results", out)
	}
	for _, result := range results {
		report(result)
	}
	fmt.Printf("%d task(s) dispatched\n", len(results))
	return 0, nil
}

// now is a variable so output formatting stays testable.
var now = time.Now

func report(r supervisor.Result) {
	switch {
	case r.Applied.Escalated:
		fmt.Printf("%s escalated: %s\n", r.Task.ShortID, r.Applied.Reason)
		fmt.Printf("   wf attach %s\n", r.Task.ShortID)
	case r.Applied.Closed:
		fmt.Printf("%s closed\n", r.Task.ShortID)
	}
	for _, doc := range r.Applied.Bound {
		fmt.Printf("   note: %s\n", doc.VaultPath)
	}
	for _, ref := range r.Applied.Created {
		fmt.Printf("   follow-on: %s\n", ref)
	}
}

func (a *app) cmdAttach(ctx context.Context, args []string) (int, error) {
	ref := firstPositional(args)
	if ref == "" {
		return 1, errors.New("wf attach <ref>")
	}

	// A session is machine-local and lives only in the ledger — never on
	// the tracker — so the ledger record is the only place to look.
	found, err := a.resolve(ctx, ref)
	if err != nil {
		return 1, err
	}
	session, ok := found.Record.Bindings.Current(wf.KindSession)
	if !ok {
		return 1, fmt.Errorf("%s: no session recorded on this host", ref)
	}
	if session.Host != "" && session.Host != a.cfg.Actor {
		// A session file is a fact about one machine. Handing over a path
		// that will not resolve here reads as a broken install; saying
		// whose it is reads as the truth.
		return 1, fmt.Errorf("%s ran on %s — its session lives there, not here", ref, session.Host)
	}

	dir := session.Get(wf.MetaCwd)
	if dir == "" {
		dir, _ = os.Getwd()
	}
	bin := a.cfg.PiBin
	if bin == "" {
		bin = "pi"
	}

	// Hand the terminal to pi; wf has nothing further to do.
	pi := exec.CommandContext(ctx, bin, wf.AttachArgs(session)...)
	pi.Dir = dir
	pi.Stdin, pi.Stdout, pi.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := pi.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, fmt.Errorf("attach to %s: %w", ref, err)
	}
	return 0, nil
}

// cmdUI prints a deep link for a task, or the daemon's origin when no ref is
// given — a framed UI needs the origin before it has anything selected, and
// the port is not fixed.
func (a *app) cmdUI(ctx context.Context, args []string) (int, error) {
	origin, err := a.queue.WebUIOrigin(ctx)
	if err != nil {
		return 1, fmt.Errorf("%w (is the daemon running?)", err)
	}

	ref := firstPositional(args)
	if ref == "" {
		fmt.Println(origin)
		return 0, nil
	}
	fmt.Printf("%s/issues/%s\n", origin, ref)
	return 0, nil
}

func (a *app) summary(t wf.Task) string {
	var extra string
	if flow, ok := a.workflows.Select(t); ok {
		extra += " {" + flow.Name + "}"
	}
	if state, ok := t.Meta[wf.StateKey].(string); ok && state != "" {
		extra += " [" + state + "]"
	}
	if lease, ok := wf.ParseLease(t.Meta[wf.LeaseKey]); ok {
		extra += " (" + lease.Actor + ")"
	}
	return fmt.Sprintf("%-6s P%d  %s%s", t.ShortID, t.Priority, t.Title, extra)
}

// knownFlags take a value, so positional arguments can be told apart from
// flag values without a full flag parser.
var knownFlags = map[string]bool{
	"--limit": true, "--max": true, "--repo": true, "--ref": true, "--config": true,
	"--vault": true, "--pr": true, "--format": true, "--workflow": true,
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, flag+"=") {
			return strings.TrimPrefix(a, flag+"=")
		}
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func positionals(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if knownFlags[a] {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

func firstPositional(args []string) string {
	if p := positionals(args); len(p) > 0 {
		return p[0]
	}
	return ""
}
