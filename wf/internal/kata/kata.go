// Package kata implements the wf queue seam over the kata issue tracker.
//
// It drives the `kata` CLI with `--json` rather than speaking HTTP: the CLI
// already performs daemon discovery, auth and workspace/project resolution,
// all of which wf would otherwise reimplement badly. kata is itself a Go
// program that supports embedding its listener-free HTTP service in-process,
// so a second, embedded implementation of this same interface is the
// intended end state — once the CLI path has proved the semantics.
//
// Command surface and JSON shapes here were verified against kata v0.16.0
// by integration_test.go, which runs the real binary when it is installed.
// Four things differ from kata's published reference, and the tests pin all
// four: `claim` has no --if-unowned (an unqualified claim already refuses an
// owned issue), `close` takes no --idempotency-key, multiple PRs go through
// repeated --evidence rather than --pr, and releasing ownership is
// `edit --owner ""`.
package kata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// State that kata cannot express natively — its status is binary
// open/closed — is carried as metadata under the core's keys. The
// convention they follow happens to be kata's own, which ships
// `--with-hooks` for `work.attention` precisely so escalations show up in
// the CLI, TUI and web UI without bespoke code.

// Backend is a kata-backed wf.Queue.
type Backend struct {
	bin     string
	cwd     string
	project string
	actor   string
}

// Options configures the adapter. Bin matters because kata may not be on
// PATH under a launchd or systemd unit.
type Options struct {
	Bin string
	Cwd string
	// Project overrides the workspace's .kata.toml binding.
	Project string
	// Actor is passed as --as, so kata records wf as the owner rather than
	// whichever human account the process happens to run under.
	Actor string
}

// New builds a kata backend. An empty Bin defaults to "kata" on PATH.
func New(opts Options) *Backend {
	bin := opts.Bin
	if bin == "" {
		bin = "kata"
	}
	return &Backend{bin: bin, cwd: opts.Cwd, project: opts.Project, actor: opts.Actor}
}

var _ wf.Queue = (*Backend)(nil)

// globals appends the flags every invocation carries. The actor is passed
// exactly once: cobra takes the last occurrence of a repeated flag, so
// appending a second --as would silently override the caller's.
func (b *Backend) globals(args []string, actor string) []string {
	if b.project != "" {
		args = append(args, "--project", b.project)
	}
	if actor == "" {
		actor = b.actor
	}
	if actor != "" {
		args = append(args, "--as", actor)
	}
	return args
}

func (b *Backend) run(ctx context.Context, args ...string) (string, error) {
	return b.runAs(ctx, "", args...)
}

func (b *Backend) runAs(ctx context.Context, actor string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.bin, b.globals(args, actor)...)
	cmd.Dir = b.cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := stdout.String()

	// kata reports failures as structured JSON on stdout, which carries a
	// far better message than the exit status does.
	if detail := errorMessage(out); detail != "" {
		return out, fmt.Errorf("kata %s: %s", args[0], detail)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("kata %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

func (b *Backend) runJSON(ctx context.Context, args ...string) (any, error) {
	out, err := b.run(ctx, append(args, "--json")...)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}

	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		preview := trimmed
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return nil, fmt.Errorf("kata %s returned non-JSON output: %s", strings.Join(args, " "), preview)
	}
	return parsed, nil
}

