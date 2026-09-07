/**
 * Pins taskblock.ts against the same fixtures wf's own
 * internal/note/taskblock/block_test.go used before Stage 2 of
 * DESIGN-slim.md moved rendering here — see taskblock.ts's header comment.
 */

import { test } from "node:test";
import assert from "node:assert/strict";

import { applyBlock, bindingSubject, renderBlock } from "./taskblock.ts";
import type { WfBinding, WfShow } from "./wf.ts";

function ts(h: number, m: number): string {
    return `2026-03-04T${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}:00Z`;
}

/** Minimal identity fields every fixture needs — the rest of WfTask is optional. */
function baseTask(): WfShow {
    return { id: "01M1S", shortId: "neck", title: "Add the parser", priority: 2 };
}

/** The design's own example: a first run that escalated after producing a plan, and a re-run that shipped a PR. */
function twoRuns(): WfShow {
    return {
        ...baseTask(),
        updated: ts(12, 0),
        runs: [
            {
                id: "run-1",
                workflow: "plan-to-pr",
                profile: "coding",
                model: "sonnet-5",
                host: "wf-laptop",
                started: ts(10, 0),
                ended: ts(10, 30),
                outcome: "escalated",
            },
            {
                id: "run-2",
                workflow: "plan-to-pr",
                profile: "coding",
                model: "opus-5",
                host: "wf-laptop",
                started: ts(11, 48),
                ended: ts(11, 58),
                outcome: "done",
            },
        ],
        bindings: [
            {
                kind: "doc",
                ref: "Research/parser-plan.md",
                at: ts(10, 25),
                via: "run-1",
                meta: { store: "vault" },
            },
            {
                kind: "pr",
                ref: "https://github.com/me/app/pull/412",
                state: "live",
                stateLabel: "open",
                at: ts(11, 57),
                via: "run-2",
            },
        ],
    };
}

// Workspace bindings live under each run's own list in `wf show --json`.
const twoRunsWorkspace1: WfBinding = {
    kind: "workspace",
    ref: "/Users/me/.wf/worktrees/neck-add-parser",
    state: "superseded",
    stateLabel: "superseded",
    at: ts(10, 0),
    via: "run-1",
    host: "wf-laptop",
    meta: { branch: "wf/neck-add-parser" },
};
const twoRunsWorkspace2: WfBinding = {
    kind: "workspace",
    ref: "/Users/me/.wf/worktrees/neck-add-parser-2",
    state: "live",
    stateLabel: "live",
    at: ts(11, 48),
    via: "run-2",
    host: "wf-laptop",
};

function twoRunsFull(): WfShow {
    const rec = twoRuns();
    rec.runs![0].bindings = [rec.bindings![0], twoRunsWorkspace1];
    rec.runs![1].bindings = [rec.bindings![1], twoRunsWorkspace2];
    rec.bindings = [];
    return rec;
}

test("RenderBlock matches the design", () => {
    const want = [
        "%% wf:begin %%",
        "- **run 2** · plan-to-pr · opus-5 · done · 12m ago",
        "  - PR [#412](https://github.com/me/app/pull/412) — open",
        "  - worktree `neck-add-parser-2` — live *(wf-laptop)*",
        "- **run 1** · plan-to-pr · sonnet-5 · escalated · 2h ago",
        "  - [[Research/parser-plan]]",
        "  - worktree `neck-add-parser` — superseded *(wf-laptop)*",
        "%% wf:end %%",
    ].join("\n");

    assert.equal(renderBlock(twoRunsFull()), want);
});

test("RenderBlock is stable across calls", () => {
    const rec = twoRunsFull();
    const first = renderBlock(rec);
    const second = renderBlock(rec);
    assert.equal(first, second);
});

test("ages are anchored to the record's clock, not wall time", () => {
    const rec = twoRunsFull();
    rec.updated = ts(23, 0);

    const got = renderBlock(rec);
    assert.match(got, /· 11h ago/);
    assert.match(got, /· 13h ago/);
});

test("age is omitted without a clock", () => {
    const rec: WfShow = { ...baseTask(), runs: [{ id: "run-1", workflow: "research" }] };
    const want = "%% wf:begin %%\n- **run 1** · research · running\n%% wf:end %%";
    assert.equal(renderBlock(rec), want);
});

