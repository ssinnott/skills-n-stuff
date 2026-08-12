/**
 * Document-binding logic: session frontmatter, task lines, @pi comments,
 * wiki-link extraction. Pure functions over strings — no Obsidian imports —
 * so the whole file is unit-testable outside the app.
 */

const FRONTMATTER = /^---\n([\s\S]*?)\n---\n?/;
const SESSION_KEY = /^pi-session:\s*(\S+)\s*$/m;

/** Read the pi-session id from a note's frontmatter, if bound. */
export function getSessionId(noteText: string): string | null {
  const fm = noteText.match(FRONTMATTER);
  if (!fm) return null;
  const m = fm[1].match(SESSION_KEY);
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
const SESSION_REF = /\s*%%\s*pi:session=(\S+)\s*%%/;

/** Parse the task line at a given line number, if it is one. */
export function taskAt(noteText: string, line: number): TaskLine | null {
  const raw = noteText.split("\n")[line];
  if (raw === undefined) return null;
  const m = raw.match(TASK);
  if (!m) return null;
  const text = m[3].replace(SESSION_REF, "").trim();
  return { line, text, checked: m[2] !== " ", slug: slugify(text) };
}

/** Read the hidden session reference on a task line, if present. */
export function taskSessionRef(noteText: string, line: number): string | null {
  const raw = noteText.split("\n")[line];
  const m = raw?.match(SESSION_REF);
  return m ? m[1] : null;
}

/** Append a hidden %% pi:session=... %% marker to a task line (idempotent). */
export function attachSessionRef(noteText: string, line: number, sessionPath: string): string {
  const lines = noteText.split("\n");
  const raw = lines[line];
  if (raw === undefined || !TASK.test(raw)) return noteText;
  lines[line] = SESSION_REF.test(raw)
    ? raw.replace(SESSION_REF, ` %% pi:session=${sessionPath} %%`)
    : `${raw} %% pi:session=${sessionPath} %%`;
  return lines.join("\n");
}

/** Parse a task agent's final text: DONE / BLOCKED, optional review path. */
export function parseOutcome(text: string): { status: "done" | "failed"; reviewPath: string | null } {
  const done = !/\bBLOCKED\b/.test(text) && /\bDONE\b/.test(text);
  const m = text.match(/\bDONE\b\s*[—–-]*\s*review:\s*(\S+)/i);
  return { status: done ? "done" : "failed", reviewPath: m ? m[1] : null };
}

export function slugify(text: string): string {
  return text.toLowerCase()
    .replace(/\[\[|\]\]|[`*_%]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 48) || "task";
}

/** Mark a task line as running / done / failed, preserving indentation. */
export function setTaskStatus(
  noteText: string, line: number,
  status: "running" | "done" | "failed", suffix?: string,
): string {
  const lines = noteText.split("\n");
  const m = lines[line]?.match(TASK);
  if (!m) return noteText;
  const box = status === "done" ? "x" : " ";
  const refMatch = m[3].match(SESSION_REF);
  const ref = refMatch ? refMatch[0].trim() : "";
  const clean = m[3].replace(SESSION_REF, "").replace(/\s*(⏳|❌)\s*$/u, "").trimEnd();
  const marker = status === "running" ? " ⏳" : status === "failed" ? " ❌" : "";
  const tail = suffix ? ` ${suffix}` : "";
  lines[line] = `${m[1]}- [${box}] ${clean}${marker}${tail}${ref ? ` ${ref}` : ""}`;
  return lines.join("\n");
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
    `context before starting. When the task is complete, end with a final ` +
    `line of exactly "DONE — review: <path>" where <path> is the one ` +
    `document a reviewer should read (vault-relative), or just "DONE" if ` +
    `the result is code changes rather than a document. If you are ` +
    `blocked, end with "BLOCKED" and why.`
  );
}
