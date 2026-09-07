// Command wf runs agent work off a queue.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ssinnott/skills-n-stuff/wf/internal/config"
	"github.com/ssinnott/skills-n-stuff/wf/internal/kata"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
)

const usage = `wf — workflow CLI over pluggable queues

  wf ready [--limit N]                                         actionable work, top of queue first
  wf show <ref>                                                one task: its runs and what each produced
  wf escalations                                               tasks flagged needs-human
  wf workflows                                                 canned workflows loaded from the workflow dir
  wf run <ref> [--workflow W] [--repo P]                       run one workflow against a task
  wf close <ref> [--message M]                                 close a task with the evidence its runs recorded
  wf attach <ref>                                              open the task's pi session
  wf bind <ref> <note.md>                                      bind a task to an Obsidian note, both ways
  wf ui [<ref>]                                                print the web UI deep link for a task
  wf review <ref> | --pr <url> [--repo P] | <ref> --stop       resolve the task's diff and open it in difit, or stop the viewer
  wf review comment <ref> [--format difit]                     read a pasted review prompt from stdin
  wf gc [--delete]                                             report ledger bindings whose referent is gone; --delete drops dead records

A <ref> is anything ` + "`kata show`" + ` accepts: the issue's ULID or its short id.

Add --json to any subcommand for machine-readable output; that is the
protocol both the pi extension and the Obsidian plugin speak.

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

// commonFlags are the two flags every subcommand's FlagSet registers.
// --config is also read out of argv before any FlagSet exists (see
// configFlagValue), since newApp needs it before dispatch picks a command.
type commonFlags struct {
	json   bool
	config string
}

// newFlagSet builds one subcommand's FlagSet, wired the way every wf
// subcommand needs: errors reported rather than panicking or exiting, usage
// text to stderr, and --json/--config registered so neither is ever an
// "unknown flag" no matter which subcommand sees it.
func newFlagSet(name string) (*flag.FlagSet, *commonFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cf := &commonFlags{}
	fs.BoolVar(&cf.json, "json", false, "machine-readable output")
	fs.StringVar(&cf.config, "config", "", "path to config.json (default ~/.wf/config.json)")
	return fs, cf
}

// parseFlags parses fs against args and returns the positionals, wherever
// they fall. Go's flag package stops at the first non-flag argument, which
// would make `wf run <ref> --workflow W` treat --workflow as a positional;
// this loop instead peels off one positional at a time and keeps parsing
// whatever comes after it, so flags may appear before or after positionals
// interchangeably. Any argument flag.Parse does not recognize is still a
// hard error — that is the whole point of moving off the old scanner.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rem := fs.Args()
		if len(rem) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rem[0])
		args = rem[1:]
	}
}

// configFlagValue picks --config out of raw argv without a FlagSet, since
// newApp has to open the config before any subcommand's FlagSet exists to
// declare the flag properly. Every subcommand still registers --config
// itself (via newFlagSet) so it is a recognized flag once real parsing
// happens, rather than an error there.
func configFlagValue(args []string) string {
	for i, a := range args {
		if a == "--config" && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--config="); ok {
			return v
		}
	}
	return ""
}

func newApp(args []string) (*app, error) {
	cfg, err := config.Load(configFlagValue(args))
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
	case "close":
		return a.cmdClose(ctx, rest)
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
	default:
		return 1, fmt.Errorf("unknown command: %s\n\n%s", cmd, usage)
	}
}

func (a *app) cmdReady(ctx context.Context, args []string) (int, error) {
	fs, cf := newFlagSet("ready")
	limit := fs.Int("limit", 20, "max tasks to list")
	if _, err := parseFlags(fs, args); err != nil {
		return 1, err
	}

	tasks, err := a.queue.Ready(ctx, *limit)
	if err != nil {
		return 1, err
	}
	if cf.json {
		return 0, emit("tasks", a.tasksToJSON(tasks))
	}
	return a.printTasks(tasks, "nothing ready"), nil
}

func (a *app) cmdEscalations(ctx context.Context, args []string) (int, error) {
	fs, cf := newFlagSet("escalations")
	if _, err := parseFlags(fs, args); err != nil {
		return 1, err
	}

	tasks, err := a.queue.Escalations(ctx)
	if err != nil {
		return 1, err
	}
	if cf.json {
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
	fs, cf := newFlagSet("workflows")
	if _, err := parseFlags(fs, args); err != nil {
		return 1, err
	}

	flows := a.workflows.All()
	if cf.json {
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
