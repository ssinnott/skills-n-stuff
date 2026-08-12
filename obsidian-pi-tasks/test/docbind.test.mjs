// Mechanical tests for docbind.ts — run with: npm run build:test && node test/docbind.test.mjs
// (esbuild bundles docbind.ts to test/docbind.mjs first; no Obsidian needed)
import assert from "node:assert";
import {
  getSessionId, setSessionId, taskAt, setTaskStatus,
  findComments, wikiLinks, slugify, taskPrompt,
} from "./docbind.mjs";

// session frontmatter
const bare = "# Plan\n\n- [ ] Do the thing\n";
assert.equal(getSessionId(bare), null);
const bound = setSessionId(bare, "abc123");
assert.equal(getSessionId(bound), "abc123");
assert.ok(bound.startsWith("---\npi-session: abc123\n---\n"));
const rebound = setSessionId(bound, "def456");
assert.equal(getSessionId(rebound), "def456");
assert.equal((rebound.match(/pi-session/g) || []).length, 1);
const withFm = "---\nstatus: draft\n---\n# Plan\n";
const withBoth = setSessionId(withFm, "s1");
assert.equal(getSessionId(withBoth), "s1");
assert.ok(withBoth.includes("status: draft"));
assert.equal(getSessionId("---\npi-session: —\n---\n"), null, "placeholder dash is unbound");

// task lines
const doc = "# T\n\n- [ ] Build the chat view with [[rpc-notes]]\n- [x] Done thing\nnot a task\n";
const t = taskAt(doc, 2);
const t2 = t;
assert.ok(t && !t.checked);
assert.equal(t.slug, "build-the-chat-view-with-rpc-notes");
assert.equal(taskAt(doc, 3).checked, true);
assert.equal(taskAt(doc, 4), null);
assert.equal(taskAt(doc, 99), null);

// status transitions preserve text and indentation
let s = setTaskStatus(doc, 2, "running");
assert.ok(s.split("\n")[2].endsWith("⏳"));
s = setTaskStatus(s, 2, "done", "([[reviews/chat-view]])");
const doneLine = s.split("\n")[2];
assert.ok(doneLine.startsWith("- [x] Build the chat view"));
assert.ok(!doneLine.includes("⏳"), "running marker cleared");
assert.ok(doneLine.endsWith("([[reviews/chat-view]])"));
const failed = setTaskStatus(doc, 2, "failed");
assert.ok(failed.split("\n")[2].endsWith("❌"));
assert.equal(setTaskStatus(doc, 4, "done"), doc, "non-task line untouched");

// comments
const cdoc = "Intro %% @pi: tighten this %% middle\n\n%% @pi: drop the\nsecond example %%\n%% plain obsidian comment %%";
const comments = findComments(cdoc);
assert.equal(comments.length, 2);
assert.equal(comments[0].instruction, "tighten this");
assert.equal(comments[1].instruction, "drop the\nsecond example");
assert.equal(cdoc.slice(comments[0].start, comments[0].end), "%% @pi: tighten this %%");

// wiki links
assert.deepEqual(
  wikiLinks("see [[plans/alpha|the plan]] and [[notes#Heading]] and [[plans/alpha]]"),
  ["plans/alpha", "notes"],
);

// session refs on task lines
import { taskSessionRef, attachSessionRef, parseOutcome } from "./docbind.mjs";
let rdoc = attachSessionRef(doc, 2, ".pi-sessions/abc.jsonl");
assert.equal(taskSessionRef(rdoc, 2), ".pi-sessions/abc.jsonl");
assert.equal(taskSessionRef(rdoc, 3), null);
rdoc = attachSessionRef(rdoc, 2, ".pi-sessions/xyz.jsonl");   // idempotent replace
assert.equal(taskSessionRef(rdoc, 2), ".pi-sessions/xyz.jsonl");
assert.equal((rdoc.split("\n")[2].match(/pi:session/g) || []).length, 1);
assert.ok(taskAt(rdoc, 2).text === t.text, "hidden ref excluded from task text");
assert.equal(taskAt(rdoc, 2).slug, t.slug, "hidden ref excluded from slug");
// status transitions keep the ref at end of line
let rs = setTaskStatus(rdoc, 2, "running");
assert.ok(rs.split("\n")[2].includes("⏳"));
assert.ok(rs.split("\n")[2].endsWith("%% pi:session=.pi-sessions/xyz.jsonl %%"));
rs = setTaskStatus(rs, 2, "done", "([[reviews/out]])");
const rline = rs.split("\n")[2];
assert.ok(rline.startsWith("- [x] Build the chat view"));
assert.ok(!rline.includes("⏳"));
assert.ok(rline.includes("([[reviews/out]])"));
assert.equal(taskSessionRef(rs, 2), ".pi-sessions/xyz.jsonl");

// outcome parsing
const empty = { repoPath: null, prs: [], issues: [], next: [] };
assert.deepEqual(parseOutcome("all wrapped up\nDONE — review: reviews/plan.md"),
  { status: "done", reviewPath: "reviews/plan.md", ...empty });
