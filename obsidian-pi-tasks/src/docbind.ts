/**
 * Document-binding logic: session frontmatter, task lines, @pi comments,
 * wiki-link extraction. Pure functions over strings — no Obsidian imports —
 * so the whole file is unit-testable outside the app.
 */

const FRONTMATTER = /^---\n([\s\S]*?)\n---\n?/;
const SESSION_KEY = /^pi-session:\s*(\S+)\s*$/m;
const PROFILE_KEY = /^pi-profile:\s*(\S+)\s*$/m;

/** Read the pi-session id from a note's frontmatter, if bound. */
export function getSessionId(noteText: string): string | null {
  const fm = noteText.match(FRONTMATTER);
  if (!fm) return null;
  const m = fm[1].match(SESSION_KEY);
  return m && m[1] !== "—" ? m[1] : null;
}

/** Read the note's default pi profile name from frontmatter, if set. */
export function getProfile(noteText: string): string | null {
  const fm = noteText.match(FRONTMATTER);
  if (!fm) return null;
  const m = fm[1].match(PROFILE_KEY);
  return m && m[1] !== "—" ? m[1] : null;
}

/** Return note text with pi-session set, creating frontmatter if needed. */
export function setSessionId(noteText: string, id: string): string {
  const fm = noteText.match(FRONTMATTER);
  if (!fm) return `---\npi-session: ${id}\n---\n\n${noteText}`;
  const body = fm[1];
  const updated = SESSION_KEY.test(body)
    ? body.replace(SESSION_KEY, `pi-session: ${id}`)
    : `${body}\npi-session: ${id}`;
  return noteText.replace(FRONTMATTER, `---\n${updated}\n---\n`);
}

export interface TaskLine {
  line: number;        // 0-based index into the note's lines
  text: string;        // task text without the checkbox marker
  checked: boolean;
  slug: string;        // session-name-safe identifier
}

const TASK = /^(\s*)- \[([ xX])\]\s+(.*)$/;
const PI_MARKER = /\s*%%\s*pi:(\w+)=(\S+)\s*%%/g;

/** Parse the task line at a given line number, if it is one. */
export function taskAt(noteText: string, line: number): TaskLine | null {
  const raw = noteText.split("\n")[line];
  if (raw === undefined) return null;
  const m = raw.match(TASK);
  if (!m) return null;
  const text = m[3].replace(PI_MARKER, "").trim();
  return { line, text, checked: m[2] !== " ", slug: slugify(text) };
}

/** Read a hidden %% pi:<key>=... %% marker on a task line, if present. */
export function taskMarker(noteText: string, line: number, key: string): string | null {
  const raw = noteText.split("\n")[line];
  if (raw === undefined) return null;
  for (const m of raw.matchAll(PI_MARKER)) {
    if (m[1] === key) return m[2];
  }
  return null;
}

/** Append/replace a hidden %% pi:<key>=... %% marker on a task line. */
export function attachMarker(noteText: string, line: number, key: string, value: string): string {
  const lines = noteText.split("\n");
  const raw = lines[line];
  if (raw === undefined || !TASK.test(raw)) return noteText;
  const keyed = new RegExp(`\\s*%%\\s*pi:${key}=\\S+\\s*%%`);
  lines[line] = keyed.test(raw)
    ? raw.replace(keyed, ` %% pi:${key}=${value} %%`)
    : `${raw} %% pi:${key}=${value} %%`;
  return lines.join("\n");
}

/** Back-compat helpers for the two markers in use. */
export const taskSessionRef = (t: string, l: number) => taskMarker(t, l, "session");
export const attachSessionRef = (t: string, l: number, v: string) => attachMarker(t, l, "session", v);
export const taskRepoRef = (t: string, l: number) => taskMarker(t, l, "repo");
export const attachRepoRef = (t: string, l: number, v: string) => attachMarker(t, l, "repo", v);
export const taskProfileRef = (t: string, l: number) => taskMarker(t, l, "profile");
export const attachProfileRef = (t: string, l: number, v: string) => attachMarker(t, l, "profile", v);

/**
 * Parse a task agent's final text. DONE/BLOCKED decide done vs failed;
 * "DONE — review: <path>" names a review document; "PR: <url> [— title]"
 * lines (or "DONE — pr: <url>") report pull requests, which put the task
 * in review rather than done — it completes when its PRs merge.
 */
