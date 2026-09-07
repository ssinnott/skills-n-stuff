package wf

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Applying a run's outcomes to the tracker. A run that reports DONE with
// something to show completes and leaves the task open for a human to run
// the next workflow or close; silence is never success, so a run that did
// not report DONE escalates instead.

const (
	// StateKey carries wf's state vocabulary.
	StateKey = "wf.state"
	// AttentionKey flags work for a human (kata's `work.attention` convention).
	AttentionKey = "work.attention"
	// RepoKey records the repo a REPO: outcome named.
	RepoKey = "wf.repo"
	// PRsKey records PR: outcomes as a JSON array of URLs, across every run.
	PRsKey = "wf.pr"
	// IssuesKey records ISSUE: outcomes as JSON, across every run.
	IssuesKey = "wf.issue"
	// DocsKey records the vault paths of produced documents, across every run.
	DocsKey = "wf.docs"
)

// IssueRecord is the persisted shape of one ISSUE: outcome.
type IssueRecord struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// PRsFromMeta reads recorded PR urls; garbage or absent metadata reads as empty.
func PRsFromMeta(meta map[string]any) []string {
	return stringsFromMeta(meta[PRsKey])
}

// DocsFromMeta reads the produced documents recorded on a task.
func DocsFromMeta(meta map[string]any) []string {
	return stringsFromMeta(meta[DocsKey])
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

func stringsFromMeta(raw any) []string {
	data, ok := metaJSONBytes(raw)
	if !ok {
		return nil
	}
	var out []string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// metaJSONBytes normalizes a metadata value into JSON bytes.
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

// setJSONMeta writes a list-valued key.
func setJSONMeta(ctx context.Context, q Queue, task Task, key string, value any, what string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s for %s: %w", what, task.ShortID, err)
	}
	if err := q.SetMeta(ctx, task.ID, key, string(encoded), SetMetaOptions{JSON: true}); err != nil {
		return fmt.Errorf("record %s on %s: %w", what, task.ShortID, err)
	}
	return nil
}

// recordRunFacts persists what a run reported about itself regardless of
// how it settles: an escalated run that opened a PR leaves that trace here.
// Lists merge with what earlier runs recorded, since a task accumulates
// output across every workflow run against it.
func recordRunFacts(ctx context.Context, q Queue, task Task, outcomes []Outcome) error {
	if repo := ReportedRepo(outcomes); repo != "" {
		if err := q.SetMeta(ctx, task.ID, RepoKey, repo, SetMetaOptions{}); err != nil {
			return fmt.Errorf("record repo on %s: %w", task.ShortID, err)
		}
	}

	prs := PRsFromMeta(task.Meta)
	added := false
	for _, o := range outcomes {
		if o.Verb == VerbPR {
			before := len(prs)
			prs = appendUnique(prs, o.URL)
			added = added || len(prs) > before
		}
	}
	if added {
		if err := setJSONMeta(ctx, q, task, PRsKey, prs, "PR urls"); err != nil {
			return err
		}
	}

	issues, _ := Spawned(outcomes)
	if len(issues) > 0 {
		records := IssuesFromMeta(task.Meta)
		for _, o := range issues {
			if !hasIssue(records, o.URL) {
				records = append(records, IssueRecord{URL: o.URL, Title: o.Title})
			}
		}
		if err := setJSONMeta(ctx, q, task, IssuesKey, records, "filed issues"); err != nil {
			return err
		}
	}

	return nil
}

func hasIssue(records []IssueRecord, url string) bool {
	for _, r := range records {
		if r.URL == url {
			return true
		}
	}
	return false
}

// recordDocs adds the documents a run bound into the vault to the task's list.
func recordDocs(ctx context.Context, q Queue, task Task, bound []BoundDoc) error {
	if len(bound) == 0 {
		return nil
	}
	docs := DocsFromMeta(task.Meta)
	for _, b := range bound {
		docs = appendUnique(docs, b.VaultPath)
	}
	return setJSONMeta(ctx, q, task, DocsKey, docs, "documents")
}

// ApplyResult reports what a run's outcomes did to the task.
type ApplyResult struct {
	// Completed means the run reported DONE with something to show; the
	// task stays open, in review, for a human's next move.
	Completed bool
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
	// Bind configures artifact binding; a zero value disables it.
	Bind BindOptions
}

// SetState records wf's work state, mirroring needs-human into AttentionKey.
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
	_ = q.UnsetMeta(ctx, ref, AttentionKey)
	return nil
}

// Escalate parks a task for a human with a reason and a transcript excerpt.
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

