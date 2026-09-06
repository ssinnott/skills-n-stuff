package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/review"
	"github.com/ssinnott/skills-n-stuff/wf/internal/supervisor"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
)

// Machine-readable output.
//
// Both clients — the pi extension and the Obsidian plugin — talk to wf
// through this rather than through kata directly, so there is one place that
// knows how a task is shaped and one protocol to keep stable. The field
// names are wf's own vocabulary, not kata's: a client written against these
// keeps working when the queue backend changes.
//
// `wf show --json` emits one object: the tracker row's fields (jsonTask,
// minus its runs count) at the top level, plus `bindings` — the task's own,
// the ones no run produced — and `runs`, each carrying what that run
// produced. There is no second, wf-only object any more: kata's ULID is the
// task's only id, so the tracker row and the ledger record describe the same
// task rather than two. `ready`, `escalations` and `run --json` keep
// emitting bare jsonTask; a client that wants the run count reads
// len(runs) from `show`.

type jsonLease struct {
	Actor   string `json:"actor"`
	Host    string `json:"host"`
	Renewed string `json:"renewed"`
	Stale   bool   `json:"stale"`
}

type jsonTask struct {
	ID       string     `json:"id"`
	ShortID  string     `json:"shortId"`
	Title    string     `json:"title"`
	Body     string     `json:"body,omitempty"`
	Priority int        `json:"priority"`
	Labels   []string   `json:"labels,omitempty"`
	Owner    string     `json:"owner,omitempty"`
	State    string     `json:"state,omitempty"`
	Workflow string     `json:"workflow,omitempty"`
	Lease    *jsonLease `json:"lease,omitempty"`
	Session  string     `json:"session,omitempty"`
	Cwd      string     `json:"cwd,omitempty"`
	Note     string     `json:"note,omitempty"`
	// NeedsHuman is the flag clients render as an escalation.
	NeedsHuman bool `json:"needsHuman,omitempty"`
}

type jsonWorkflow struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Profile     string   `json:"profile,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	BindDocs    bool     `json:"bindDocs,omitempty"`
	VaultDir    string   `json:"vaultDir,omitempty"`
}

// jsonReview is the `wf review` contract. Another client codes directly
// against these field names, so they are additive-only: a new rung or
// flag gets a new omitempty field, never a renamed one.
type jsonReview struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind,omitempty"`

	URL  string `json:"url,omitempty"`
	Port int    `json:"port,omitempty"`
	PID  int    `json:"pid,omitempty"`

	Repo   string `json:"repo,omitempty"`
	Target string `json:"target,omitempty"`
	Base   string `json:"base,omitempty"`
	PR     string `json:"pr,omitempty"`
	Seeded int    `json:"seeded,omitempty"`

	// Note is set instead of the viewer fields when kind is "doc": there is
	// no diff to open, only a vault-relative path.
	Note string `json:"note,omitempty"`

	// Stopped and Commented are set only by `--stop` and `review comment`
	// respectively; a resolve response never carries either.
	Stopped   bool `json:"stopped,omitempty"`
	Commented bool `json:"commented,omitempty"`
	// Count accompanies Commented only for `review comment --format
	// difit`: how many harvested threads were folded into the posted
	// comment. Absent for the plain-text format.
	Count int `json:"count,omitempty"`
}

// jsonFinding and jsonGC are the `wf gc` contract.
type jsonFinding struct {
	Kind string `json:"kind"`
	// Task is the record this finding belongs to, if any — the tracker's
	// own id.
	Task   string `json:"task,omitempty"`
	Ref    string `json:"ref"`
	Detail string `json:"detail,omitempty"`
	Repair string `json:"repair,omitempty"`
	Done   bool   `json:"done,omitempty"`
	Error  string `json:"error,omitempty"`
}

type jsonGC struct {
	Records  int           `json:"records"`
	Deleted  bool          `json:"deleted"`
	Repaired int           `json:"repaired"`
	Findings []jsonFinding `json:"findings"`
}

