// Package taskblock renders a wf task record into an Obsidian note.
//
// It sits under note/ rather than in it because internal/wf imports
// internal/note (artifact.go binds a produced document by writing
// frontmatter), so a renderer in package note that imports wf.Record would
// close an import cycle. A leaf package that may depend on both is the
// cheap way through; the honest fix is for that write to stop pointing
// downhill, and then this collapses into package note unchanged.
package taskblock

// The managed block: a task record projected into an Obsidian note.
//
// The note is the face, wf is still the record. Everything here is a pure
// string transform over wf.Record — no filesystem, no vault, no Obsidian —
// because the projection is the part worth testing and the deleted
// pi-tasks plugin proved that: its docbind.ts had no Obsidian imports and
// a real test suite, and that is the half of it worth keeping.
//
// Three properties do the work, and each is load-bearing rather than
// stylistic:
//
//   - The block is regenerated wholesale between two delimiters and
//     everything outside them belongs to the human. wf is a third writer
//     into a file Obsidian and an agent also touch, so it writes a region
//     it owns entirely rather than merging into prose it does not.
//   - Machine-local bindings are annotated with the host that owns them. A
//     vault syncs across devices; a bare `~/.wf/worktrees/...` line read on
//     a phone is a dead path, while the same line marked *(wf-laptop)* is a
//     true statement about another machine.
//   - Nothing in the output moves unless the record moved. The note lives
//     in a synced vault, so a re-render of an unchanged task must produce
//     the same bytes: ordering is total, and ages are measured against the
//     record's own clock rather than the wall clock (see renderAnchor).

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/note"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

const (
	// TaskKey is the frontmatter field naming the bound task, re-exported
	// so a caller rendering a block needs one import.
	TaskKey = note.TaskKey

	// BeginMarker and EndMarker delimit the region wf owns. They are
	// Obsidian comments, so the block is invisible in reading view and a
	// note carrying one still reads as prose.
	BeginMarker = "%% wf:begin %%"
	EndMarker   = "%% wf:end %%"

	// emptyBlock is what a task with no history renders as. Saying so
	// beats an empty region that reads as a rendering failure.
	emptyBlock = "_No runs or bindings yet._"
)

// RenderBlock renders a record as the managed block, delimiters included
// and with no trailing newline. Delimiters are part of the output because
// they are part of what wf owns: a caller that had to add them could put
// the region's boundary in the wrong place, and ApplyBlock's replacement
// span is defined by exactly these two lines.
func RenderBlock(rec wf.Record) string {
	var b strings.Builder
	b.WriteString(BeginMarker)
	for _, line := range blockLines(rec) {
		b.WriteString("\n")
		b.WriteString(line)
	}
	b.WriteString("\n")
	b.WriteString(EndMarker)
	return b.String()
}

// ApplyBlock writes the record into a note: the task id into frontmatter,
// the bindings into the managed block. Applying twice is byte-identical to
// applying once, which is what lets the plugin call it on every open
// without dirtying a synced file.
func ApplyBlock(noteText string, rec wf.Record) string {
	text := noteText
	// An id-less record has no join to write. Stamping an empty field
	// would claim a binding that does not exist.
	if rec.ID != "" {
		text = note.SetField(text, TaskKey, rec.ID)
	}

	block := RenderBlock(rec)
	if start, end, ok := findBlock(text); ok {
		return text[:start] + block + text[end:]
	}
	return appendBlock(text, block)
}

// ReadTaskID is the reverse join: which task this note is bound to.
func ReadTaskID(noteText string) string { return note.GetField(noteText, TaskKey) }

// Markers are matched as whole lines: a delimiter mentioned mid-sentence
// must not be able to redefine the region wf overwrites.
var (
	beginRe = regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(BeginMarker) + `[ \t]*\r?$`)
	endRe   = regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(EndMarker) + `[ \t]*\r?$`)
)