// Ready returns kata's own view of actionable work: open issues with no
// unfinished blocking predecessor.
func (b *Backend) Ready(ctx context.Context, limit int) ([]wf.Task, error) {
	if limit <= 0 {
		limit = 20
	}
	raw, err := b.runJSON(ctx, "ready", "--limit", strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	return tasksFrom(raw), nil
}

func (b *Backend) Get(ctx context.Context, ref string) (wf.Task, error) {
	raw, err := b.runJSON(ctx, "show", ref)
	if err != nil {
		return wf.Task{}, err
	}
	tasks := tasksFrom(raw)
	if len(tasks) == 0 {
		return wf.Task{}, fmt.Errorf("kata show %s: no issue in response", ref)
	}
	return tasks[0], nil
}

// Claim takes ownership. An unqualified claim already refuses an issue owned
// by someone else — kata answers `already_claimed` — which is the semantics
// wf wants, so --force is never passed. Ownership is for humans reading the
// tracker; wf's own lease is what decides liveness.
func (b *Backend) Claim(ctx context.Context, ref, actor string) error {
	_, err := b.runAs(ctx, actor, "claim", ref)
	return err
}

// Release clears both halves of ownership: wf's lease record, which decides
// liveness, and kata's owner field, so the tracker does not show work as
// taken once nobody is running it.
func (b *Backend) Release(ctx context.Context, ref string) error {
	if err := b.UnsetMeta(ctx, ref, wf.LeaseKey); err != nil {
		return err
	}
	// An empty --owner clears the field; kata rejects the same value on
	// `assign`, which is why release goes through `edit`.
	_, err := b.run(ctx, "edit", ref, "--owner", "")
	return err
}

func (b *Backend) Comment(ctx context.Context, ref, body string) error {
	_, err := b.run(ctx, "comment", ref, "--body", body)
	return err
}

// Close maps the outcome protocol onto kata's close discipline. Everything
// goes through repeated --evidence rather than the --pr and --commit sugar,
// because those take a single value and a run can open several PRs.
func (b *Backend) Close(ctx context.Context, ref string, result wf.CloseResult) error {
	args := []string{"close", ref, "--done", "--message", result.Message}
	for _, pr := range result.PRs {
		args = append(args, "--evidence", "pr:"+pr)
	}
	for _, doc := range result.Docs {
		args = append(args, "--evidence", "reviewed-paths:"+doc)
	}
	_, err := b.run(ctx, args...)
	return err
}

func (b *Backend) Create(ctx context.Context, in wf.CreateInput) (wf.Task, error) {
	args := []string{"create", in.Title}
	if in.Body != "" {
		args = append(args, "--body", in.Body)
	}
	if in.RelatedTo != "" {
		args = append(args, "--related", in.RelatedTo)
	}
	if in.IdempotencyKey != "" {
		args = append(args, "--idempotency-key", in.IdempotencyKey)
	}

	raw, err := b.runJSON(ctx, args...)
	if err != nil {
		return wf.Task{}, err
	}
	tasks := tasksFrom(raw)
	if len(tasks) == 0 {
		return wf.Task{}, fmt.Errorf("kata create: no issue in response")
	}
	return tasks[0], nil
}

func (b *Backend) SetMeta(ctx context.Context, ref, key, value string, opts wf.SetMetaOptions) error {
	args := []string{"meta", "set", ref, key, value}
	if opts.JSON {
		args = append(args, "--json-value")
	}
	_, err := b.run(ctx, args...)
	return err
}

// UnsetMeta removes a key. Removing one that is already absent is not an
// error to wf: release runs on every exit path, including ones where the
// lease was never written.
func (b *Backend) UnsetMeta(ctx context.Context, ref, key string) error {
	if _, err := b.run(ctx, "meta", "unset", ref, key); err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "absent") {
			return nil
		}
		return err
	}
	return nil
}

func (b *Backend) GetMeta(ctx context.Context, ref string) (map[string]any, error) {
	raw, err := b.runJSON(ctx, "meta", "get", ref)
	if err != nil {
		return nil, err
	}
	return MetaObject(raw), nil
}

// Escalations lists tasks flagged for a human, as a plain metadata query.
func (b *Backend) Escalations(ctx context.Context) ([]wf.Task, error) {
	raw, err := b.runJSON(ctx, "list", "--meta", wf.AttentionKey+"=needs-human")
	if err != nil {
		return nil, err
	}
	return tasksFrom(raw), nil
}

// WebUIOrigin resolves the daemon's browser origin, for deep-linking a
// framed UI. The port is not fixed, so this must be asked for rather than
// configured.
func (b *Backend) WebUIOrigin(ctx context.Context) (string, error) {
	raw, err := b.runJSON(ctx, "daemon", "locate")
	if err != nil {
		return "", err
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("kata daemon locate: unexpected response shape")
	}
	for _, key := range []string{"web_ui_url", "webUiUrl", "ui_url", "url", "origin", "address", "endpoint"} {
		if v, ok := obj[key].(string); ok && strings.HasPrefix(v, "http") {
			return strings.TrimRight(v, "/"), nil
		}
	}
	return "", fmt.Errorf("kata daemon locate: no web UI origin in response")
}

