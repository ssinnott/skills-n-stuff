package main

// wf note: the task↔note join, both directions.
//
// `wf note sync` renders a task's managed block into the note that faces
// it; `wf bind` declares which note that is. They live in one file because
// they are two halves of one thing, and the failure they exist to prevent
// is the two disagreeing about which binding is the note. Both go through
// notesync.Syncer.Bind, which is the only writer of that binding.
//
// As with cmdReview, this file parses argv and renders a result. The
// deciding lives in internal/notesync.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
	"github.com/ssinnott/skills-n-stuff/wf/internal/notesync"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func (a *app) syncer() *notesync.Syncer {
	return &notesync.Syncer{
		Vault: a.cfg.Vault,
		Dir:   a.cfg.NoteDir,
		Store: a.ledger,
	}
}

// jsonNoteSync is the `wf note sync` contract. Like jsonReview it is
// additive-only: the Obsidian plugin calls this on open and after dispatch,
// and needs to know whether anything moved without re-reading the file.
type jsonNoteSync struct {
	Task   string `json:"task"`
	Handle string `json:"handle,omitempty"`
	// Note is vault-relative, which is what a wikilink and the plugin's own
	// vault API both want.
	Note    string `json:"note"`
	Created bool   `json:"created,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	Bound   bool   `json:"bound,omitempty"`
}

type jsonNoteSkip struct {
	Ref   string `json:"ref"`
	Error string `json:"error"`
}

// jsonNoteResult is the shape both `wf note sync <ref>` and `--all` emit.
// One shape rather than two: a client that handles the sweep handles the
// single ref for free, and a single sync is a sweep of one.
type jsonNoteResult struct {
	Notes []jsonNoteSync `json:"notes"`
	// Unbound names tasks with no note, which `--all` reports and never
	// treats as a failure.
	Unbound []string       `json:"unbound,omitempty"`
	Skipped []jsonNoteSkip `json:"skipped,omitempty"`
}

func (a *app) cmdNote(args []string) (int, error) {
	positional := positionals(args)
	if len(positional) == 0 || positional[0] != "sync" {
		return 1, errors.New("wf note sync <ref> | wf note sync --all")
	}

	if hasFlag(args, "--all") {
		return a.noteSyncAll(args)
	}
	if len(positional) < 2 {
		return 1, errors.New("wf note sync <ref> | wf note sync --all")
	}
	return a.noteSyncOne(args, positional[1])
}

func (a *app) noteSyncOne(args []string, ref string) (int, error) {
	// The ledger, not the queue: a note renders bindings, and the ledger is
	// what is authoritative for those. It also means `wf note sync` needs
	// no tracker running at all, which is the point of a note you can open
	// on a device the tracker does not reach.
	rec, err := a.ledger.Resolve(ref)
	if err != nil {
		return 1, err
	}

	// Create: a human who typed this verb has asked for the note. See
	// notesync.create for why the sweep answers the same question the
	// other way.
	res, err := a.syncer().Sync(rec, notesync.Options{Create: true})
	if err != nil {
		return 1, err
	}

	if hasFlag(args, "--json") {
		return 0, emit("sync", jsonNoteResult{Notes: []jsonNoteSync{noteSyncToJSON(res)}})
	}
	printNoteSync(res)
	return 0, nil
}

func (a *app) noteSyncAll(args []string) (int, error) {
	sweep, err := a.syncer().SyncAll()
	if err != nil {
		return 1, err
	}

	if hasFlag(args, "--json") {
		out := jsonNoteResult{Notes: make([]jsonNoteSync, 0, len(sweep.Results)), Unbound: sweep.Unbound}
		for _, res := range sweep.Results {
			out.Notes = append(out.Notes, noteSyncToJSON(res))
		}
		for _, skip := range sweep.Skipped {
			out.Skipped = append(out.Skipped, jsonNoteSkip{Ref: skip.Ref, Error: skip.Err.Error()})
		}
		return 0, emit("sync", out)
	}

	changed := 0
	for _, res := range sweep.Results {
		if res.Changed {
			changed++
			printNoteSync(res)
		}
	}
	fmt.Printf("%d note(s) synced, %d changed\n", len(sweep.Results), changed)
	if len(sweep.Unbound) > 0 {
		fmt.Printf("%d task(s) have no note — `wf note sync <ref>` creates one\n", len(sweep.Unbound))
	}
	// Skipped files are named, not counted: the whole reason List hands
	// back records alongside a *SkipError is so a corrupt one can be
	// pointed at and fixed while the rest keep working.
	for _, skip := range sweep.Skipped {
		fmt.Fprintf(os.Stderr, "skipped %s: %v\n", skip.Ref, skip.Err)
	}
	if len(sweep.Skipped) > 0 {
		// A sweep that could not read part of the ledger did not do what
		// was asked, and a script driving it deserves to know.
		return 1, nil
	}
	return 0, nil
}

func noteSyncToJSON(r notesync.Result) jsonNoteSync {
	return jsonNoteSync{
		Task:    r.Task,
		Handle:  r.Handle,
		Note:    r.Note,
		Created: r.Created,
		Changed: r.Changed,
		Bound:   r.Bound,
	}
}

func printNoteSync(r notesync.Result) {
	switch {
	case r.Created:
		fmt.Printf("created %s\n", r.Note)
	case r.Changed:
		fmt.Printf("updated %s\n", r.Note)
	default:
		fmt.Printf("unchanged %s\n", r.Note)
	}
}

// cmdBind binds a task to a note by hand: the id into the note's
// frontmatter, the note into the task's bindings.
//
// Two frontmatter fields are written, and they answer different questions.
// `wf-task` is the durable half of the join — it is what ReadTaskID reads,
// what survives a rename in Obsidian, and therefore what recovers the
// binding when the recorded path stops resolving. `kata-issue` stays
// because it names the tracker row, which is one binding among the task's
// many rather than the task itself; a note bound before this existed keeps
// working, and anything reading it keeps reading it.
func (a *app) cmdBind(ctx context.Context, args []string) (int, error) {
	positional := positionals(args)
	if len(positional) < 2 {
		return 1, errors.New("wf bind <ref> <note.md>")
	}
	ref, notePath := positional[0], positional[1]

	task, err := a.queue.Get(ctx, ref)
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
	if existing := note.GetField(text, note.TaskKey); existing != "" && existing != task.ID {
		return 1, fmt.Errorf("%s is already bound to task %s", notePath, existing)
	}

	updated := note.SetField(text, note.IssueKey, task.ID)
	updated = note.SetField(updated, note.TaskKey, task.ID)
	if updated != text {
		// Atomic, like every other write into the vault: a note is a file
		// a human is probably looking at.
		if err := notesync.WriteNote(abs, updated); err != nil {
			return 1, err
		}
	}

	// Publication to the tracker is unchanged — one direction, never read
	// back — and is what keeps a task bound by hand visible to a client
	// that only speaks to the queue.
	if err := a.queue.SetMeta(ctx, task.ID, wf.DocKey, notePath, wf.SetMetaOptions{}); err != nil {
		return 1, fmt.Errorf("bind note path on %s: %w", task.ShortID, err)
	}

	// The ledger is the half `wf note sync` reads, and it stores the
	// vault-relative path because that is what resolves on every device
	// holding the vault. A note outside the vault is a real note wf simply
	// cannot manage, so the binding is not claimed and the human is told
	// rather than left wondering why sync never touches it.
	syncer := a.syncer()
	rel, inVault := syncer.Rel(abs)
	switch {
	case a.cfg.Vault == "":
		fmt.Fprintln(os.Stderr, "no vault configured — `wf note sync` will not manage this note")
	case !inVault:
		fmt.Fprintf(os.Stderr, "%s is outside the vault — `wf note sync` will not manage this note\n", notePath)
	default:
		if err := syncer.Bind(task.ID, task.ShortID, rel); err != nil {
			return 1, fmt.Errorf("record note binding on %s: %w", task.ShortID, err)
		}
	}

	fmt.Printf("bound %s ↔ %s\n", task.ShortID, notePath)
	return 0, nil
}