test("dead bindings are shown with their state, never hidden", () => {
    const rec: WfShow = {
        ...baseTask(),
        updated: ts(12, 0),
        runs: [
            {
                id: "run-1",
                started: ts(11, 0),
                ended: ts(11, 30),
                outcome: "done",
                bindings: [
                    {
                        kind: "pr",
                        ref: "https://github.com/me/app/pull/9",
                        state: "merged",
                        stateLabel: "merged",
                        at: ts(11, 20),
                        via: "run-1",
                    },
                    {
                        kind: "workspace",
                        ref: "/w/gone",
                        state: "disposed",
                        stateLabel: "disposed",
                        at: ts(11, 0),
                        via: "run-1",
                        host: "wf-desktop",
                    },
                    {
                        kind: "session",
                        ref: "/s/neck-a1b2.jsonl",
                        state: "missing",
                        stateLabel: "missing",
                        at: ts(11, 0),
                        via: "run-1",
                        host: "wf-desktop",
                    },
                ],
            },
        ],
    };

    const got = renderBlock(rec);
    for (const want of [
        "  - PR [#9](https://github.com/me/app/pull/9) — merged",
        "  - worktree `gone` — disposed *(wf-desktop)*",
        "  - session `neck-a1b2` — missing *(wf-desktop)*",
    ]) {
        assert.ok(got.includes(want), `missing ${JSON.stringify(want)} in:\n${got}`);
    }
});

test("machine-local bindings carry their host, a vault doc never does", () => {
    const rec: WfShow = {
        ...baseTask(),
        updated: ts(12, 0),
        bindings: [
            { kind: "workspace", ref: "/w/neck", state: "live", stateLabel: "live", at: ts(11, 0), host: "wf-laptop" },
            { kind: "doc", ref: "Research/plan.md", at: ts(11, 0) },
        ],
    };

    const got = renderBlock(rec);
    assert.ok(got.includes("worktree `neck` — live *(wf-laptop)*"));
    assert.ok(!got.includes("[[Research/plan]] *("));
});

test("bindings with no run are the task's own, and lead", () => {
    const rec: WfShow = {
        ...baseTask(),
        updated: ts(12, 0),
        runs: [{ id: "run-1", workflow: "plan-to-pr", started: ts(11, 0), bindings: [
            { kind: "workspace", ref: "/w/neck", state: "live", stateLabel: "live", at: ts(11, 0), via: "run-1", host: "wf-laptop" },
        ] }],
        bindings: [
            { kind: "repo", ref: "~/code/app", at: ts(9, 0), host: "wf-laptop" },
        ],
    };

    const want = [
        "%% wf:begin %%",
        "- **task**",
        "  - repo `~/code/app` *(wf-laptop)*",
        "- **run 1** · plan-to-pr · running · 1h ago",
        "  - worktree `neck` — live *(wf-laptop)*",
        "%% wf:end %%",
    ].join("\n");

    assert.equal(renderBlock(rec), want);
});

test("bindings from an unknown run are not dropped", () => {
    const rec: WfShow = {
        ...baseTask(),
        updated: ts(12, 0),
        bindings: [{ kind: "doc", ref: "Notes/found.md", at: ts(11, 0), via: "run-gone" }],
    };

    const got = renderBlock(rec);
    assert.ok(got.includes("- **run** `run-gone` · run record missing"));
    assert.ok(got.includes("[[Notes/found]]"));
});

test("an empty record says so", () => {
    const want = "%% wf:begin %%\n_No runs or bindings yet._\n%% wf:end %%";
    assert.equal(renderBlock(baseTask()), want);
});

test("doc links", () => {
    const cases: [string, WfBinding, string][] = [
        ["strips .md", { kind: "doc", ref: "Research/parser-plan.md" }, "[[Research/parser-plan]]"],
        ["keeps a bare name", { kind: "doc", ref: "Research/parser-plan" }, "[[Research/parser-plan]]"],
        ["strips ./", { kind: "doc", ref: "./Research/plan.md" }, "[[Research/plan]]"],
        ["uses the basename of an absolute path", { kind: "doc", ref: "/Users/me/vault/Research/plan.md" }, "[[plan]]"],
        ["aliases a label", { kind: "doc", ref: "Research/plan.md", label: "Parser plan" }, "[[Research/plan|Parser plan]]"],
        ["drops a redundant label", { kind: "doc", ref: "Research/plan.md", label: "plan" }, "[[Research/plan]]"],
        ["case-insensitive extension", { kind: "doc", ref: "Research/plan.MD" }, "[[Research/plan]]"],
    ];
    for (const [name, bind, want] of cases) {
        assert.equal(bindingSubject(bind), want, name);
    }
});