// Apply turns a settled run into tracker state: a complete run records what
// it produced and files follow-on work, then leaves the task in review; an
// incomplete one escalates. Closing is a human's call — see Close.
func Apply(
	ctx context.Context,
	q Queue,
	task Task,
	outcomes []Outcome,
	opts ApplyOptions,
) (ApplyResult, error) {
	// Publication happens before the completeness check: an escalated run
	// may still have opened a PR.
	if err := recordRunFacts(ctx, q, task, outcomes); err != nil {
		return ApplyResult{}, err
	}

	if len(outcomes) == 0 {
		return Escalate(ctx, q, task, "the run reported no outcomes", opts.Transcript)
	}
	if !IsComplete(outcomes) {
		return Escalate(ctx, q, task, "the run ended without DONE", opts.Transcript)
	}
	if !HasOutput(outcomes) {
		return Escalate(ctx, q, task,
			"the run reported DONE but produced nothing — no pull request, document, or issue",
			opts.Transcript)
	}

	result := ApplyResult{}
	_, next := Spawned(outcomes)

	// Artifacts move into the vault before the summary is written, so the
	// summary can cite their final location.
	bound, err := BindArtifacts(ctx, q, task, outcomes, opts.Bind)
	if err != nil {
		return result, err
	}
	result.Bound = bound
	if err := recordDocs(ctx, q, task, bound); err != nil {
		return result, err
	}

	if summary := runSummary(outcomes, bound); summary != "" {
		if err := q.Comment(ctx, task.ID, summary); err != nil {
			return result, fmt.Errorf("comment outcomes on %s: %w", task.ShortID, err)
		}
	}

	// NEXT materializes follow-on work as a sibling, deliberately not launched.
	for _, o := range next {
		created, err := q.Create(ctx, CreateInput{
			Title:          o.Text,
			Body:           followOnBody(task, outcomes, bound),
			RelatedTo:      task.ID,
			IdempotencyKey: fmt.Sprintf("wf-next-%s-%s", task.ID, slugKey(o.Text)),
		})
		if err != nil {
			return result, fmt.Errorf("create follow-on task for %s: %w", task.ShortID, err)
		}
		result.Created = append(result.Created, created.ShortID)
	}

	if err := SetState(ctx, q, task.ID, StateReview); err != nil {
		return result, err
	}

	result.Completed = true
	return result, nil
}

// Evidence gathers what the task's runs recorded on the tracker row: the
// pull requests and vault documents that a close cites. Filed issues are
// deliberately absent, since they say work moved elsewhere.
func Evidence(task Task) CloseResult {
	return CloseResult{
		PRs:  PRsFromMeta(task.Meta),
		Docs: DocsFromMeta(task.Meta),
	}
}

// Close is the human's terminal transition: it closes the task with the
// evidence its runs accumulated, refusing when there is none. message may
// be empty, in which case one is composed from the evidence.
func Close(ctx context.Context, q Queue, task Task, message string) (CloseResult, error) {
	result := Evidence(task)
	if !result.HasEvidence() {
		return result, fmt.Errorf("%s has no pull request or document recorded to close with; run a workflow that produces one, or close it in the tracker directly", taskRef(task))
	}
	result.Message = closeMessage(task, message, result)

	if err := q.Close(ctx, task.ID, result); err != nil {
		return result, fmt.Errorf("close %s: %w", task.ShortID, err)
	}
	if err := SetState(ctx, q, task.ID, StateDone); err != nil {
		return result, err
	}
	return result, nil
}

// runSummary renders what the run produced as one comment. Documents are
// named at their final location, so a reader can open them.
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

// MinCloseMessage is the shortest close message kata's `close --done` accepts.
const MinCloseMessage = 40

// closeMessage composes the substance a close needs from what wf actually
// knows, rather than padding a short message with filler.
func closeMessage(task Task, message string, result CloseResult) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Closed by wf close"
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
	return strings.Join(parts, ". ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// finalPath maps an agent-reported path to where the file ended up.
func finalPath(reported string, bound []BoundDoc) string {
	for _, b := range bound {
		if b.Source == reported {
			return b.VaultPath
		}
	}
	return reported
}

// followOnBody carries the parent's artifacts into the new task.
func followOnBody(task Task, outcomes []Outcome, bound []BoundDoc) string {
	body := fmt.Sprintf("Follow-on from %s: %s\n", task.ShortID, task.Title)
	if summary := runSummary(outcomes, bound); summary != "" {
		body += "\n" + summary + "\n"
	}
	return body
}

// sessionHint points an escalated task's comment at `wf attach`, naming only
// the ref: sessions are machine-local, in a ledger this package never reads.
func sessionHint(task Task) string {
	return fmt.Sprintf("Session: `wf attach %s`", taskRef(task))
}

// taskRef is what a human types for a task: the short id when it has one.
func taskRef(task Task) string {
	if task.ShortID != "" {
		return task.ShortID
	}
	return task.ID
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
