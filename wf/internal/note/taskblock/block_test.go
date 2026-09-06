package taskblock

import (
	"strings"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func ts(h, m int) time.Time {
	return time.Date(2026, 3, 4, h, m, 0, 0, time.UTC)
}

func ended(h, m int) *time.Time {
	t := ts(h, m)
	return &t
}

// twoRuns is the design's own example, built as a record: a first run that
// escalated after producing a plan, and a re-run that shipped a PR.
func twoRuns() wf.Record {
	return wf.Record{
		ID:      "01M1S",
		Updated: ts(12, 0),
		Created: ts(9, 0),
		Runs: []wf.Run{
			{
				ID: "run-1", Workflow: "plan-to-pr", Profile: "coding", Model: "sonnet-5",
				Host: "wf-laptop", Started: ts(10, 0), Ended: ended(10, 30),
				Outcome: wf.SessionEscalated,
			},
			{
				ID: "run-2", Workflow: "plan-to-pr", Profile: "coding", Model: "opus-5",
				Host: "wf-laptop", Started: ts(11, 48), Ended: ended(11, 58),
				Outcome: wf.SessionDone,
			},
		},
		Bindings: wf.Bindings{
			{
				Kind: wf.KindWorkspace, Ref: "/Users/me/.wf/worktrees/neck-add-parser",
				State: wf.BindingSuperseded, At: ts(10, 0), Via: "run-1", Host: "wf-laptop",
				Meta: map[string]string{wf.MetaBranch: "wf/neck-add-parser"},
			},
			{
				Kind: wf.KindDoc, Ref: "Research/parser-plan.md",
				At: ts(10, 25), Via: "run-1",
				Meta: map[string]string{wf.MetaStore: "vault"},
			},
			{
				Kind: wf.KindWorkspace, Ref: "/Users/me/.wf/worktrees/neck-add-parser-2",
				State: wf.BindingLive, At: ts(11, 48), Via: "run-2", Host: "wf-laptop",
			},
			{
				Kind: wf.KindPR, Ref: "https://github.com/me/app/pull/412",
				State: wf.BindingLive, At: ts(11, 57), Via: "run-2",
			},
		},
	}
}

// The block the design specifies, rendered exactly. Worth pinning as one
// string rather than as assertions about substrings: the whole claim is
// that a human reads this, so the shape is the contract.
func TestRenderBlockMatchesTheDesign(t *testing.T) {
	want := strings.Join([]string{
		"%% wf:begin %%",
		"- **run 2** · plan-to-pr · opus-5 · done · 12m ago",
		"  - PR [#412](https://github.com/me/app/pull/412) — open",
		"  - worktree `neck-add-parser-2` — live *(wf-laptop)*",
		"- **run 1** · plan-to-pr · sonnet-5 · escalated · 2h ago",
		"  - [[Research/parser-plan]]",
		"  - worktree `neck-add-parser` — superseded *(wf-laptop)*",
		"%% wf:end %%",
	}, "\n")

	if got := RenderBlock(twoRuns()); got != want {
		t.Errorf("RenderBlock() =\n%s\n\nwant:\n%s", got, want)
	}
}

// The note lives in a synced vault, so an unchanged record must render the
// same bytes however often the plugin calls this.
func TestRenderBlockIsStable(t *testing.T) {
	rec := twoRuns()
	first := RenderBlock(rec)
	time.Sleep(2 * time.Millisecond) // any wall-clock dependency shows up here
	if second := RenderBlock(rec); first != second {
		t.Errorf("re-render differs:\n%s\n\nvs\n%s", first, second)
	}
}

// Ages are measured against the record's clock, so the passage of real
// time between syncs is not itself a diff.
func TestAgesAreAnchoredToTheRecord(t *testing.T) {
	rec := twoRuns()
	rec.Updated = ts(23, 0)

	got := RenderBlock(rec)
	if !strings.Contains(got, "· 11h ago") {
		t.Errorf("run 2 age did not move with the record's clock:\n%s", got)
	}
	if !strings.Contains(got, "· 13h ago") {
		t.Errorf("run 1 age did not move with the record's clock:\n%s", got)
	}
}

func TestAgeOmittedWithoutAClock(t *testing.T) {
	rec := wf.Record{ID: "01M1S", Runs: []wf.Run{{ID: "run-1", Workflow: "research"}}}
	want := "%% wf:begin %%\n- **run 1** · research · running\n%% wf:end %%"
	if got := RenderBlock(rec); got != want {
		t.Errorf("RenderBlock() =\n%s\nwant:\n%s", got, want)
	}
}

// A superseded checkout and a merged PR are the record of what happened.
// Hiding them would undo Supersede, whose entire purpose is keeping an
// escalated run's checkout findable.
func TestDeadBindingsAreShownWithTheirState(t *testing.T) {
	rec := wf.Record{
		ID:      "01M1S",
		Updated: ts(12, 0),
		Runs:    []wf.Run{{ID: "run-1", Started: ts(11, 0), Ended: ended(11, 30), Outcome: wf.SessionDone}},
		Bindings: wf.Bindings{
			{Kind: wf.KindPR, Ref: "https://github.com/me/app/pull/9", State: wf.BindingMerged, At: ts(11, 20), Via: "run-1"},
			{Kind: wf.KindWorkspace, Ref: "/w/gone", State: wf.BindingDisposed, At: ts(11, 0), Via: "run-1", Host: "wf-desktop"},
			{Kind: wf.KindSession, Ref: "/s/neck-a1b2.jsonl", State: wf.BindingMissing, At: ts(11, 0), Via: "run-1", Host: "wf-desktop"},
		},
	}

	got := RenderBlock(rec)
	for _, want := range []string{
		"  - PR [#9](https://github.com/me/app/pull/9) — merged",
		"  - worktree `gone` — disposed *(wf-desktop)*",
		"  - session `neck-a1b2` — missing *(wf-desktop)*",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A machine-local path read on another device is a dead path; the same
// path annotated with its host is a fact about another machine.
func TestMachineLocalBindingsCarryTheirHost(t *testing.T) {
	rec := wf.Record{
		ID:      "01M1S",
		Updated: ts(12, 0),
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/w/neck", State: wf.BindingLive, At: ts(11, 0), Host: "wf-laptop"},
			{Kind: wf.KindDoc, Ref: "Research/plan.md", At: ts(11, 0)},
		},
	}

	got := RenderBlock(rec)
	if !strings.Contains(got, "worktree `neck` — live *(wf-laptop)*") {
		t.Errorf("host annotation missing:\n%s", got)
	}
	// A vault document is not machine-local and must not be labelled as if
	// it were.
	if strings.Contains(got, "[[Research/plan]] *(") {
		t.Errorf("a doc was annotated with a host:\n%s", got)
	}
}

func TestBindingsWithNoRunAreTheTasksOwn(t *testing.T) {
	rec := wf.Record{
		ID:      "01M1S",
		Updated: ts(12, 0),
		Runs:    []wf.Run{{ID: "run-1", Workflow: "plan-to-pr", Started: ts(11, 0)}},
		Bindings: wf.Bindings{
			{Kind: wf.KindQueue, Ref: "01M1SQUEUE", At: ts(9, 0), Meta: map[string]string{wf.MetaBackend: "kata"}},
			{Kind: wf.KindRepo, Ref: "~/code/app", At: ts(9, 0), Host: "wf-laptop"},
			{Kind: wf.KindWorkspace, Ref: "/w/neck", State: wf.BindingLive, At: ts(11, 0), Via: "run-1", Host: "wf-laptop"},
		},
	}

	want := strings.Join([]string{
		"%% wf:begin %%",
		"- **task**",
		"  - repo `~/code/app` *(wf-laptop)*",
		"  - queue `kata 01M1SQUEUE`",
		"- **run 1** · plan-to-pr · running · 1h ago",
		"  - worktree `neck` — live *(wf-laptop)*",
		"%% wf:end %%",
	}, "\n")

	if got := RenderBlock(rec); got != want {
		t.Errorf("RenderBlock() =\n%s\nwant:\n%s", got, want)
	}
}

// Evidence tagged with a run the ledger lost is still evidence.
func TestBindingsFromAnUnknownRunAreNotDropped(t *testing.T) {
	rec := wf.Record{
		ID:       "01M1S",
		Updated:  ts(12, 0),
		Bindings: wf.Bindings{{Kind: wf.KindDoc, Ref: "Notes/found.md", At: ts(11, 0), Via: "run-gone"}},
	}

	got := RenderBlock(rec)
	if !strings.Contains(got, "- **run** `run-gone` · run record missing") {
		t.Errorf("orphaned run group missing:\n%s", got)
	}
	if !strings.Contains(got, "[[Notes/found]]") {
		t.Errorf("orphaned binding was dropped:\n%s", got)
	}
}

func TestEmptyRecordSaysSo(t *testing.T) {
	want := "%% wf:begin %%\n" + emptyBlock + "\n%% wf:end %%"
	if got := RenderBlock(wf.Record{ID: "01M1S"}); got != want {
		t.Errorf("RenderBlock() =\n%s\nwant:\n%s", got, want)
	}
}

func TestDocLinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		bind wf.Binding
		want string
	}{
		{"strips .md", wf.Binding{Kind: wf.KindDoc, Ref: "Research/parser-plan.md"}, "[[Research/parser-plan]]"},
		{"keeps a bare name", wf.Binding{Kind: wf.KindDoc, Ref: "Research/parser-plan"}, "[[Research/parser-plan]]"},
		{"strips ./", wf.Binding{Kind: wf.KindDoc, Ref: "./Research/plan.md"}, "[[Research/plan]]"},
		{"uses the basename of an absolute path", wf.Binding{Kind: wf.KindDoc, Ref: "/Users/me/vault/Research/plan.md"}, "[[plan]]"},
		{"aliases a label", wf.Binding{Kind: wf.KindDoc, Ref: "Research/plan.md", Label: "Parser plan"}, "[[Research/plan|Parser plan]]"},
		{"drops a redundant label", wf.Binding{Kind: wf.KindDoc, Ref: "Research/plan.md", Label: "plan"}, "[[Research/plan]]"},
		{"case-insensitive extension", wf.Binding{Kind: wf.KindDoc, Ref: "Research/plan.MD"}, "[[Research/plan]]"},
	} {
		if got := bindingSubject(tc.bind); got != tc.want {
			t.Errorf("%s: bindingSubject() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPRSubjects(t *testing.T) {
	for _, tc := range []struct {
		name string
		bind wf.Binding
		want string
	}{
		{
			"github url",
			wf.Binding{Kind: wf.KindPR, Ref: "https://github.com/me/app/pull/412"},
			"PR [#412](https://github.com/me/app/pull/412)",
		},
		{
			"trailing slash",
			wf.Binding{Kind: wf.KindPR, Ref: "https://github.com/me/app/pull/412/"},
			"PR [#412](https://github.com/me/app/pull/412/)",
		},
		{
			// No scheme means no address wf was actually told; inventing
			// https:// would be fabricating a link.
			"unlinkable ref falls back to text",
			wf.Binding{Kind: wf.KindPR, Ref: "github.com/me/app#412"},
			"PR `#412`",
		},
		{
			"label when no number is derivable",
			wf.Binding{Kind: wf.KindPR, Ref: "https://example.com/review/abc", Label: "Add the parser"},
			"PR [Add the parser](https://example.com/review/abc)",
		},
	} {
		if got := bindingSubject(tc.bind); got != tc.want {
			t.Errorf("%s: bindingSubject() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestOtherKindSubjects(t *testing.T) {
	for _, tc := range []struct {
		name string
		bind wf.Binding
		want string
	}{
		{
			"session prefers the runner's own id over its path",
			wf.Binding{Kind: wf.KindSession, Ref: "/Users/me/.wf/sessions/2026-03-04-neck.jsonl",
				Meta: map[string]string{wf.MetaSessionID: "neck-a1b2"}},
			"session `neck-a1b2`",
		},
		{
			"session falls back to the file name",
			wf.Binding{Kind: wf.KindSession, Ref: "/Users/me/.wf/sessions/neck-a1b2.jsonl"},
			"session `neck-a1b2`",
		},
		{
			"review links its pane",
			wf.Binding{Kind: wf.KindReview, Ref: "http://localhost:4980", Label: "difit",
				Meta: map[string]string{wf.MetaPort: "4980"}},
			"review [difit :4980](http://localhost:4980)",
		},
		{
			"queue names its backend",
			wf.Binding{Kind: wf.KindQueue, Ref: "01M1SQ9F2", Meta: map[string]string{wf.MetaBackend: "kata"}},
			"queue `kata 01M1SQ9F2`",
		},
		{
			"a sibling task carries its relation and title",
			wf.Binding{Kind: wf.KindTask, Ref: "01M1TB2", Label: "Wire the parser into the CLI",
				Meta: map[string]string{wf.MetaRelation: "next"}},
			"next task `01M1TB2` · Wire the parser into the CLI",
		},
		{
			// An unmodelled kind is still rendered: a reader seeing a
			// ref they do not recognize beats a binding that vanished.
			"an unknown kind still renders",
			wf.Binding{Kind: wf.Kind("dataset"), Ref: "s3://bucket/thing"},
			"dataset `s3://bucket/thing`",
		},
	} {
		if got := bindingSubject(tc.bind); got != tc.want {
			t.Errorf("%s: bindingSubject() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestApplyBlockCreatesFrontmatter(t *testing.T) {
	got := ApplyBlock("# Add the parser\n\nMy own notes here.\n", twoRuns())

	if !strings.HasPrefix(got, "---\nwf-task: 01M1S\n---\n") {
		t.Errorf("frontmatter was not created:\n%s", got)
	}
	if ReadTaskID(got) != "01M1S" {
		t.Errorf("ReadTaskID() = %q, want 01M1S", ReadTaskID(got))
	}
	if !strings.Contains(got, "My own notes here.") {
		t.Errorf("the human's text was lost:\n%s", got)
	}
	if !strings.Contains(got, RenderBlock(twoRuns())) {
		t.Errorf("block missing:\n%s", got)
	}
}

func TestApplyBlockAppendsBelowExistingFrontmatter(t *testing.T) {
	note := "---\nstatus: draft\ntags: [work]\n---\n# Add the parser\n\nNotes.\n"
	got := ApplyBlock(note, twoRuns())

	for _, want := range []string{"status: draft", "tags: [work]", "wf-task: 01M1S", "# Add the parser", "Notes."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, RenderBlock(twoRuns())+"\n") {
		t.Errorf("block was not appended at the end:\n%s", got)
	}
}

func TestApplyBlockReplacesOnlyTheBlock(t *testing.T) {
	note := ApplyBlock("---\nwf-task: 01M1S\n---\n# Add the parser\n\nMine.\n", wf.Record{ID: "01M1S"})
	got := ApplyBlock(note, twoRuns())

	if strings.Contains(got, emptyBlock) {
		t.Errorf("the stale block survived:\n%s", got)
	}
	if n := strings.Count(got, BeginMarker); n != 1 {
		t.Errorf("block appears %d times, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "Mine.") {
		t.Errorf("the human's text was lost:\n%s", got)
	}
}

func TestApplyBlockPreservesTextAfterTheBlock(t *testing.T) {
	note := "---\nwf-task: 01M1S\n---\n# Task\n\nBefore.\n\n" +
		"%% wf:begin %%\n- stale\n%% wf:end %%\n\nAfter, written by hand.\n"
	got := ApplyBlock(note, twoRuns())

	if !strings.Contains(got, "Before.") || !strings.Contains(got, "After, written by hand.") {
		t.Errorf("text around the block was lost:\n%s", got)
	}
	if strings.Contains(got, "- stale") {
		t.Errorf("the old block content survived:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\nAfter, written by hand.\n") {
		t.Errorf("the tail moved:\n%s", got)
	}
}

// The malformed case, and the one with a real cost: an unterminated marker
// must not license deleting everything below it.
func TestUnterminatedBlockEatsNothing(t *testing.T) {
	note := "---\nwf-task: 01M1S\n---\n# Task\n\n%% wf:begin %%\n- half-written\n\nMy own paragraph.\n"
	got := ApplyBlock(note, twoRuns())

	for _, want := range []string{"- half-written", "My own paragraph.", BeginMarker} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q after applying to an unterminated block:\n%s", want, got)
		}
	}
	if !strings.HasSuffix(got, RenderBlock(twoRuns())+"\n") {
		t.Errorf("no fresh block was appended:\n%s", got)
	}

	// And the recovery must settle: the second apply rewrites the block it
	// added rather than pairing the orphan marker with the new end marker,
	// which is exactly the deletion this guards against.
	twice := ApplyBlock(got, twoRuns())
	if twice != got {
		t.Errorf("apply after recovery was not idempotent:\n%s\n\nvs\n%s", got, twice)
	}
	if !strings.Contains(twice, "My own paragraph.") {
		t.Errorf("the second apply ate the human's text:\n%s", twice)
	}
}

// A stray end marker above the real block must not shadow it.
func TestStrayEndMarkerIsSkipped(t *testing.T) {
	note := "# Task\n\n%% wf:end %%\n\nMine.\n\n%% wf:begin %%\n- stale\n%% wf:end %%\n"
	got := ApplyBlock(note, twoRuns())

	if strings.Contains(got, "- stale") {
		t.Errorf("the real block was not replaced:\n%s", got)
	}
	if !strings.Contains(got, "Mine.") {
		t.Errorf("text under the stray marker was lost:\n%s", got)
	}
	if twice := ApplyBlock(got, twoRuns()); twice != got {
		t.Errorf("not idempotent with a stray marker present:\n%s\n\nvs\n%s", got, twice)
	}
}

// A marker named in a sentence is prose, not a delimiter.
func TestInlineMarkerMentionIsNotADelimiter(t *testing.T) {
	note := "# Task\n\nwf writes between %% wf:begin %% and %% wf:end %% markers.\n"
	got := ApplyBlock(note, twoRuns())

	if !strings.Contains(got, "wf writes between %% wf:begin %% and %% wf:end %% markers.") {
		t.Errorf("the sentence was rewritten:\n%s", got)
	}
	if !strings.HasSuffix(got, RenderBlock(twoRuns())+"\n") {
		t.Errorf("block was not appended:\n%s", got)
	}
}

// Applying twice is what the plugin does on every open. It must be a
// no-op on the second call, byte for byte, or every sync is a conflict.
func TestApplyBlockIsIdempotent(t *testing.T) {
	notes := map[string]string{
		"no frontmatter":      "# Add the parser\n\nNotes.\n",
		"frontmatter only":    "---\nstatus: draft\n---\n",
		"empty note":          "",
		"no trailing newline": "# Add the parser\n\nNotes.",
		"existing block":      "---\nwf-task: 01M1S\n---\n# T\n\n%% wf:begin %%\n- stale\n%% wf:end %%\n",
		"text after block":    "%% wf:begin %%\n- stale\n%% wf:end %%\n\nTail.\n",
		"crlf note":           "---\r\nstatus: draft\r\n---\r\n# T\r\n\r\nNotes.\r\n",
	}

	for _, rec := range []wf.Record{twoRuns(), {ID: "01M1S"}, {}} {
		for name, note := range notes {
			once := ApplyBlock(note, rec)
			twice := ApplyBlock(once, rec)
			if once != twice {
				t.Errorf("%s: not idempotent:\n%q\n\nvs\n%q", name, once, twice)
			}
		}
	}
}

// A record with no id has no join to write; stamping an empty field would
// claim a binding that does not exist.
func TestApplyBlockWithoutAnIDLeavesFrontmatterAlone(t *testing.T) {
	got := ApplyBlock("# Task\n", wf.Record{})
	if strings.Contains(got, TaskKey) {
		t.Errorf("an empty task id was written:\n%s", got)
	}
	if !strings.Contains(got, emptyBlock) {
		t.Errorf("block missing:\n%s", got)
	}
}

func TestReadTaskID(t *testing.T) {
	if got := ReadTaskID("# Note\n"); got != "" {
		t.Errorf("ReadTaskID() = %q on an unbound note, want empty", got)
	}
	if got := ReadTaskID("---\nwf-task: 01M1S\nkata-issue: 01HZNQ\n---\n"); got != "01M1S" {
		t.Errorf("ReadTaskID() = %q, want 01M1S", got)
	}
	// The tracker binding is a separate field and neither read disturbs
	// the other.
	if got := note.GetField("---\nwf-task: 01M1S\nkata-issue: 01HZNQ\n---\n", note.IssueKey); got != "01HZNQ" {
		t.Errorf("note.GetField(note.IssueKey) = %q, want 01HZNQ", got)
	}
}