test("PR subjects", () => {
    const cases: [string, WfBinding, string][] = [
        ["github url", { kind: "pr", ref: "https://github.com/me/app/pull/412" }, "PR [#412](https://github.com/me/app/pull/412)"],
        ["trailing slash", { kind: "pr", ref: "https://github.com/me/app/pull/412/" }, "PR [#412](https://github.com/me/app/pull/412/)"],
        ["unlinkable ref falls back to text", { kind: "pr", ref: "github.com/me/app#412" }, "PR `#412`"],
        [
            "label when no number is derivable",
            { kind: "pr", ref: "https://example.com/review/abc", label: "Add the parser" },
            "PR [Add the parser](https://example.com/review/abc)",
        ],
    ];
    for (const [name, bind, want] of cases) {
        assert.equal(bindingSubject(bind), want, name);
    }
});

test("other kind subjects", () => {
    const cases: [string, WfBinding, string][] = [
        [
            "session prefers the runner's own id over its path",
            { kind: "session", ref: "/Users/me/.wf/sessions/2026-03-04-neck.jsonl", meta: { session_id: "neck-a1b2" } },
            "session `neck-a1b2`",
        ],
        [
            "session falls back to the file name",
            { kind: "session", ref: "/Users/me/.wf/sessions/neck-a1b2.jsonl" },
            "session `neck-a1b2`",
        ],
        [
            "a sibling task carries its relation and title",
            { kind: "task", ref: "01M1TB2", label: "Wire the parser into the CLI", meta: { relation: "next" } },
            "next task `01M1TB2` · Wire the parser into the CLI",
        ],
        ["an unknown kind still renders", { kind: "dataset", ref: "s3://bucket/thing" }, "dataset `s3://bucket/thing`"],
    ];
    for (const [name, bind, want] of cases) {
        assert.equal(bindingSubject(bind), want, name);
    }
});

test("applyBlock creates frontmatter", () => {
    const got = applyBlock("# Add the parser\n\nMy own notes here.\n", twoRunsFull());

    assert.ok(got.startsWith("---\nwf-task: 01M1S\n---\n"));
    assert.ok(got.includes("My own notes here."));
    assert.ok(got.includes(renderBlock(twoRunsFull())));
});

test("applyBlock appends below existing frontmatter", () => {
    const note = "---\nstatus: draft\ntags: [work]\n---\n# Add the parser\n\nNotes.\n";
    const got = applyBlock(note, twoRunsFull());

    for (const want of ["status: draft", "tags: [work]", "wf-task: 01M1S", "# Add the parser", "Notes."]) {
        assert.ok(got.includes(want), `missing ${JSON.stringify(want)}`);
    }
    assert.ok(got.endsWith(renderBlock(twoRunsFull()) + "\n"));
});

test("applyBlock replaces only the block", () => {
    const first = applyBlock("---\nwf-task: 01M1S\n---\n# Add the parser\n\nMine.\n", baseTask());
    const got = applyBlock(first, twoRunsFull());

    assert.ok(!got.includes("_No runs or bindings yet._"));
    assert.equal((got.match(/%% wf:begin %%/g) ?? []).length, 1);
    assert.ok(got.includes("Mine."));
});

test("applyBlock preserves text after the block, touching nothing outside its span", () => {
    const note =
        "---\nwf-task: 01M1S\n---\n# Task\n\nBefore.\n\n" +
        "%% wf:begin %%\n- stale\n%% wf:end %%\n\nAfter, written by hand.\n";
    const got = applyBlock(note, twoRunsFull());

    assert.ok(got.includes("Before."));
    assert.ok(got.includes("After, written by hand."));
    assert.ok(!got.includes("- stale"));
    assert.ok(got.endsWith("\n\nAfter, written by hand.\n"));
});