// findBlock locates the managed region: the first end marker, paired with
// the *last* begin marker before it.
//
// Pairing the closest begin rather than the first one is the whole safety
// rule for a malformed note. An unterminated `%% wf:begin %%` left behind
// by a crashed write or a hand-edit would otherwise swallow every line
// between it and the next end marker — including the human's own prose and
// a later, well-formed block. With this rule an orphan marker is inert: it
// is never a region boundary, it is never deleted (wf does not get to
// decide the human's text is garbage), and it stays visible so whoever
// left it can see it. A stray end marker with no begin above it is skipped
// for the same reason, and the next end marker is tried instead.
func findBlock(text string) (start, end int, ok bool) {
	begins := beginRe.FindAllStringIndex(text, -1)
	ends := endRe.FindAllStringIndex(text, -1)

	for _, e := range ends {
		best := -1
		for _, b := range begins {
			if b[0] >= e[0] {
				break
			}
			best = b[0]
		}
		if best >= 0 {
			return best, e[1], true
		}
	}
	return 0, 0, false
}

// appendBlock puts a fresh block at the end of the note, separated by one
// blank line. This is also the recovery path for a malformed note: a new,
// properly terminated block is added and nothing existing is removed, so
// the worst case of a broken delimiter is a duplicate region a human can
// delete rather than lost writing.
func appendBlock(text, block string) string {
	body := strings.TrimRight(text, "\n")
	if body == "" {
		return block + "\n"
	}
	return body + "\n\n" + block + "\n"
}

// blockLines renders the body: the task's own bindings first, then runs
// newest first with what each produced indented beneath it.
func blockLines(rec wf.Record) []string {
	anchor := renderAnchor(rec)
	var lines []string

	// Bindings with no run are the task's own — an adopted PR, a note
	// bound by hand, the tracker row. They lead because they are context
	// for every run below, matching how `wf show` stacks them.
	if own := rec.Bindings.From(""); len(own) > 0 {
		lines = append(lines, "- **task**")
		lines = append(lines, bindingLines(own)...)
	}

	for _, g := range runGroups(rec) {
		lines = append(lines, g.header(anchor))
		lines = append(lines, bindingLines(g.bindings)...)
	}

	if len(lines) == 0 {
		return []string{emptyBlock}
	}
	return lines
}

// runGroup is one run and the bindings it produced. A group whose run is
// missing from the record carries only the id it was tagged with.
type runGroup struct {
	number   int
	run      wf.Run
	via      string
	known    bool
	bindings wf.Bindings
}

func (g runGroup) header(anchor time.Time) string {
	if !g.known {
		// Evidence tagged with a run wf has no record of is still
		// evidence. Dropping it would hide exactly the artifacts a
		// half-written ledger most needs to account for.
		return "- **run** `" + g.via + "` · run record missing"
	}

	parts := []string{fmt.Sprintf("**run %d**", g.number)}
	if g.run.Workflow != "" {
		parts = append(parts, g.run.Workflow)
	}
	if g.run.Model != "" {
		parts = append(parts, g.run.Model)
	}
	parts = append(parts, outcomeText(g.run))
	if a := ageText(g.run.Started, anchor); a != "" {
		parts = append(parts, a)
	}
	return "- " + strings.Join(parts, " · ")
}

// outcomeText names how a run settled. A run with no end is running; one
// that ended without saying how is "ended", because silence is not
// success anywhere else in wf either.
func outcomeText(r wf.Run) string {
	switch {
	case !r.Done():
		return "running"
	case r.Outcome == "":
		return "ended"
	default:
		return string(r.Outcome)
	}
}

// runGroups numbers runs chronologically and returns them newest first.
// Numbering is by start order so "run 1" means the first attempt for the
// life of the task, and a later run appearing cannot renumber the ones
// already written into a note.
func runGroups(rec wf.Record) []runGroup {
	order := make([]int, len(rec.Runs))
	for i := range order {
		order[i] = i
	}
	// Index breaks ties so two runs started in the same instant still get
	// a stable order rather than whatever the sort felt like.
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := rec.Runs[order[a]], rec.Runs[order[b]]
		if !ra.Started.Equal(rb.Started) {
			return ra.Started.Before(rb.Started)
		}
		return order[a] < order[b]
	})

	groups := make([]runGroup, 0, len(rec.Runs))
	seen := make(map[string]bool, len(rec.Runs))
	for n := len(order) - 1; n >= 0; n-- {
		run := rec.Runs[order[n]]
		seen[run.ID] = true
		groups = append(groups, runGroup{
			number:   n + 1,
			run:      run,
			via:      run.ID,
			known:    true,
			bindings: rec.Bindings.From(run.ID),
		})
	}

	var orphans []string
	for _, b := range rec.Bindings {
		if b.Via == "" || seen[b.Via] {
			continue
		}
		seen[b.Via] = true
		orphans = append(orphans, b.Via)
	}
	sort.Strings(orphans)
	for _, via := range orphans {
		groups = append(groups, runGroup{via: via, bindings: rec.Bindings.From(via)})
	}
	return groups
}