export function parseOutcome(text: string): {
  status: "done" | "failed" | "review";
  reviewPath: string | null;
  repoPath: string | null;
  prs: { url: string; title?: string }[];
  issues: { url: string; title?: string }[];
  next: string[];
} {
  const done = !/\bBLOCKED\b/.test(text) && /\bDONE\b/.test(text);
  const rm = text.match(/\bDONE\b\s*[—–-]*\s*review:\s*(\S+)/i);
  const repo = text.match(/^\s*REPO:\s*(\S+)\s*$/im);
  const prs: { url: string; title?: string }[] = [];
  for (const m of text.matchAll(/^\s*PR:\s*(https?:\/\/\S+?)(?:\s+—\s+(.+?))?\s*$/gim)) {
    prs.push({ url: m[1], title: m[2]?.trim() });
  }
  const dm = text.match(/\bDONE\b\s*[—–-]*\s*pr:\s*(https?:\/\/\S+)/i);
  if (dm && !prs.some((p) => p.url === dm[1])) prs.push({ url: dm[1] });
  const issues: { url: string; title?: string }[] = [];
  for (const m of text.matchAll(/^\s*ISSUE:\s*(https?:\/\/\S+?)(?:\s+—\s+(.+?))?\s*$/gim)) {
    issues.push({ url: m[1], title: m[2]?.trim() });
  }
  const next: string[] = [];
  for (const m of text.matchAll(/^\s*NEXT:\s*(.+?)\s*$/gim)) {
    next.push(m[1]);
  }
  // Only PRs gate completion: a created issue is a produced artifact, a
  // shipped PR is an obligation still in flight.
  const status = !done ? "failed" : prs.length > 0 ? "review" : "done";
  return {
    status, reviewPath: rm ? rm[1] : null, repoPath: repo ? repo[1] : null,
    prs, issues, next,
  };
}

