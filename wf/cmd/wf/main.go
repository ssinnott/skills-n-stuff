// Command wf runs agent work off a queue.
//
// What ships today is the read side plus the bindings: enough to see the
// queue, bind a task to a note, and reattach to a task's pi session. The
// dispatch loop (`wf run`) lands with the worktree and pi runner seams; it
// refuses rather than pretending.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/ssinnott/skills-n-stuff/wf/internal/kata"
	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// ObsidianNoteKey is the task-side half of the note binding.
const ObsidianNoteKey = "obsidian.note"

const usage = `wf — workflow CLI over pluggable queues

  wf ready [--limit N]        actionable work, top of queue first
  wf show <ref>               one task, with its lease and session
  wf escalations              tasks flagged needs-human
  wf attach <ref>             open the task's pi session
  wf bind <ref> <note.md>     bind a task to an Obsidian note, both ways
  wf ui <ref>                 print the web UI deep link for a task
  wf run [--once] [--max N]   the supervisor loop (not yet implemented)

Queue backend: kata. Set KATA_BIN to override the binary.`

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

func run(ctx context.Context, argv []string) (int, error) {
	if len(argv) == 0 || argv[0] == "help" || argv[0] == "--help" || argv[0] == "-h" {
		fmt.Println(usage)
		return 0, nil
	}

	cmd, rest := argv[0], argv[1:]

	cwd, err := os.Getwd()
	if err != nil {
		return 1, fmt.Errorf("resolve working directory: %w", err)
	}
	backend := kata.New(kata.Options{Bin: os.Getenv("KATA_BIN"), Cwd: cwd})

	switch cmd {
	case "ready":
		return cmdReady(ctx, backend, rest)
	case "show":
		return cmdShow(ctx, backend, rest)
	case "escalations":
		return cmdEscalations(ctx, backend)
	case "attach":
		return cmdAttach(ctx, backend, rest)
	case "bind":
		return cmdBind(ctx, backend, rest)
	case "ui":
		return cmdUI(ctx, backend, rest)
	case "run":
		fmt.Fprintln(os.Stderr, strings.Join([]string{
			"wf run is not implemented yet — it needs the worktree workspace",
			"and the pi runner (see DESIGN.md build plan).",
			"",
			"Available today: wf ready, wf show, wf escalations, wf attach, wf bind, wf ui.",
		}, "\n"))
		return 2, nil
	default:
		return 1, fmt.Errorf("unknown command: %s\n\n%s", cmd, usage)
	}
}

func cmdReady(ctx context.Context, backend *kata.Backend, args []string) (int, error) {
	limit := 20
	if v := flagValue(args, "--limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 1, fmt.Errorf("--limit: %w", err)
		}
		limit = n
	}

	tasks, err := backend.Ready(ctx, limit)
	if err != nil {
		return 1, err
	}
	if len(tasks) == 0 {
		fmt.Println("nothing ready")
		return 0, nil
	}
	for _, t := range tasks {
		fmt.Println(summary(t))
	}
	return 0, nil
}

func cmdEscalations(ctx context.Context, backend *kata.Backend) (int, error) {
	tasks, err := backend.Escalations(ctx)
	if err != nil {
		return 1, err
	}
	if len(tasks) == 0 {
		fmt.Println("no escalations")
		return 0, nil
	}
	for _, t := range tasks {
		fmt.Println(summary(t))
	}
	return 0, nil
}