assert.deepEqual(parseOutcome("DONE - review: notes/x.qmd"),
  { status: "done", reviewPath: "notes/x.qmd", ...empty });
assert.deepEqual(parseOutcome("refactor finished\nDONE"),
  { status: "done", reviewPath: null, ...empty });
assert.deepEqual(parseOutcome("BLOCKED: missing credentials"),
  { status: "failed", reviewPath: null, ...empty });
assert.deepEqual(parseOutcome("did DONE things but BLOCKED on tests"),
  { status: "failed", reviewPath: null, ...empty });

// PR outcomes put the task in review
const multi = parseOutcome(
  "PR: https://github.com/o/r/pull/12 — theme tokens\nPR: https://github.com/o/r/pull/13\nDONE");
assert.equal(multi.status, "review");
assert.deepEqual(multi.prs, [
  { url: "https://github.com/o/r/pull/12", title: "theme tokens" },
  { url: "https://github.com/o/r/pull/13", title: undefined },
]);
assert.deepEqual(parseOutcome("DONE — pr: https://github.com/o/r/pull/9").prs,
  [{ url: "https://github.com/o/r/pull/9" }]);
assert.equal(parseOutcome("PR: https://github.com/o/r/pull/9\nBLOCKED on CI").status, "failed");
const withRepo = parseOutcome("REPO: /home/u/code/webapp\nPR: https://github.com/o/r/pull/7\nDONE");
assert.equal(withRepo.repoPath, "/home/u/code/webapp");
assert.equal(withRepo.status, "review");

// issues are produced artifacts — they do NOT gate completion
const issued = parseOutcome(
  "ISSUE: https://github.com/o/r/issues/41 — flaky login test\nISSUE: https://github.com/o/r/issues/42\nDONE");
assert.equal(issued.status, "done", "issues alone leave the task done, not in review");
assert.deepEqual(issued.issues, [
  { url: "https://github.com/o/r/issues/41", title: "flaky login test" },
  { url: "https://github.com/o/r/issues/42", title: undefined },
]);
// NEXT lines are follow-up tasks; they chain, they don't gate
const chained = parseOutcome(
  "NEXT: Fix the flaky login test from https://github.com/o/r/issues/41\nNEXT: Audit the other auth tests\nDONE");
assert.equal(chained.status, "done");
assert.deepEqual(chained.next, [
  "Fix the flaky login test from https://github.com/o/r/issues/41",
  "Audit the other auth tests",
]);
// mixed outcome: PR gates, issue and next ride along
const mixed = parseOutcome(
  "PR: https://github.com/o/r/pull/50 — the fix\nISSUE: https://github.com/o/r/issues/51 — follow-up refactor\nNEXT: Land the refactor from https://github.com/o/r/issues/51\nDONE");
assert.equal(mixed.status, "review", "the PR still gates");
assert.equal(mixed.issues.length, 1);
assert.equal(mixed.next.length, 1);

// generic markers: repo ref coexists with session ref, both survive status changes
import { taskRepoRef, attachRepoRef, taskMarker, taskProfileRef, attachProfileRef, getProfile } from "./docbind.mjs";
let mdoc = attachSessionRef(doc, 2, ".pi-sessions/abc.jsonl");
mdoc = attachRepoRef(mdoc, 2, "/home/u/code/webapp");
assert.equal(taskSessionRef(mdoc, 2), ".pi-sessions/abc.jsonl");
assert.equal(taskRepoRef(mdoc, 2), "/home/u/code/webapp");
mdoc = setTaskStatus(mdoc, 2, "review");
assert.equal(taskSessionRef(mdoc, 2), ".pi-sessions/abc.jsonl", "session ref survives");
assert.equal(taskRepoRef(mdoc, 2), "/home/u/code/webapp", "repo ref survives");
assert.ok(!taskAt(mdoc, 2).text.includes("pi:"), "hidden markers stripped from text");
assert.ok(taskAt(mdoc, 2).text.endsWith("🔃"), "status marker stays visible");
assert.equal(taskAt(mdoc, 2).slug, t2.slug, "slug unchanged by markers");
assert.equal(taskMarker(mdoc, 2, "nope"), null);

// profile: task-line marker and note-level frontmatter default
assert.equal(taskProfileRef(mdoc, 2), null);
mdoc = attachProfileRef(mdoc, 2, "research");
assert.equal(taskProfileRef(mdoc, 2), "research");
mdoc = setTaskStatus(mdoc, 2, "done");
assert.equal(taskProfileRef(mdoc, 2), "research", "profile ref survives status change");
assert.equal(taskSessionRef(mdoc, 2), ".pi-sessions/abc.jsonl", "session ref still intact");
mdoc = attachProfileRef(mdoc, 2, "shipping");   // idempotent replace
assert.equal(taskProfileRef(mdoc, 2), "shipping");
assert.equal((mdoc.split("\n")[2].match(/pi:profile/g) || []).length, 1);
assert.equal(getProfile(bare), null);
assert.equal(getProfile("---\npi-profile: research\n---\n# T\n"), "research");
assert.equal(getProfile("---\npi-session: s1\npi-profile: research\n---\n"), "research");
assert.equal(getProfile("---\npi-profile: —\n---\n"), null, "placeholder dash is unset");