export function slugify(text: string): string {
  return text.toLowerCase()
    .replace(/\[\[|\]\]|[`*_%]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48) || "task";
}

/** Mark a task line as running / review / done / failed, preserving indentation. */
export function setTaskStatus(
  noteText: string, line: number,
  status: "running" | "review" | "done" | "failed", suffix?: string,
): string {
  const lines = noteText.split("\n");
  const m = lines[line]?.match(TASK);
  if (!m) return noteText;
  const box = status === "done" ? "x" : " ";
  const refs = [...m[3].matchAll(PI_MARKER)].map((r) => r[0].trim());
  const ref = refs.join(" ");
  const clean = m[3].replace(PI_MARKER, "").replace(/\s*(⏳|❌|🔃)\s*$/u, "").trimEnd();
  const marker = status === "running" ? " ⏳"
    : status === "failed" ? " ❌"
    : status === "review" ? " 🔃" : "";
  const tail = suffix ? ` ${suffix}` : "";
  lines[line] = `${m[1]}- [${box}] ${clean}${marker}${tail}${ref ? ` ${ref}` : ""}`;
  return lines.join("\n");
}

// --- PR tracking: child lines under a task, one per pull request ---

export interface PRChild {
  line: number;
  kind: "PR" | "Issue";
  title: string;
  url: string;
  state: "open" | "merged" | "closed";
}

const PR_CHILD = /^(\s*)- \[([ xX])\] (PR|Issue): \[([^\]]+)\]\((\S+?)\)(?:\s+—\s+(open|merged|closed))?\s*$/;

/** All PR child lines in a note. */
export function findPRChildren(noteText: string): PRChild[] {
  const out: PRChild[] = [];
  noteText.split("\n").forEach((raw, i) => {
    const m = raw.match(PR_CHILD);
    if (m) {
      out.push({
        line: i, kind: m[3] as PRChild["kind"], title: m[4], url: m[5],
        state: (m[6] as PRChild["state"]) ?? (m[2] !== " " ? "merged" : "open"),
      });
    }
  });
  return out;
}

/** Insert PR/Issue child lines under a task (skipping already-listed URLs). */
export function appendPRChildren(
  noteText: string, taskLine: number,
  prs: { url: string; title?: string }[],
  issues: { url: string; title?: string }[] = [],
): string {
  const lines = noteText.split("\n");
  const m = lines[taskLine]?.match(TASK);
  if (!m) return noteText;
  const existing = new Set(findPRChildren(noteText).map((c) => c.url));
  const indent = m[1] + "  ";
  const render = (kind: "PR" | "Issue") => (item: { url: string; title?: string }) => {
    const n = item.url.match(/\/(?:pull|merge_requests|issues)\/(\d+)/);
    const title = item.title ?? (n ? `#${n[1]}` : item.url);
    return `${indent}- [ ] ${kind}: [${title}](${item.url}) — open`;
  };
  const fresh = [
    ...prs.filter((x) => !existing.has(x.url)).map(render("PR")),
    ...issues.filter((x) => !existing.has(x.url)).map(render("Issue")),
  ];
  if (fresh.length) lines.splice(taskLine + 1, 0, ...fresh);
  return lines.join("\n");
}

/** Update one PR child line's state (merged checks its box). */
export function setPRChildState(noteText: string, line: number, state: PRChild["state"]): string {
  const lines = noteText.split("\n");
  const m = lines[line]?.match(PR_CHILD);
  if (!m) return noteText;
  const kind = m[3] as PRChild["kind"];
  const complete = state === "merged" || (kind === "Issue" && state === "closed");
  const box = complete ? "x" : " ";
  lines[line] = `${m[1]}- [${box}] ${kind}: [${m[4]}](${m[5]}) — ${state}`;
  return lines.join("\n");
}

/**
 * Materialize NEXT: follow-ups as sibling task lines after the task and
 * its children — the chaining primitive: one workflow's outcome becomes
 * the next launchable task, with artifact links carried in the text.
 */
export function appendNextTasks(noteText: string, taskLine: number, next: string[]): string {
  if (next.length === 0) return noteText;
  const lines = noteText.split("\n");
  const m = lines[taskLine]?.match(TASK);
  if (!m) return noteText;
  const indent = m[1];
  let insert = taskLine + 1;
  while (insert < lines.length) {
    const ind = (lines[insert].match(/^\s*/) ?? [""])[0];
    if (lines[insert].trim() === "" || ind.length <= indent.length) break;
    insert++;
  }
  const existing = new Set(
    lines.map((l) => l.match(TASK)?.[3]?.replace(PI_MARKER, "").trim()).filter(Boolean),
  );
  const fresh = next.filter((n) => !existing.has(n)).map((n) => `${indent}- [ ] ${n}`);
  if (fresh.length) lines.splice(insert, 0, ...fresh);
  return lines.join("\n");
}

/** The parent task line of a PR child: nearest task line above with less indent. */
export function parentTaskOf(noteText: string, childLine: number): number | null {
  const lines = noteText.split("\n");
  const childIndent = (lines[childLine]?.match(/^\s*/) ?? [""])[0].length;
  for (let i = childLine - 1; i >= 0; i--) {
    const m = lines[i]?.match(TASK);
    if (m && m[1].length < childIndent) return i;
    if (lines[i].trim() === "") return null;
  }
  return null;
}

export interface TaskScan {
  line: number;
  /** Display text: hidden markers and the trailing status emoji removed. */
  text: string;
  status: "todo" | "running" | "review" | "blocked" | "done";
  slug: string;
}

/**
 * Scan a whole note for task lines, classifying each by the status the
 * plugin writes back (⏳ 🔃 ❌, checked box). PR/Issue child lines are
 * excluded — they belong to their parent task's card. This is the board's
 * read model: a pure projection of the document, never a second store.
 */
export function scanTasks(noteText: string): TaskScan[] {
  const out: TaskScan[] = [];
  noteText.split("\n").forEach((raw, i) => {
    const m = raw.match(TASK);
    if (!m || PR_CHILD.test(raw)) return;
    const cleaned = m[3].replace(PI_MARKER, "").trim();
    const status: TaskScan["status"] = m[2] !== " " ? "done"
      : cleaned.endsWith("⏳") ? "running"
      : cleaned.endsWith("🔃") ? "review"
      : cleaned.endsWith("❌") ? "blocked"
      : "todo";
    const text = cleaned.replace(/\s*(⏳|❌|🔃)\s*$/u, "").trim();
    out.push({ line: i, text, status, slug: slugify(text) });
  });
  return out;
}

export interface PiComment {
  start: number;       // char offset of the opening %%
  end: number;         // char offset just past the closing %%
  instruction: string;
}

const PI_COMMENT = /%%\s*@pi:\s*([\s\S]*?)\s*%%/g;

/** Find all %% @pi: ... %% markers in a note. */
export function findComments(noteText: string): PiComment[] {
  const out: PiComment[] = [];
  for (const m of noteText.matchAll(PI_COMMENT)) {
    out.push({ start: m.index, end: m.index + m[0].length, instruction: m[1] });
  }
  return out;
}

/** Extract [[wiki-link]] targets (without aliases or headings). */
export function wikiLinks(noteText: string): string[] {
  const out = new Set<string>();
  for (const m of noteText.matchAll(/\[\[([^\]|#]+)(?:[|#][^\]]*)?\]\]/g)) {
    out.add(m[1].trim());
  }
  return [...out];
}

/** Build the seed prompt for a task agent. */
export function taskPrompt(task: TaskLine, notePath: string, linkedPaths: string[]): string {
  const links = linkedPaths.length
    ? `\nLinked context files:\n${linkedPaths.map((p) => `- ${p}`).join("\n")}`
    : "";
  return (
    `Work on this task from ${notePath}:\n\n${task.text}\n${links}\n\n` +
    `The task document itself is at ${notePath} — read it for surrounding ` +
    `context before starting. If the task names a workflow, slash command, ` +
    `or skill (like /plan-to-pr), follow that workflow. When finished, ` +
    `report on the final lines: "DONE — review: <path>" if the deliverable ` +
    `is a document a reviewer should read (vault-relative path); one ` +
    `"PR: <url> — <title>" line per pull request you opened plus one ` +
    `"REPO: <absolute path>" line naming the repository you worked in, ` +
    `then "DONE", if the work went out as pull requests; one ` +
    `"ISSUE: <url> — <title>" line per issue you filed; one ` +
    `"NEXT: <task text>" line per follow-up task the outcome calls for ` +
    `(include the artifact's link in the text so the next agent starts ` +
    `from it); plain "DONE" for direct code changes; "BLOCKED" and why ` +
    `if you are stuck. Never invent URLs or paths.`
  );
}
