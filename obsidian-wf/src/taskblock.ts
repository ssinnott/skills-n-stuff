/**
 * Renders a wf task (`wf show --json`'s single object) into the managed
 * block of an Obsidian note.
 *
 * This is a port of wf's own `internal/note/taskblock` (Go), which rendered
 * this block before Stage 2 of DESIGN-slim.md moved rendering to whichever
 * surface displays it. Everything here is a pure string transform over the
 * object `wf show --json` emits — no vault, no filesystem, no Obsidian API
 * — because the projection is the part worth pinning with tests, and it
 * needs none of those to be tested.
 *
 * Two properties carry over from the Go version and are still load-bearing:
 *
 *   - The block is regenerated wholesale between two delimiters, and
 *     everything outside them is the human's. wf is a third writer into a
 *     file an agent and Obsidian also touch, so it owns a region rather
 *     than merging into prose it does not.
 *   - Nothing in the output moves unless the task moved. Ages are coarse
 *     ("12m ago") and anchored to the task's own `updated` time, not the
 *     wall clock, so re-rendering an unchanged task is byte-identical.
 */

import type { WfBinding, WfRun, WfShow } from "./wf";

/** Frontmatter field naming the bound wf task — the durable half of the join. */
export const TASK_KEY = "wf-task";

/** Delimiters of the region this module owns. Obsidian comments, so the block is invisible in reading view. */
export const BEGIN_MARKER = "%% wf:begin %%";
export const END_MARKER = "%% wf:end %%";

const EMPTY_BLOCK = "_No runs or bindings yet._";

/**
 * Renders a record as the managed block, delimiters included, no trailing
 * newline. The delimiters are part of the output because they are part of
 * what this module owns: `applyBlock`'s replacement span is defined by
 * exactly these two lines.
 */
export function renderBlock(record: WfShow): string {
    return [BEGIN_MARKER, ...blockLines(record), END_MARKER].join("\n");
}

/**
 * Writes the record into a note: the task id into frontmatter, the bindings
 * into the managed block. Applying twice is byte-identical to applying
 * once, which is what lets the plugin call this on every open without
 * dirtying a synced file.
 *
 * The note's own binding to itself (the doc binding this very note is bound
 * through) is dropped before rendering: a wikilink to itself is a link
 * nobody can follow and a self-edge in the graph the design never wanted.
 */
export function applyBlock(noteText: string, record: WfShow): string {
    let text = noteText;
    // An id-less record has no join to write. Stamping an empty field would
    // claim a binding that does not exist.
    if (record.id) {
        text = setField(text, TASK_KEY, record.id);
    }

    const block = renderBlock(dropOwnNote(record));
    const found = findBlock(text);
    if (found) {
        return text.slice(0, found.start) + block + text.slice(found.end);
    }
    return appendBlock(text, block);
}

// ---- frontmatter (a minimal, scalar-only read/write — see wf's internal/note/frontmatter.go) ----

const FRONTMATTER_RE = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/;

function setField(text: string, key: string, value: string): string {
    const m = FRONTMATTER_RE.exec(text);
    if (!m) {
        return `---\n${key}: ${value}\n---\n${text}`;
    }
    const whole = m[0];
    const body = m[1];
    const existingRe = new RegExp(`^${key}:.*$`, "m");
    const updatedBody = existingRe.test(body)
        ? body.replace(existingRe, `${key}: ${value}`)
        : `${body}\n${key}: ${value}`;
    return `---\n${updatedBody}\n---\n${text.slice(whole.length)}`;
}

// ---- the managed region ----

const BEGIN_RE = /^[ \t]*%% wf:begin %%[ \t]*\r?$/gm;
const END_RE = /^[ \t]*%% wf:end %%[ \t]*\r?$/gm;

interface Span {
    start: number;
    end: number;
}

function matches(re: RegExp, text: string): Span[] {
    re.lastIndex = 0;
    const out: Span[] = [];
    let m: RegExpExecArray | null;
    while ((m = re.exec(text))) {
        out.push({ start: m.index, end: m.index + m[0].length });
    }
    return out;
}