// Kinds render in the order a human reads them: what shipped, what was
// written, where the work went, then the machinery it ran on.
var kindOrder = map[wf.Kind]int{
	wf.KindPR:        0,
	wf.KindDoc:       1,
	wf.KindIssue:     2,
	wf.KindTask:      3,
	wf.KindWorkspace: 4,
	wf.KindSession:   5,
	wf.KindReview:    6,
	wf.KindRepo:      7,
	wf.KindQueue:     8,
}

func kindRank(k wf.Kind) int {
	if r, ok := kindOrder[k]; ok {
		return r
	}
	return len(kindOrder)
}

// bindingLines renders one group's bindings, indented under its header and
// in a total order: kind, then time, then ref. Recorded order would be
// good enough to read and is not good enough to diff, since a ledger
// rewrite that reorders equal facts would churn every note.
func bindingLines(bs wf.Bindings) []string {
	sorted := make(wf.Bindings, len(bs))
	copy(sorted, bs)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if ra, rb := kindRank(a.Kind), kindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.Ref < b.Ref
	})

	lines := make([]string, 0, len(sorted))
	for _, b := range sorted {
		lines = append(lines, "  - "+bindingLine(b))
	}
	return lines
}

// bindingLine renders one binding: what it points at, what state it is in,
// and — when the referent only exists on one machine — whose machine.
//
// Dead bindings are rendered, never filtered. A superseded worktree and a
// merged PR are the evidence of what happened; keeping the escalated run's
// checkout findable is the entire point of Supersede, and a note that hid
// it would undo that at the last step.
func bindingLine(b wf.Binding) string {
	line := bindingSubject(b)
	if s := stateText(b); s != "" {
		line += " — " + s
	}
	if b.Host != "" {
		line += " *(" + b.Host + ")*"
	}
	return line
}

// stateText says what a binding's lifecycle means for its kind. A live PR
// is an *open* PR to everyone who reads one, and the binding vocabulary
// has no word for that — BindingState says the referent exists and is
// current, which for a pull request is exactly "open". Translating here
// keeps that reading in the note without adding a state the type does not
// have.
func stateText(b wf.Binding) string {
	if b.State == wf.BindingUnknown {
		return ""
	}
	if b.State == wf.BindingLive && (b.Kind == wf.KindPR || b.Kind == wf.KindIssue) {
		return "open"
	}
	return string(b.State)
}

func bindingSubject(b wf.Binding) string {
	switch b.Kind {
	case wf.KindDoc:
		return docLink(b)
	case wf.KindPR:
		return "PR " + refLink(b)
	case wf.KindIssue:
		return "issue " + refLink(b)
	case wf.KindWorkspace:
		// The checkout's name, not its path: the path is machine-local
		// noise and the host annotation already says whose machine.
		return "worktree `" + baseName(b.Ref) + "`"
	case wf.KindSession:
		return "session `" + sessionName(b) + "`"
	case wf.KindReview:
		return "review " + linkOrCode(b.Ref, reviewText(b))
	case wf.KindQueue:
		if backend := b.Get(wf.MetaBackend); backend != "" {
			return "queue " + linkOrCode(b.Ref, backend+" "+b.Ref)
		}
		return "queue " + linkOrCode(b.Ref, b.Ref)
	case wf.KindRepo:
		return "repo `" + b.Ref + "`"
	case wf.KindTask:
		// A sibling task's id is opaque in a way a path or a PR number is
		// not, so its title comes along: an edge you cannot read is an
		// edge you will not follow.
		subject := "task `" + b.Ref + "`"
		if rel := b.Get(wf.MetaRelation); rel != "" {
			subject = rel + " " + subject
		}
		if b.Label != "" {
			subject += " · " + b.Label
		}
		return subject
	default:
		return string(b.Kind) + " " + linkOrCode(b.Ref, b.Ref)
	}
}