// PR children under a task line
import { findPRChildren, appendPRChildren, setPRChildState, parentTaskOf } from "./docbind.mjs";
let pdoc = "# T\n\n- [ ] Ship dark mode 🔃\nother\n";
pdoc = appendPRChildren(pdoc, 2, multi.prs);
let kids = findPRChildren(pdoc);
assert.equal(kids.length, 2);
assert.equal(kids[0].title, "theme tokens");
assert.equal(kids[1].title, "#13");
assert.equal(kids[0].state, "open");
assert.ok(pdoc.split("\n")[3].startsWith("  - [ ] PR:"), "indented under parent");
pdoc = appendPRChildren(pdoc, 2, multi.prs);   // idempotent
assert.equal(findPRChildren(pdoc).length, 2);
pdoc = setPRChildState(pdoc, kids[0].line, "merged");
kids = findPRChildren(pdoc);
assert.equal(kids[0].state, "merged");
assert.ok(pdoc.split("\n")[kids[0].line].includes("- [x]"), "merged checks the box");
assert.equal(parentTaskOf(pdoc, kids[0].line), 2);
assert.equal(parentTaskOf(pdoc, 0), null);
// review -> done transition strips the 🔃 marker
const finished = setTaskStatus(pdoc, 2, "done");
assert.ok(!finished.split("\n")[2].includes("🔃"));
assert.ok(finished.split("\n")[2].startsWith("- [x] Ship dark mode"));
const inReview = setTaskStatus(pdoc, 2, "review");
assert.ok(inReview.split("\n")[2].endsWith("🔃"));

// Issue children: tracked like PRs but closed (not merged) checks the box
let idoc = "# T\n\n- [ ] Research the login flakiness\n";
idoc = appendPRChildren(idoc, 2, [], [{ url: "https://github.com/o/r/issues/41", title: "flaky login" }]);
let ikids = findPRChildren(idoc);
assert.equal(ikids.length, 1);
assert.equal(ikids[0].kind, "Issue");
assert.equal(ikids[0].title, "flaky login");
assert.equal(ikids[0].state, "open");
assert.ok(idoc.split("\n")[3].startsWith("  - [ ] Issue:"));
idoc = appendPRChildren(idoc, 2, [], [{ url: "https://github.com/o/r/issues/41" }]);   // idempotent
assert.equal(findPRChildren(idoc).length, 1);
idoc = setPRChildState(idoc, 3, "closed");
ikids = findPRChildren(idoc);
assert.equal(ikids[0].state, "closed");
assert.ok(idoc.split("\n")[3].includes("- [x]"), "closed checks an issue's box");
// untitled issue falls back to #number
const untitled = appendPRChildren("- [ ] T\n", 0, [], [{ url: "https://github.com/o/r/issues/9" }]);
assert.ok(untitled.includes("Issue: [#9](https://github.com/o/r/issues/9)"));

// NEXT chaining: follow-ups materialize as sibling tasks after the children block
import { appendNextTasks } from "./docbind.mjs";
let ndoc = "# T\n\n- [x] Research flakiness\n  - [x] Issue: [#41](https://github.com/o/r/issues/41) — closed\n- [ ] Unrelated task\n";
ndoc = appendNextTasks(ndoc, 2, ["Fix the flaky test from https://github.com/o/r/issues/41"]);
let nlines = ndoc.split("\n");
assert.equal(nlines[4], "- [ ] Fix the flaky test from https://github.com/o/r/issues/41",
  "inserted after the child block, before the sibling");
assert.equal(nlines[5], "- [ ] Unrelated task");
ndoc = appendNextTasks(ndoc, 2, ["Fix the flaky test from https://github.com/o/r/issues/41"]);   // dedup
assert.equal(ndoc.split("\n").filter((l) => l.includes("Fix the flaky test")).length, 1);
assert.equal(appendNextTasks(ndoc, 2, []), ndoc, "no-op on empty");
// indented parent keeps its indent for the new sibling
const nested = appendNextTasks("- [ ] Outer\n  - [x] Inner done\n", 1, ["Follow up"]);
assert.equal(nested.split("\n")[2], "  - [ ] Follow up");

// slug + prompt
assert.equal(slugify("Fix the %%weird%% thing!!"), "fix-the-weird-thing");
const p = taskPrompt(t, "plans/build.md", ["rpc-notes.md"]);
assert.ok(p.includes("plans/build.md") && p.includes("rpc-notes.md") && p.includes("DONE"));

console.log("docbind: all assertions pass");