func reviewToJSON(r review.Result) jsonReview {
	out := jsonReview{Ref: r.Ref, Kind: string(r.Target.Kind)}
	if r.Target.Kind == review.KindDoc {
		out.Note = r.Target.Note
		return out
	}
	out.Repo = r.Target.Repo
	out.Target = r.Target.Branch
	out.Base = r.Target.Base
	out.PR = r.Target.PR
	out.Seeded = r.Seeded
	out.URL = r.Viewer.URL
	out.Port = r.Viewer.Port
	out.PID = r.Viewer.PID
	return out
}

type jsonRunResult struct {
	Task      jsonTask `json:"task"`
	Closed    bool     `json:"closed"`
	Escalated bool     `json:"escalated"`
	Reason    string   `json:"reason,omitempty"`
	Session   string   `json:"session,omitempty"`
	Notes     []string `json:"notes,omitempty"`
	Created   []string `json:"created,omitempty"`
}

func (a *app) toJSON(t wf.Task) jsonTask {
	out := jsonTask{
		ID:       t.ID,
		ShortID:  t.ShortID,
		Title:    t.Title,
		Body:     t.Body,
		Priority: t.Priority,
		Labels:   t.Labels,
		Owner:    t.Owner,
	}

	if state, ok := t.Meta[wf.StateKey].(string); ok {
		out.State = state
	}
	if attention, ok := t.Meta[wf.AttentionKey].(string); ok && attention != "" {
		out.NeedsHuman = true
	}
	if flow, ok := a.workflows.Select(t); ok {
		out.Workflow = flow.Name
	}
	if lease, ok := wf.ParseLease(t.Meta[wf.LeaseKey]); ok {
		out.Lease = &jsonLease{
			Actor:   lease.Actor,
			Host:    lease.Host,
			Renewed: lease.Renewed.Format("2006-01-02T15:04:05Z07:00"),
			Stale:   lease.IsStale(now()),
		}
	}
	bindings := wf.LoadBindings(t)
	if session, ok := bindings.Current(wf.KindSession); ok {
		out.Session = session.Ref
		out.Cwd = session.Get(wf.MetaCwd)
	}
	// The bound note is the tracker's own fact, read straight off its
	// metadata rather than through a binding: wf.doc is written once, by
	// `wf bind`, and is never a run's output.
	if doc, ok := t.Meta[wf.DocKey].(string); ok && doc != "" {
		out.Note = doc
	}
	return out
}

func (a *app) tasksToJSON(tasks []wf.Task) []jsonTask {
	out := make([]jsonTask, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, a.toJSON(t))
	}
	return out
}

func workflowsToJSON(flows []workflow.Workflow) []jsonWorkflow {
	out := make([]jsonWorkflow, 0, len(flows))
	for _, w := range flows {
		out = append(out, jsonWorkflow{
			Name:        w.Name,
			Description: w.Description,
			Profile:     w.Profile,
			Labels:      w.Labels,
			BindDocs:    w.BindDocs,
			VaultDir:    w.VaultDir,
		})
	}
	return out
}

func (a *app) runResultToJSON(r supervisor.Result) jsonRunResult {
	out := jsonRunResult{
		Task:      a.toJSON(r.Task),
		Closed:    r.Applied.Closed,
		Escalated: r.Applied.Escalated,
		Reason:    r.Applied.Reason,
		Session:   r.Session,
		Created:   r.Applied.Created,
	}
	for _, doc := range r.Applied.Bound {
		out.Notes = append(out.Notes, doc.VaultPath)
	}
	return out
}

// Machine-readable form of a task's bindings and run history.
//
// This is the shape `wf show` prints, with the same grouping: the task's own
// bindings at the top level, and each run carrying what it produced. Both
// clients render that structure, so putting the join here rather than in
// each of them is the whole reason `--json` exists. A consumer that wants
// every binding regardless of provenance unions the two lists.