// docLink renders a produced document as a wikilink, which is the reason
// the projection exists at all: Obsidian's backlinks then give doc→task
// navigation for free, without wf storing a second edge to keep in sync.
func docLink(b wf.Binding) string {
	target := wikiTarget(b.Ref)
	if b.Label != "" && b.Label != path.Base(target) {
		return "[[" + target + "|" + b.Label + "]]"
	}
	return "[[" + target + "]]"
}

// wikiTarget turns a document ref into a link target: no `.md`, since
// Obsidian links name notes rather than files, and no absolute path, since
// a wikilink to one resolves to nothing at all — the basename usually
// resolves, and a wrong-but-live link beats a certainly-dead one.
func wikiTarget(ref string) string {
	t := strings.TrimSpace(ref)
	t = strings.TrimPrefix(t, "./")
	if strings.HasPrefix(t, "/") {
		t = path.Base(t)
	}
	if ext := path.Ext(t); strings.EqualFold(ext, ".md") {
		t = strings.TrimSuffix(t, ext)
	}
	return t
}

// refLink renders a PR or an issue as a markdown link so it is one click
// from the note. The short form (`#412`) is the link text because the
// number is what a human recognizes and the URL carries the rest.
func refLink(b wf.Binding) string {
	text := numberText(b.Ref)
	if text == "" {
		text = b.Label
	}
	if text == "" {
		text = b.Ref
	}
	return linkOrCode(b.Ref, text)
}

var numberRe = regexp.MustCompile(`(?:#|/(?:pull|pulls|issues|merge_requests)/)(\d+)/?$`)

func numberText(ref string) string {
	if m := numberRe.FindStringSubmatch(ref); m != nil {
		return "#" + m[1]
	}
	return ""
}

// linkOrCode links a ref only when it is actually addressable. Guessing a
// scheme onto a bare `github.com/me/app#412` would manufacture a URL wf
// was never told, so an unlinkable ref renders as text instead.
func linkOrCode(ref, text string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return "[" + text + "](" + ref + ")"
	}
	return "`" + text + "`"
}

func reviewText(b wf.Binding) string {
	text := b.Label
	if port := b.Get(wf.MetaPort); port != "" {
		text = strings.TrimSpace(text + " :" + port)
	}
	if text == "" {
		text = b.Ref
	}
	return text
}

// sessionName prefers the runner's own session id: Ref holds a path, which
// is what reattaching uses and not what a reader needs to see.
func sessionName(b wf.Binding) string {
	if id := b.Get(wf.MetaSessionID); id != "" {
		return id
	}
	name := baseName(b.Ref)
	return strings.TrimSuffix(name, path.Ext(name))
}

func baseName(ref string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(ref), "/")
	if trimmed == "" {
		return ref
	}
	return path.Base(trimmed)
}

// renderAnchor is the clock ages are measured against: the record's own
// last update, not time.Now().
//
// The design writes ages into the note ("12m ago") and also requires that
// re-rendering an unchanged task produce no diff — which a wall-clock age
// cannot do, since it changes every minute whether the task did or not.
// Anchoring to the record makes the note a snapshot: the age is accurate
// when the block is written, freezes with the facts around it, and moves
// again only when the record itself does. A record with no clock at all
// falls back to the newest timestamp it carries; if there is none, ages
// are omitted rather than invented.
func renderAnchor(rec wf.Record) time.Time {
	if !rec.Updated.IsZero() {
		return rec.Updated
	}
	newest := rec.Created
	consider := func(t time.Time) {
		if t.After(newest) {
			newest = t
		}
	}
	for _, r := range rec.Runs {
		consider(r.Started)
		if r.Ended != nil {
			consider(*r.Ended)
		}
	}
	for _, b := range rec.Bindings {
		consider(b.At)
	}
	return newest
}

// ageText renders a coarse age. Coarse on purpose: a note re-rendered a
// few seconds later should not differ, and no decision anyone makes from
// this block turns on a minute.
func ageText(t, anchor time.Time) string {
	if t.IsZero() || anchor.IsZero() {
		return ""
	}
	d := anchor.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