/**
 * Locates the managed region: the first end marker, paired with the *last*
 * begin marker before it. Pairing the closest begin rather than the first
 * one means an unterminated `%% wf:begin %%` left by a crashed write or a
 * hand edit is inert — never a region boundary, never deleted — rather than
 * swallowing every line below it including the human's own prose.
 */
function findBlock(text: string): Span | null {
    const begins = matches(BEGIN_RE, text);
    const ends = matches(END_RE, text);

    for (const e of ends) {
        let best = -1;
        for (const b of begins) {
            if (b.start >= e.start) break;
            best = b.start;
        }
        if (best >= 0) return { start: best, end: e.end };
    }
    return null;
}

/** Puts a fresh block at the end of the note, separated by one blank line. */
function appendBlock(text: string, block: string): string {
    const body = text.replace(/\n+$/, "");
    if (body === "") return block + "\n";
    return body + "\n\n" + block + "\n";
}

// ---- the body ----

interface RunGroup {
    number: number;
    run?: WfRun;
    via: string;
    known: boolean;
    bindings: WfBinding[];
}

function blockLines(record: WfShow): string[] {
    const runs = record.runs ?? [];
    const bindings = allBindings(record);
    const anchor = renderAnchor(record, runs, bindings);
    const lines: string[] = [];

    const own = bindingsFrom(bindings, "");
    if (own.length > 0) {
        lines.push("- **task**");
        lines.push(...bindingLines(own));
    }

    for (const g of runGroups(runs, bindings)) {
        lines.push(header(g, anchor));
        lines.push(...bindingLines(g.bindings));
    }

    return lines.length > 0 ? lines : [EMPTY_BLOCK];
}

/** Reconstructs the record's flat binding list from the two places `wf show --json` splits it across. */
function allBindings(record: WfShow): WfBinding[] {
    const out = [...(record.bindings ?? [])];
    for (const run of record.runs ?? []) {
        out.push(...(run.bindings ?? []));
    }
    return out;
}

function bindingsFrom(bindings: WfBinding[], via: string): WfBinding[] {
    return bindings.filter((b) => (b.via ?? "") === via);
}

function header(g: RunGroup, anchor: Date | null): string {
    if (!g.known) {
        // Evidence tagged with a run wf has no record of is still evidence.
        return "- **run** `" + g.via + "` · run record missing";
    }
    const run = g.run as WfRun;
    const parts = [`**run ${g.number}**`];
    if (run.workflow) parts.push(run.workflow);
    if (run.model) parts.push(run.model);
    parts.push(outcomeText(run));
    const a = ageText(parseTime(run.started), anchor);
    if (a) parts.push(a);
    return "- " + parts.join(" · ");
}

function outcomeText(run: WfRun): string {
    if (!run.ended) return "running";
    if (!run.outcome) return "ended";
    return run.outcome;
}

/**
 * Numbers runs chronologically (oldest = 1) and returns them newest first.
 * A binding tagged with a run id the record has lost is still rendered, as
 * an orphan group naming the run it came from.
 */
function runGroups(runs: WfRun[], bindings: WfBinding[]): RunGroup[] {
    const order = runs.map((_, i) => i);
    order.sort((a, b) => {
        const ta = parseTime(runs[a].started)?.getTime() ?? -Infinity;
        const tb = parseTime(runs[b].started)?.getTime() ?? -Infinity;
        if (ta !== tb) return ta - tb;
        return a - b;
    });

    const groups: RunGroup[] = [];
    const seen = new Set<string>();
    for (let n = order.length - 1; n >= 0; n--) {
        const run = runs[order[n]];
        seen.add(run.id);
        groups.push({
            number: n + 1,
            run,
            via: run.id,
            known: true,
            bindings: bindingsFrom(bindings, run.id),
        });
    }

    const orphans: string[] = [];
    for (const b of bindings) {
        if (!b.via || seen.has(b.via)) continue;
        seen.add(b.via);
        orphans.push(b.via);
    }
    orphans.sort();
    for (const via of orphans) {
        groups.push({ number: 0, via, known: false, bindings: bindingsFrom(bindings, via) });
    }
    return groups;
}