// jsonBinding is one typed reference. State is the raw vocabulary and
// StateLabel is the word to show a human — carried rather than derived,
// because a client that translated for itself would drift from `wf show`.
type jsonBinding struct {
	Kind       string            `json:"kind"`
	Ref        string            `json:"ref"`
	Label      string            `json:"label,omitempty"`
	State      string            `json:"state,omitempty"`
	StateLabel string            `json:"stateLabel,omitempty"`
	At         string            `json:"at,omitempty"`
	Via        string            `json:"via,omitempty"`
	Host       string            `json:"host,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
}

// jsonRun is one dispatch and its output. Workflow, profile and model are on
// the run because a task dispatched twice under two recipes has a history,
// which is precisely what a client comparing two models needs to read.
type jsonRun struct {
	ID       string        `json:"id"`
	Workflow string        `json:"workflow,omitempty"`
	Profile  string        `json:"profile,omitempty"`
	Model    string        `json:"model,omitempty"`
	Host     string        `json:"host,omitempty"`
	Started  string        `json:"started,omitempty"`
	Ended    string        `json:"ended,omitempty"`
	Outcome  string        `json:"outcome,omitempty"`
	Bindings []jsonBinding `json:"bindings,omitempty"`
}

// jsonShow is the whole of `wf show --json`: the tracker row's own fields at
// the top level, the task's own bindings, and each run with what it
// produced. One object, because there is one task now — kata's ULID is its
// only id, so there is nothing left to give a second key to.
type jsonShow struct {
	jsonTask
	// Bindings are the task's own — every binding no run produced. A run's
	// output hangs off that run instead.
	Bindings []jsonBinding `json:"bindings,omitempty"`
	// Runs is the ledger's history for this task, oldest first.
	Runs []jsonRun `json:"runs,omitempty"`
	// Updated is when the ledger record last changed. Omitted for a task
	// the ledger has never recorded anything about.
	Updated string `json:"updated,omitempty"`
}

func bindingToJSON(b wf.Binding) jsonBinding {
	out := jsonBinding{
		Kind:       string(b.Kind),
		Ref:        b.Ref,
		Label:      b.Label,
		State:      string(b.State),
		StateLabel: b.StateLabel(),
		Via:        b.Via,
		Host:       b.Host,
		Meta:       b.Meta,
	}
	if !b.At.IsZero() {
		out.At = stamp(b.At)
	}
	return out
}

func bindingsToJSON(bs wf.Bindings) []jsonBinding {
	if len(bs) == 0 {
		return nil
	}
	out := make([]jsonBinding, 0, len(bs))
	for _, b := range bs {
		out = append(out, bindingToJSON(b))
	}
	return out
}

// showToJSON renders the whole of `wf show --json`: the tracker row found
// resolved to, plus the task's own bindings and its runs from the ledger.
func (a *app) showToJSON(found resolved) jsonShow {
	rec := found.Record
	out := jsonShow{
		jsonTask: a.toJSON(found.Task),
		Bindings: bindingsToJSON(sortBindings(taskOwnBindings(rec))),
	}
	if !rec.Updated.IsZero() {
		out.Updated = stamp(rec.Updated)
	}
	for _, run := range rec.Runs {
		entry := jsonRun{
			ID:       run.ID,
			Workflow: run.Workflow,
			Profile:  run.Profile,
			Model:    run.Model,
			Host:     run.Host,
			Outcome:  string(run.Outcome),
			Bindings: bindingsToJSON(sortBindings(rec.Produced(run.ID))),
		}
		if !run.Started.IsZero() {
			entry.Started = stamp(run.Started)
		}
		if run.Ended != nil {
			entry.Ended = stamp(*run.Ended)
		}
		out.Runs = append(out.Runs, entry)
	}
	return out
}

// stamp is the one timestamp format the JSON protocol uses, matching what
// jsonLease already emits.
func stamp(t time.Time) string { return t.Format("2006-01-02T15:04:05Z07:00") }

// emit writes a JSON document to stdout. Every payload is an object with a
// named field rather than a bare array, so the shape can grow without
// breaking a client that already parses it.
func emit(key string, value any) error {
	return emitFields(map[string]any{key: value})
}

// emitFields is emit for a document carrying more than one named field.
func emitFields(payload map[string]any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}

// emitValue writes value itself as the JSON document, with no wrapping key.
// `wf show` is the one payload that IS the object rather than a named field
// of one, now that there is only one task to describe.
func emitValue(value any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(value); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}