// --- wire format ---------------------------------------------------------

// errorMessage extracts kata's structured error, which it prints as JSON on
// stdout: {"error":{"kind":"conflict","message":"...","exit_code":5}}.
func errorMessage(out string) string {
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") || !strings.Contains(trimmed, `"error"`) {
		return ""
	}
	var envelope struct {
		Error struct {
			Kind    string `json:"kind"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return ""
	}
	if envelope.Error.Message == "" {
		return ""
	}
	if envelope.Error.Code != "" {
		return envelope.Error.Code + ": " + envelope.Error.Message
	}
	return envelope.Error.Message
}

func tasksFrom(raw any) []wf.Task {
	issues := ExtractIssues(raw)
	tasks := make([]wf.Task, 0, len(issues))
	for _, issue := range issues {
		tasks = append(tasks, NormalizeIssue(issue))
	}
	return tasks
}

// ExtractIssues pulls issues out of kata's response envelope. `show` returns
// {"issue":{…},"labels":[…]} with labels beside the issue rather than in it,
// so they are folded in here; list and ready return {"issues":[…]} with
// labels already inline.
func ExtractIssues(raw any) []map[string]any {
	switch v := raw.(type) {
	case nil:
		return nil

	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			if obj, ok := item.(map[string]any); ok {
				out = append(out, obj)
			}
		}
		return out

	case map[string]any:
		for _, key := range []string{"issues", "items", "data", "results", "ready"} {
			if nested, ok := v[key].([]any); ok {
				return ExtractIssues(nested)
			}
		}
		if issue, ok := v["issue"].(map[string]any); ok {
			if labels, ok := v["labels"]; ok {
				if _, present := issue["labels"]; !present {
					issue["labels"] = labels
				}
			}
			return []map[string]any{issue}
		}
		for _, key := range []string{"uid", "id", "short_id"} {
			if _, ok := v[key]; ok {
				return []map[string]any{v}
			}
		}
	}
	return nil
}

// MetaObject unwraps a metadata response. `meta get` returns
// {"ref":…,"revision":N,"metadata":{…}}.
func MetaObject(raw any) map[string]any {
	obj, ok := raw.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	for _, key := range []string{"metadata", "meta"} {
		if nested, ok := obj[key].(map[string]any); ok {
			return nested
		}
	}
	return obj
}

// NormalizeIssue converts a kata issue into a wf.Task.
//
// `uid` is the 26-character ULID and the ref that survives renames and moves
// between projects; `id` is a per-project integer and must never be mistaken
// for it, which is why every accessor here skips values of the wrong type
// rather than taking the first key that happens to be present.
func NormalizeIssue(raw map[string]any) wf.Task {
	id := pickString(raw, "uid", "ulid", "id")

	shortID := pickString(raw, "short_id", "shortId", "ref")
	if shortID == "" && len(id) >= 4 {
		shortID = strings.ToLower(id[len(id)-4:])
	}

	priority := 2
	if p, ok := pickNumber(raw, "priority", "prio"); ok {
		priority = p
	}

	return wf.Task{
		ID:       id,
		ShortID:  shortID,
		Title:    pickString(raw, "title", "summary", "name"),
		Body:     pickString(raw, "body", "description", "text"),
		Priority: priority,
		Labels:   pickLabels(raw, "labels", "tags"),
		Owner:    pickString(raw, "owner", "assignee"),
		Meta:     MetaObject(map[string]any{"metadata": pickAny(raw, "metadata", "meta")}),
	}
}

func pickAny(obj map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := obj[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// pickString returns the first key holding a string, skipping keys whose
// value is of another type. kata's `id` is an integer sitting in front of
// the `uid` we actually want, so "first present key" is not good enough.
func pickString(obj map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := obj[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func pickNumber(obj map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		switch v := obj[k].(type) {
		case float64:
			return int(v), true
		case int:
			return v, true
		}
	}
	return 0, false
}

// pickLabels accepts both shapes kata uses: bare strings from list and ready,
// and {"label":"…"} objects from show.
func pickLabels(obj map[string]any, keys ...string) []string {
	list, ok := pickAny(obj, keys...).([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if s, ok := v["label"].(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
