// Package kata implements the wf queue seam over the kata issue tracker.
//
// It drives the `kata` CLI with `--json` rather than speaking HTTP: the CLI
// already performs daemon discovery, auth and workspace/project resolution,
// all of which wf would otherwise reimplement badly. kata is itself a Go
// program that supports embedding its listener-free HTTP service in-process,
// so a second, embedded implementation of this same interface is the
// intended end state — once the CLI path has proved the semantics.
//
// IMPORTANT — this adapter was written against kata's published command
// reference, not against a running daemon. Command names and flags are taken
// from the docs; the *shape of the JSON they return* is not documented and is
// therefore inferred. Every such assumption is confined to normalizeIssue and
// extractIssues at the bottom of this file, which are the only things to fix
// after the first live run. Nothing else in wf touches kata's wire format.
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
// open/closed — is carried as metadata, following the convention kata's own
// docs use for exactly this (`kata list --meta work.attention=needs-human`).
// That makes the escalation queue a plain list query in the CLI, TUI and web
// UI, with no code on our side.
const (
	AttentionKey = "work.attention"
	StateKey     = "wf.state"
)

// Backend is a kata-backed wf.Queue.
type Backend struct {
	bin string
	cwd string
}

// Options configures the adapter. Bin matters because kata may not be on
// PATH under a launchd or systemd unit.
type Options struct {
	Bin string
	Cwd string
}

// New builds a kata backend. An empty Bin defaults to "kata" on PATH.
func New(opts Options) *Backend {
	bin := opts.Bin
	if bin == "" {
		bin = "kata"
	}
	return &Backend{bin: bin, cwd: opts.Cwd}
}

var _ wf.Queue = (*Backend)(nil)

func (b *Backend) Name() string { return "kata" }

func (b *Backend) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, b.bin, args...)
	cmd.Dir = b.cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("kata %s: %s", strings.Join(args, " "), detail)
	}
	return stdout.String(), nil
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

// Ready returns kata's own view of actionable work: open issues not blocked
// by unfinished predecessors.
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

// Claim uses --if-unowned so a claim never silently steals. wf's own lease,
// not this call, decides liveness; kata's owner field is for humans reading
// the tracker.
func (b *Backend) Claim(ctx context.Context, ref, actor string) error {
	_, err := b.run(ctx, "claim", ref, "--if-unowned", "--comment", "Claimed by "+actor)
	return err
}

// Release clears wf's lease record and leaves kata's owner field alone:
// kata documents no release or unclaim verb. A stale owner is cosmetic, a
// stale lease is not. Revisit once the live CLI is available — if an
// `edit --unowned` exists, clear the owner here too.
func (b *Backend) Release(ctx context.Context, ref string) error {
	return b.UnsetMeta(ctx, ref, wf.LeaseKey)
}

func (b *Backend) Comment(ctx context.Context, ref, body string) error {
	_, err := b.run(ctx, "comment", ref, "--body", body)
	return err
}

// Close maps the outcome protocol onto kata's close discipline, which wants
// exactly this evidence: a message plus PRs, commits, tests and reviewed
// artifacts.
func (b *Backend) Close(ctx context.Context, ref string, result wf.CloseResult, idempotencyKey string) error {
	args := []string{"close", ref, "--done", "--message", result.Message}
	for _, pr := range result.PRs {
		args = append(args, "--pr", pr)
	}
	for _, sha := range result.Commits {
		args = append(args, "--commit", sha)
	}
	for _, doc := range result.Docs {
		args = append(args, "--reviewed", doc)
	}
	for _, test := range result.Tests {
		args = append(args, "--test", test)
	}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	_, err := b.run(ctx, args...)
	return err
}

