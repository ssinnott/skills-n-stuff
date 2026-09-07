package main

// wf attach and wf ui: handing the terminal (or a link) to something else.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func (a *app) cmdAttach(ctx context.Context, args []string) (int, error) {
	fs, _ := newFlagSet("attach")
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return 1, err
	}
	var ref string
	if len(positionals) > 0 {
		ref = positionals[0]
	}
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

	bin := a.cfg.PiBin
	if bin == "" {
		bin = "pi"
	}

	// Hand the terminal to pi; wf has nothing further to do.
	pi := exec.CommandContext(ctx, bin, wf.AttachArgs(session)...)
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
	fs, _ := newFlagSet("ui")
	positionals, err := parseFlags(fs, args)
	if err != nil {
		return 1, err
	}

	origin, err := a.queue.WebUIOrigin(ctx)
	if err != nil {
		return 1, fmt.Errorf("%w (is the daemon running?)", err)
	}

	if len(positionals) == 0 {
		fmt.Println(origin)
		return 0, nil
	}
	fmt.Printf("%s/issues/%s\n", origin, positionals[0])
	return 0, nil
}