/** Kinds render in the order a human reads them: what shipped, what was written, where the work went, then the machinery. */
const KIND_ORDER: Record<string, number> = {
    pr: 0,
    doc: 1,
    issue: 2,
    task: 3,
    workspace: 4,
    session: 5,
    repo: 6,
};

function kindRank(kind: string): number {
    return kind in KIND_ORDER ? KIND_ORDER[kind] : Object.keys(KIND_ORDER).length;
}

/** Renders one group's bindings in a total order: kind, then time, then ref — so an equal-facts reorder in the ledger does not churn the note. */
function bindingLines(bindings: WfBinding[]): string[] {
    const sorted = [...bindings].sort((a, b) => {
        const ra = kindRank(a.kind);
        const rb = kindRank(b.kind);
        if (ra !== rb) return ra - rb;
        if (a.kind !== b.kind) return a.kind < b.kind ? -1 : 1;
        const ta = parseTime(a.at)?.getTime() ?? 0;
        const tb = parseTime(b.at)?.getTime() ?? 0;
        if (ta !== tb) return ta - tb;
        return a.ref < b.ref ? -1 : a.ref > b.ref ? 1 : 0;
    });
    return sorted.map((b) => "  - " + bindingLine(b));
}

/**
 * Renders one binding: what it points at, what state it is in, and — when
 * the referent only exists on one machine — whose machine. Dead bindings
 * are rendered, never filtered: a superseded worktree or a merged PR is
 * the evidence of what happened.
 */
function bindingLine(b: WfBinding): string {
    let line = bindingSubject(b);
    if (b.state) line += " — " + b.state;
    if (b.host) line += " *(" + b.host + ")*";
    return line;
}

export function bindingSubject(b: WfBinding): string {
    switch (b.kind) {
        case "doc":
            return docLink(b);
        case "pr":
            return "PR " + refLink(b);
        case "issue":
            return "issue " + refLink(b);
        case "workspace":
            // The checkout's name, not its path: the path is machine-local
            // noise and the host annotation already says whose machine.
            return "worktree `" + baseName(b.ref) + "`";
        case "session":
            return "session `" + sessionName(b) + "`";
        case "repo":
            return "repo `" + b.ref + "`";
        case "task": {
            // A sibling task's id is opaque in a way a path or a PR number
            // is not, so its title comes along.
            let subject = "task `" + b.ref + "`";
            const relation = b.meta?.relation;
            if (relation) subject = relation + " " + subject;
            if (b.label) subject += " · " + b.label;
            return subject;
        }
        default:
            return b.kind + " " + linkOrCode(b.ref, b.ref);
    }
}

/** Produced documents render as wikilinks, giving Obsidian's backlinks doc-to-task navigation for free. */
function docLink(b: WfBinding): string {
    const target = wikiTarget(b.ref);
    if (b.label && b.label !== baseName(target)) {
        return "[[" + target + "|" + b.label + "]]";
    }
    return "[[" + target + "]]";
}

/** Turns a document ref into a link target: no `.md` (a wikilink names a note, not a file) and no absolute path (a wikilink to one resolves to nothing). */
function wikiTarget(ref: string): string {
    let t = ref.trim();
    if (t.startsWith("./")) t = t.slice(2);
    if (t.startsWith("/")) t = baseName(t);
    const ext = extOf(t);
    if (ext.toLowerCase() === ".md") t = t.slice(0, t.length - ext.length);
    return t;
}

/** Renders a PR or issue as a markdown link; the short form (`#412`) is the text because the number is what a human recognizes. */
function refLink(b: WfBinding): string {
    let text = numberText(b.ref);
    if (!text) text = b.label ?? "";
    if (!text) text = b.ref;
    return linkOrCode(b.ref, text);
}