func (b *Backend) Create(ctx context.Context, in wf.CreateInput) (wf.Task, error) {
	args := []string{"create", in.Title}
	if in.Body != "" {
		args = append(args, "--body", in.Body)
	}
	for _, label := range in.Labels {
		args = append(args, "--label", label)
	}
	if in.Priority > 0 {
		args = append(args, "--priority", strconv.Itoa(in.Priority))
	}
	if in.RelatedTo != "" {
		args = append(args, "--related", in.RelatedTo)
	}
	if in.BlockedBy != "" {
		args = append(args, "--blocked-by", in.BlockedBy)
	}
	for k, v := range in.Meta {
		args = append(args, "--meta", k+"="+v)
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
	if opts.IfMatch != "" {
		args = append(args, "--if-match", opts.IfMatch)
	}
	_, err := b.run(ctx, args...)
	return err
}

func (b *Backend) UnsetMeta(ctx context.Context, ref, key string) error {
	_, err := b.run(ctx, "meta", "unset", ref, key)
	return err
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
	raw, err := b.runJSON(ctx, "list", "--meta", AttentionKey+"=needs-human")
	if err != nil {
		return nil, err
	}
	return tasksFrom(raw), nil
}

// SetState records wf's state vocabulary, mirroring needs-human into the
// attention key so kata's own surfaces can filter on it.
func (b *Backend) SetState(ctx context.Context, ref string, state wf.WorkState) error {
	if err := b.SetMeta(ctx, ref, StateKey, string(state), wf.SetMetaOptions{}); err != nil {
		return err
	}
	if state == wf.StateNeedsHuman {
		return b.SetMeta(ctx, ref, AttentionKey, "needs-human", wf.SetMetaOptions{})
	}
	// Best effort: the key is often already absent, which is not a failure.
	_ = b.UnsetMeta(ctx, ref, AttentionKey)
	return nil
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
	for _, key := range []string{"web_ui_url", "webUiUrl", "ui_url", "origin", "address", "endpoint"} {
		if v, ok := obj[key].(string); ok && strings.HasPrefix(v, "http") {
			return strings.TrimRight(v, "/"), nil
		}
	}
	return "", fmt.Errorf("kata daemon locate: no web UI origin in response")
}

// --- wire-format assumptions, all of them, live below this line ----------

func tasksFrom(raw any) []wf.Task {
	issues := ExtractIssues(raw)
	tasks := make([]wf.Task, 0, len(issues))
	for _, issue := range issues {
		tasks = append(tasks, NormalizeIssue(issue))
	}
	return tasks
}

// ExtractIssues pulls an issue array out of whatever envelope kata used. It
// handles a bare array, a single object, and the common wrapper keys so the
// first live run degrades to a fixable error rather than a crash.
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
			return []map[string]any{issue}
		}
		// A single issue object, identified by carrying an id-ish field.
		for _, key := range []string{"id", "ulid", "short_id"} {
			if _, ok := v[key]; ok {
				return []map[string]any{v}
			}
		}
	}
	return nil
}

// MetaObject unwraps a metadata response, which may be the object itself or
// nested under a metadata/meta key.
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

// NormalizeIssue is the single point of contact with kata's JSON shape.
// Field names are inferred, ordered by likelihood; fix them here after the
// first live run and nothing else in wf needs to change.
func NormalizeIssue(raw map[string]any) wf.Task {
	id := pickString(raw, "ulid", "id", "uid")

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
		Labels:   pickStrings(raw, "labels", "tags"),
		Owner:    pickString(raw, "owner", "assignee"),
		Meta:     MetaObject(pickAny(raw, "metadata", "meta")),
		Rev:      pickString(raw, "revision", "rev", "version"),
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

func pickString(obj map[string]any, keys ...string) string {
	if s, ok := pickAny(obj, keys...).(string); ok {
		return s
	}
	return ""
}

func pickNumber(obj map[string]any, keys ...string) (int, bool) {
	switch v := pickAny(obj, keys...).(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

func pickStrings(obj map[string]any, keys ...string) []string {
	list, ok := pickAny(obj, keys...).([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