test("an unterminated block eats nothing, and recovery settles", () => {
    const note = "---\nwf-task: 01M1S\n---\n# Task\n\n%% wf:begin %%\n- half-written\n\nMy own paragraph.\n";
    const got = applyBlock(note, twoRunsFull());

    for (const want of ["- half-written", "My own paragraph.", "%% wf:begin %%"]) {
        assert.ok(got.includes(want), `missing ${JSON.stringify(want)}`);
    }
    assert.ok(got.endsWith(renderBlock(twoRunsFull()) + "\n"));

    const twice = applyBlock(got, twoRunsFull());
    assert.equal(twice, got);
    assert.ok(twice.includes("My own paragraph."));
});

test("a stray end marker above the real block does not shadow it", () => {
    const note = "# Task\n\n%% wf:end %%\n\nMine.\n\n%% wf:begin %%\n- stale\n%% wf:end %%\n";
    const got = applyBlock(note, twoRunsFull());

    assert.ok(!got.includes("- stale"));
    assert.ok(got.includes("Mine."));
    assert.equal(applyBlock(got, twoRunsFull()), got);
});

test("a marker named in a sentence is prose, not a delimiter", () => {
    const note = "# Task\n\nwf writes between %% wf:begin %% and %% wf:end %% markers.\n";
    const got = applyBlock(note, twoRunsFull());

    assert.ok(got.includes("wf writes between %% wf:begin %% and %% wf:end %% markers."));
    assert.ok(got.endsWith(renderBlock(twoRunsFull()) + "\n"));
});

test("applyBlock is idempotent across a range of note shapes", () => {
    const notes: Record<string, string> = {
        "no frontmatter": "# Add the parser\n\nNotes.\n",
        "frontmatter only": "---\nstatus: draft\n---\n",
        "empty note": "",
        "no trailing newline": "# Add the parser\n\nNotes.",
        "existing block": "---\nwf-task: 01M1S\n---\n# T\n\n%% wf:begin %%\n- stale\n%% wf:end %%\n",
        "text after block": "%% wf:begin %%\n- stale\n%% wf:end %%\n\nTail.\n",
        "crlf note": "---\r\nstatus: draft\r\n---\r\n# T\r\n\r\nNotes.\r\n",
    };

    for (const rec of [twoRunsFull(), baseTask(), { ...baseTask(), id: "" }]) {
        for (const [name, note] of Object.entries(notes)) {
            const once = applyBlock(note, rec);
            const twice = applyBlock(once, rec);
            assert.equal(once, twice, name);
        }
    }
});

test("applyBlock without an id leaves frontmatter alone", () => {
    const got = applyBlock("# Task\n", { ...baseTask(), id: "" });
    assert.ok(!got.includes("wf-task"));
    assert.ok(got.includes("_No runs or bindings yet._"));
});

// Self-link suppression: the note's own bound document never becomes a
// wikilink to itself, while a document some run produced still renders.
test("applyBlock drops the note's own doc binding so it never links to itself", () => {
    const rec: WfShow = {
        ...baseTask(),
        updated: ts(12, 0),
        bindings: [
            // The note's own binding to itself: no run produced it, and it
            // lives in the vault.
            { kind: "doc", ref: "Tasks/Add the parser.md", state: "live", at: ts(9, 0), meta: { store: "vault" } },
            { kind: "repo", ref: "~/code/app", at: ts(9, 0) },
        ],
        runs: [{ id: "run-1", workflow: "plan-to-pr", started: ts(10, 0), bindings: [
            // A document a run produced is not the note itself and must still render.
            { kind: "doc", ref: "Research/plan.md", at: ts(10, 5), via: "run-1" },
        ] }],
    };

    const got = applyBlock("# Add the parser\n", rec);
    assert.ok(!got.includes("[[Add the parser]]"));
    assert.ok(!got.includes("Tasks/Add the parser"));
    assert.ok(got.includes("[[Research/plan]]"));
    // renderBlock itself is a pure projection and does no such filtering —
    // only applyBlock, which is the one that could be writing into that
    // very note, drops it.
    assert.ok(renderBlock(rec).includes("Tasks/Add the parser"));
});
