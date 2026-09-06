package main

import (
	"encoding/json"
	"fmt"
	"os"

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
	Runs     int        `json:"runs,omitempty"`
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
	if runs := len(bindings.ByKind(wf.KindSession)); runs > 0 {
		out.Runs = runs
	}
	if doc, ok := bindings.Current(wf.KindDoc); ok {
		out.Note = doc.Ref
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

// emit writes a JSON document to stdout. Every payload is an object with a
// named field rather than a bare array, so the shape can grow without
// breaking a client that already parses it.
func emit(key string, value any) error {
	payload := map[string]any{key: value}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		return fmt.Errorf("write json: %w", err)
	}
	return nil
}