const NUMBER_RE = /(?:#|\/(?:pull|pulls|issues|merge_requests)\/)(\d+)\/?$/;

function numberText(ref: string): string {
    const m = NUMBER_RE.exec(ref);
    return m ? "#" + m[1] : "";
}

/** Links a ref only when it is actually addressable — guessing a scheme onto a bare ref would manufacture a URL wf was never told. */
function linkOrCode(ref: string, text: string): string {
    if (ref.startsWith("http://") || ref.startsWith("https://")) {
        return "[" + text + "](" + ref + ")";
    }
    return "`" + text + "`";
}

/** Prefers the runner's own session id: Ref holds a path, which is what reattaching uses and not what a reader needs to see. */
function sessionName(b: WfBinding): string {
    const id = b.meta?.session_id;
    if (id) return id;
    const name = baseName(b.ref);
    const ext = extOf(name);
    return ext ? name.slice(0, name.length - ext.length) : name;
}

function baseName(ref: string): string {
    const trimmed = ref.trim().replace(/\/+$/, "");
    if (trimmed === "") return ref;
    const i = trimmed.lastIndexOf("/");
    return i === -1 ? trimmed : trimmed.slice(i + 1);
}

function extOf(s: string): string {
    const slash = s.lastIndexOf("/");
    const base = slash === -1 ? s : s.slice(slash + 1);
    const dot = base.lastIndexOf(".");
    return dot === -1 ? "" : base.slice(dot);
}

// ---- ages ----

function parseTime(s?: string): Date | null {
    if (!s) return null;
    const t = new Date(s);
    return Number.isNaN(t.getTime()) ? null : t;
}

/**
 * The clock ages are measured against: the record's own `updated` time, not
 * wall-clock now. A note in a synced vault must re-render to the same bytes
 * when nothing about the task changed, which a wall-clock age cannot do —
 * it moves every minute regardless. A record with no clock at all falls
 * back to the newest timestamp it carries; with none, ages are omitted.
 */
function renderAnchor(record: WfShow, runs: WfRun[], bindings: WfBinding[]): Date | null {
    const updated = parseTime(record.updated);
    if (updated) return updated;

    let newest: Date | null = null;
    const consider = (s?: string) => {
        const t = parseTime(s);
        if (t && (!newest || t.getTime() > newest.getTime())) newest = t;
    };
    for (const r of runs) {
        consider(r.started);
        consider(r.ended);
    }
    for (const b of bindings) {
        consider(b.at);
    }
    return newest;
}

/** A coarse age ("12m ago", "2h ago", "3d ago") — coarse on purpose, since no decision here turns on a minute. */
function ageText(t: Date | null, anchor: Date | null): string {
    if (!t || !anchor) return "";
    const ms = anchor.getTime() - t.getTime();
    const minute = 60_000;
    const hour = 60 * minute;
    const day = 24 * hour;
    if (ms < minute) return "just now";
    if (ms < hour) return `${Math.trunc(ms / minute)}m ago`;
    if (ms < day) return `${Math.trunc(ms / hour)}h ago`;
    return `${Math.trunc(ms / day)}d ago`;
}

// ---- self-link suppression ----

/**
 * The task's own note — the newest live vault doc binding no run produced —
 * identified the same way wf's `Bindings.Note()` does. Dropped before
 * rendering so a note never links to itself.
 */
function dropOwnNote(record: WfShow): WfShow {
    const bindings = record.bindings ?? [];
    let best: WfBinding | null = null;
    for (const b of bindings) {
        if (b.kind !== "doc" || b.via || b.meta?.store !== "vault") continue;
        // IsLive(): unknown (the empty/omitted state) counts as live too.
        if (b.state && b.state !== "live") continue;
        const at = parseTime(b.at)?.getTime() ?? -Infinity;
        const bestAt = best ? (parseTime(best.at)?.getTime() ?? -Infinity) : -Infinity;
        if (!best || at >= bestAt) best = b;
    }
    if (!best) return record;
    return { ...record, bindings: bindings.filter((b) => b !== best) };
}