func cmdShow(ctx context.Context, backend *kata.Backend, args []string) (int, error) {
	if len(args) == 0 {
		return 1, fmt.Errorf("wf show <ref>")
	}

	task, err := backend.Get(ctx, args[0])
	if err != nil {
		return 1, err
	}

	fmt.Printf("%s  %s\n", task.ShortID, task.Title)
	fmt.Printf("id       %s\n", task.ID)
	fmt.Printf("priority %d\n", task.Priority)
	if len(task.Labels) > 0 {
		fmt.Printf("labels   %s\n", strings.Join(task.Labels, ", "))
	}
	if task.Owner != "" {
		fmt.Printf("owner    %s\n", task.Owner)
	}

	if lease, ok := wf.ParseLease(task.Meta[wf.LeaseKey]); ok {
		fmt.Printf("lease    %s\n", lease)
	} else {
		fmt.Println("lease    unheld")
	}

	if binding, ok := wf.BindingFromMeta(task.Meta); ok {
		fmt.Printf("session  %s\n", binding.Path)
	} else {
		fmt.Println("session  none")
	}
	if history := wf.HistoryFromMeta(task.Meta); len(history) > 1 {
		fmt.Printf("runs     %d\n", len(history))
	}
	if path, ok := task.Meta[ObsidianNoteKey].(string); ok && path != "" {
		fmt.Printf("note     %s\n", path)
	}
	return 0, nil
}

func cmdAttach(ctx context.Context, backend *kata.Backend, args []string) (int, error) {
	if len(args) == 0 {
		return 1, fmt.Errorf("wf attach <ref>")
	}
	ref := args[0]

	meta, err := backend.GetMeta(ctx, ref)
	if err != nil {
		return 1, err
	}
	binding, ok := wf.BindingFromMeta(meta)
	if !ok {
		return 1, fmt.Errorf("%s has no bound session yet", ref)
	}

	dir := binding.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}

	// Hand the terminal to pi; wf has nothing further to do.
	pi := exec.CommandContext(ctx, "pi", binding.AttachArgs()...)
	pi.Dir = dir
	pi.Stdin, pi.Stdout, pi.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := pi.Run(); err != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, fmt.Errorf("attach to %s: %w", ref, err)
	}
	return 0, nil
}

func cmdBind(ctx context.Context, backend *kata.Backend, args []string) (int, error) {
	if len(args) < 2 {
		return 1, fmt.Errorf("wf bind <ref> <note.md>")
	}
	ref, notePath := args[0], args[1]

	task, err := backend.Get(ctx, ref)
	if err != nil {
		return 1, err
	}

	abs, err := filepath.Abs(notePath)
	if err != nil {
		return 1, fmt.Errorf("resolve %s: %w", notePath, err)
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return 1, fmt.Errorf("read %s: %w", notePath, err)
	}

	text := string(raw)
	if existing := note.GetField(text, note.IssueKey); existing != "" && existing != task.ID {
		return 1, fmt.Errorf("%s is already bound to %s", notePath, existing)
	}

	// wf writes both sides here because this is an explicit human action,
	// not the run loop — where the plugin owns frontmatter and the
	// supervisor owns metadata.
	if err := os.WriteFile(abs, []byte(note.SetField(text, note.IssueKey, task.ID)), 0o644); err != nil {
		return 1, fmt.Errorf("write %s: %w", notePath, err)
	}
	if err := backend.SetMeta(ctx, task.ID, ObsidianNoteKey, notePath, wf.SetMetaOptions{}); err != nil {
		return 1, fmt.Errorf("bind note path on %s: %w", task.ShortID, err)
	}

	fmt.Printf("bound %s ↔ %s\n", task.ShortID, notePath)
	return 0, nil
}

func cmdUI(ctx context.Context, backend *kata.Backend, args []string) (int, error) {
	if len(args) == 0 {
		return 1, fmt.Errorf("wf ui <ref>")
	}
	origin, err := backend.WebUIOrigin(ctx)
	if err != nil {
		return 1, fmt.Errorf("%w (is the daemon running?)", err)
	}
	fmt.Printf("%s/issues/%s\n", origin, args[0])
	return 0, nil
}

func summary(t wf.Task) string {
	var extra string
	if state, ok := t.Meta[kata.StateKey].(string); ok && state != "" {
		extra += " [" + state + "]"
	}
	if lease, ok := wf.ParseLease(t.Meta[wf.LeaseKey]); ok {
		extra += " (" + lease.Actor + ")"
	}
	return fmt.Sprintf("%-6s P%d  %s%s", t.ShortID, t.Priority, t.Title, extra)
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func asExitError(err error, target **exec.ExitError) bool {
	e, ok := err.(*exec.ExitError)
	if ok {
		*target = e
	}
	return ok
}
