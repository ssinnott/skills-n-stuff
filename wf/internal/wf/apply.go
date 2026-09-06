package wf

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Applying a run's outcomes to the tracker.
//
// This is the only place a run's result becomes tracker state: silence is
// never success, so a run that did not report DONE escalates rather than
// closing.

const (
	// StateKey carries wf's state vocabulary, which no tracker in scope
	// models natively.
	StateKey = "wf.state"
	// AttentionKey flags work for a human. The name follows the convention
	// kata's own docs use (`--meta work.attention=needs-human`), so the
	// escalation queue is a plain list query in every tracker surface
	// rather than something only wf can see.
	AttentionKey = "work.attention"
	// RepoKey records the repo a REPO: outcome named, for `wf review` once
	// the worktree that ran the agent is gone.
	RepoKey = "wf.repo"
	// PRsKey records PR: outcomes as a JSON array of URLs. See DESIGN.md
	// for why this is wf's own metadata rather than a read of kata's
	// evidence trail.
	PRsKey = "wf.pr"
	// IssuesKey records ISSUE: outcomes as JSON, so `wf review` can still
	// turn anchored findings into comments after the run's transcript is
	// gone.
	IssuesKey = "wf.issue"
)

// IssueRecord is the persisted shape of one ISSUE: outcome — a URL and a
// title, exactly what the outcome protocol carries for it and no more.
type IssueRecord struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// PRsFromMeta reads recorded PR urls, if any. Garbage or absent metadata
// reads as empty rather than failing — a binding is how work is found
// again, never a lifecycle input, so bad metadata must cost a link, never a
// run.
func PRsFromMeta(meta map[string]any) []string {
	data, ok := metaJSONBytes(meta[PRsKey])
	if !ok {
		return nil
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// IssuesFromMeta reads recorded ISSUE outcomes, if any.
func IssuesFromMeta(meta map[string]any) []IssueRecord {
	data, ok := metaJSONBytes(meta[IssuesKey])
	if !ok {
		return nil
	}
	var out []IssueRecord
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// metaJSONBytes normalizes a metadata value into JSON bytes, whether the
// backend handed it back as an already-decoded string or re-marshaled it
// into some other JSON type.
func metaJSONBytes(raw any) ([]byte, bool) {
	if raw == nil {
		return nil, false
	}
	if s, ok := raw.(string); ok {
		return []byte(s), true
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	return b, true
}

// recordRunFacts persists what a run reported about itself — repo, PRs,
// filed issues — regardless of how the run settles. Recording these only on
// a clean close would lose exactly the runs `wf review` most needs to find:
// an escalated run that still opened a PR, or named its checkout, leaves
// that trace right here rather than nowhere.
func recordRunFacts(ctx context.Context, q Queue, task Task, outcomes []Outcome) error {
	if repo := ReportedRepo(outcomes); repo != "" {
		if err := q.SetMeta(ctx, task.ID, RepoKey, repo, SetMetaOptions{}); err != nil {
			return fmt.Errorf("record repo on %s: %w", task.ShortID, err)
		}
	}

	var prs []string
	for _, o := range outcomes {
		if o.Verb == VerbPR {
			prs = appendUnique(prs, o.URL)
		}
	}
	if len(prs) > 0 {
		encoded, err := json.Marshal(prs)
		if err != nil {
			return fmt.Errorf("encode PR urls for %s: %w", task.ShortID, err)
		}
		if err := q.SetMeta(ctx, task.ID, PRsKey, string(encoded), SetMetaOptions{JSON: true}); err != nil {
			return fmt.Errorf("record PR urls on %s: %w", task.ShortID, err)
		}
	}

	issues, _ := Spawned(outcomes)
	if len(issues) > 0 {
		records := make([]IssueRecord, 0, len(issues))
		for _, o := range issues {
			records = append(records, IssueRecord{URL: o.URL, Title: o.Title})
		}
		encoded, err := json.Marshal(records)
		if err != nil {
			return fmt.Errorf("encode filed issues for %s: %w", task.ShortID, err)
		}
		if err := q.SetMeta(ctx, task.ID, IssuesKey, string(encoded), SetMetaOptions{JSON: true}); err != nil {
			return fmt.Errorf("record filed issues on %s: %w", task.ShortID, err)
		}
	}

	return nil
}

// ApplyResult reports what a run's outcomes did to the task.
type ApplyResult struct {
	Closed    bool
	Escalated bool
	// Created holds refs of follow-on tasks materialized from NEXT.
	Created []string
	// Bound holds artifacts that landed in the Obsidian vault.
	Bound []BoundDoc
	// Reason explains an escalation.
	Reason string
}

// ApplyOptions carries what applying a run needs beyond its outcomes.
type ApplyOptions struct {
	// Transcript is the run's output, excerpted into escalation comments.
	Transcript string
	// IdempotencyKey makes a retried close a no-op rather than a second one.
	IdempotencyKey string
	// Bind configures artifact binding; a zero value disables it.
	Bind BindOptions
}

// SetState records wf's work state, mirroring needs-human into the
// attention key so the tracker's own surfaces can filter on it.
func SetState(ctx context.Context, q Queue, ref string, state WorkState) error {
	if err := q.SetMeta(ctx, ref, StateKey, string(state), SetMetaOptions{}); err != nil {
		return fmt.Errorf("set state on %s: %w", ref, err)
	}
	if state == StateNeedsHuman {
		if err := q.SetMeta(ctx, ref, AttentionKey, "needs-human", SetMetaOptions{}); err != nil {
			return fmt.Errorf("flag %s for attention: %w", ref, err)
		}
		return nil
	}
	// Usually already absent, which is not a failure.
	_ = q.UnsetMeta(ctx, ref, AttentionKey)
	return nil
}

// Escalate parks a task for a human with a reason, leaving it open. The
// transcript excerpt is included because an escalation a human cannot
// diagnose from the tracker is an escalation they have to go hunting for.
func Escalate(ctx context.Context, q Queue, task Task, reason, transcript string) (ApplyResult, error) {
	body := fmt.Sprintf("**Needs a human** — %s\n\n%s", reason, sessionHint(task))
	if excerpt := tail(transcript, 1500); excerpt != "" {
		body += fmt.Sprintf("\n\nLast output from the run:\n\n```\n%s\n```", excerpt)
	}

	if err := q.Comment(ctx, task.ID, body); err != nil {
		return ApplyResult{}, fmt.Errorf("comment escalation on %s: %w", task.ShortID, err)
	}
	if err := SetState(ctx, q, task.ID, StateNeedsHuman); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Escalated: true, Reason: reason}, nil
}

// Apply turns a settled run into tracker state.
//
// A complete run closes the task with the evidence the agent produced, files
// its follow-on work, and records what it made. An incomplete one escalates.
func Apply(
	ctx context.Context,
	q Queue,
	task Task,
	outcomes []Outcome,
	opts ApplyOptions,
) (ApplyResult, error) {
	// Publication happens before the completeness check, and for the same
	// reason recordSession is written at spawn: a run that escalated
	// without reporting DONE may still have opened a PR or named its
	// checkout, and that trace belongs on the task either way.
	if err := recordRunFacts(ctx, q, task, outcomes); err != nil {
		return ApplyResult{}, err
	}

	if len(outcomes) == 0 {
		return Escalate(ctx, q, task, "the run reported no outcomes", opts.Transcript)
	}
	if !IsComplete(outcomes) {
		return Escalate(ctx, q, task, "the run ended without DONE", opts.Transcript)
	}

	result := ApplyResult{}
	issues, next := Spawned(outcomes)

	// Artifacts move into the vault before the summary is written, so the
	// comment can name where documents actually ended up rather than where
	// the agent happened to write them.
	bound, err := BindArtifacts(ctx, q, task, outcomes, opts.Bind)
	if err != nil {
		return result, err
	}
	result.Bound = bound

	// A completion with no evidence escalates instead of closing; see
	// DESIGN.md.
	closing := closeResult(task, outcomes, bound)
	if !closing.HasEvidence() {
		return Escalate(ctx, q, task,
			"the run reported DONE but produced no evidence — no pull request, commit, document, or test",
			opts.Transcript)
	}

	// Record what the run produced before closing, so the narrative is on
	// the issue even if the close itself fails.
	if summary := runSummary(outcomes, bound); summary != "" {
		if err := q.Comment(ctx, task.ID, summary); err != nil {
			return result, fmt.Errorf("comment outcomes on %s: %w", task.ShortID, err)
		}
	}

	// NEXT materializes follow-on work as a sibling. It is deliberately not
	// launched: chains stay human-started and visible in the tracker.
	for _, o := range next {
		created, err := q.Create(ctx, CreateInput{
			Title:     o.Text,
			Body:      followOnBody(task, outcomes, bound),
			RelatedTo: task.ID,
			Meta:      map[string]string{"wf.origin": task.ID},
			// Keyed on the parent and the text so a retried run does not
			// file the same follow-up twice.
			IdempotencyKey: fmt.Sprintf("wf-next-%s-%s", task.ID, slugKey(o.Text)),
		})
		if err != nil {
			return result, fmt.Errorf("create follow-on task for %s: %w", task.ShortID, err)
		}
		result.Created = append(result.Created, created.ShortID)
	}

	// Filed issues never gate completion; see DESIGN.md.
	_ = issues

	if err := q.Close(ctx, task.ID, closing, opts.IdempotencyKey); err != nil {
		return result, fmt.Errorf("close %s: %w", task.ShortID, err)
	}
	if err := SetState(ctx, q, task.ID, StateDone); err != nil {
		return result, err
	}

	result.Closed = true
	return result, nil
}

// runSummary renders what the run produced as one comment rather than
// several: a task's comment thread should read as a narrative, not a log.
// Documents are named at their final location, so a reader can open them.
func runSummary(outcomes []Outcome, bound []BoundDoc) string {
	var lines []string

	for _, o := range outcomes {
		switch o.Verb {
		case VerbPR:
			lines = append(lines, "- PR: "+link(o.URL, o.Title))
		case VerbIssue:
			lines = append(lines, "- Filed: "+link(o.URL, o.Title))
		case VerbDoc:
			lines = append(lines, "- Document: "+pathLabel(finalPath(o.Path, bound), o.Title))
		case VerbNext:
			lines = append(lines, "- Follow-on: "+o.Text)
		case VerbRepo:
			lines = append(lines, "- Repo: "+o.Path)
		case VerbDone:
			if o.Path != "" {
				lines = append(lines, "- Review: "+finalPath(o.Path, bound))
			}
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return "Run produced:\n\n" + strings.Join(lines, "\n")
}

// MinCloseMessage is the shortest close message kata's `close --done`
// accepts.
const MinCloseMessage = 40

// closeResult records evidence at final locations, so a closed task does not
// cite a path inside a disposed worktree, and makes the message substantive
// enough for a tracker that demands one.
func closeResult(task Task, outcomes []Outcome, bound []BoundDoc) CloseResult {
	result := ToCloseResult(outcomes)
	for i, doc := range result.Docs {
		result.Docs[i] = finalPath(doc, bound)
	}
	result.Message = closeMessage(task, result)
	return result
}

// closeMessage composes the substance a close needs. Where the agent wrote
// enough, its words stand. Where it did not, wf adds what it actually knows —
// the task and the evidence produced — rather than padding with filler, and
// says plainly when nothing was produced at all.
func closeMessage(task Task, result CloseResult) string {
	message := strings.TrimSpace(result.Message)
	if message == "" {
		message = "Completed by agent"
	}
	if len(message) >= MinCloseMessage {
		return message
	}

	parts := []string{message}
	if task.Title != "" {
		parts = append(parts, "Task: "+task.Title)
	}
	if n := len(result.PRs); n > 0 {
		parts = append(parts, fmt.Sprintf("%s: %s", plural(n, "pull request"), strings.Join(result.PRs, ", ")))
	}
	if n := len(result.Docs); n > 0 {
		parts = append(parts, fmt.Sprintf("%s: %s", plural(n, "document"), strings.Join(result.Docs, ", ")))
	}
	if n := len(result.Tests); n > 0 {
		parts = append(parts, fmt.Sprintf("%s: %s", plural(n, "check"), strings.Join(result.Tests, ", ")))
	}

	composed := strings.Join(parts, ". ")
	if len(composed) < MinCloseMessage {
		composed += ". Closed on the agent's DONE report; no pull request, document, or test evidence was produced."
	}
	return composed
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// finalPath maps an agent-reported path to where the file actually ended
// up, leaving unbound paths alone.
func finalPath(reported string, bound []BoundDoc) string {
	for _, b := range bound {
		if b.Source == reported {
			return b.VaultPath
		}
	}
	return reported
}

// followOnBody carries the parent's artifacts into the new task, so a
// follow-up starts with the links its predecessor produced.
func followOnBody(task Task, outcomes []Outcome, bound []BoundDoc) string {
	body := fmt.Sprintf("Follow-on from %s: %s\n", task.ShortID, task.Title)
	if summary := runSummary(outcomes, bound); summary != "" {
		body += "\n" + summary + "\n"
	}
	return body
}

// sessionHint points an escalated task's comment at `wf attach`. A session
// is machine-local and lives only in the local ledger, which this package
// never reads, so it cannot say whether one was actually bound — only the
// ref, which is all `wf attach` needs to look. If no session was ever
// recorded, `wf attach` says so.
func sessionHint(task Task) string {
	ref := task.ShortID
	if ref == "" {
		ref = task.ID
	}
	return fmt.Sprintf("Session: `wf attach %s`", ref)
}

func link(url, title string) string {
	if title == "" {
		return url
	}
	return fmt.Sprintf("[%s](%s)", title, url)
}

func pathLabel(path, title string) string {
	if title == "" {
		return path
	}
	return fmt.Sprintf("%s — %s", path, title)
}

// tail returns the last n characters, cut at a line boundary so an excerpt
// does not start mid-word.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
		cut = cut[i+1:]
	}
	return "…\n" + cut
}

// slugKey builds a stable idempotency component from free text.
func slugKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
