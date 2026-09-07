/**
 * /wf — the queue, from inside your interactive pi session.
 *
 * This extension holds no orchestration logic. It shells out to the `wf`
 * binary and renders what comes back, which is the whole point of the
 * split: workers are dispatched by a supervisor that does not care whether
 * your TUI is running, and this session is just a client with a good view.
 *
 * The one command that does more than print is `/wf attach`, which resolves
 * a task's bound pi session and switches this session to it. That is the
 * payoff of recording the session at spawn — you can drop into a worker's
 * conversation, including one that crashed, without leaving the terminal.
 *
 * Install: pi install https://github.com/ssinnott/skills-n-stuff
 * Requires the `wf` binary on PATH (or WF_BIN set).
 */

import { execFile } from "node:child_process";
import { promisify } from "node:util";

const exec = promisify(execFile);

// Minimal shapes mirroring pi's extension API. Declared locally rather than
// imported so this file compiles without pi's types present.
interface ExtensionUI {
	notify?(message: string): void;
	confirm?(message: string): Promise<boolean>;
}

interface ExtensionContext {
	cwd?: string;
	ui?: ExtensionUI;
	/** Replaces the active session with the one at this path. */
	switchSession?(sessionPath: string): Promise<void>;
}

interface CommandDefinition {
	name: string;
	description: string;
	handler(args: string, ctx: ExtensionContext): Promise<string | void> | string | void;
}

interface ExtensionAPI {
	registerCommand(command: CommandDefinition): void;
}

interface Lease {
	actor: string;
	host: string;
	renewed: string;
	stale: boolean;
}

interface Task {
	id: string;
	shortId: string;
	title: string;
	body?: string;
	priority: number;
	labels?: string[];
	owner?: string;
	state?: string;
	workflow?: string;
	lease?: Lease;
	session?: string;
	cwd?: string;
	note?: string;
	needsHuman?: boolean;
}

/**
 * `wf show <ref> --json`: the task's own fields at the top level, plus its
 * bindings, its runs (chronological, oldest first), and the ledger record's
 * last-update time. No `task` / `record` wrapper. This extension only
 * renders the run count, so bindings are left untyped rather than mirrored
 * in full.
 */
interface ShowTask extends Task {
	runs?: unknown[];
	bindings?: unknown[];
	updated?: string;
}

interface RunResult {
	task: Task;
	completed: boolean;
	escalated: boolean;
	reason?: string;
	session?: string;
	notes?: string[];
	created?: string[];
}

/**
 * `wf review --json`. A doc-kind review carries a note path and no viewer,
 * because a document is not a diff — wf decides which it is, not this.
 */
interface Review {
	ref: string;
	kind?: "pr" | "worktree" | "branch" | "doc";
	url?: string;
	port?: number;
	pid?: number;
	repo?: string;
	target?: string;
	base?: string;
	pr?: string;
	seeded?: number;
	note?: string;
	stopped?: boolean;
}

const USAGE = [
	"/wf — agent work queue",
	"",
	"  /wf                    what is ready",
	"  /wf escalations        what needs you",
	"  /wf show <ref>         one task in detail",
	"  /wf attach <ref>       switch this session to the task's agent session",
	"  /wf run <ref> [--workflow W]   run one workflow against a task",
	"  /wf run --ref <ref>    dispatch one task",
	"  /wf workflows          canned workflows that are loaded",
	"  /wf bind <ref> <note>  bind a task to a vault note",
	"  /wf review <ref>       open the task's diff in difit",
	"  /wf review <ref> --stop  stop the running viewer",
].join("\n");

function wfBin(): string {
	return process.env.WF_BIN || "wf";
}

/** Run wf and parse its JSON. Errors come back as text, not exceptions. */
async function wfJSON<T>(args: string[], cwd?: string): Promise<T | string> {
	try {
		const { stdout } = await exec(wfBin(), [...args, "--json"], {
			cwd,
			maxBuffer: 16 * 1024 * 1024,
		});
		return JSON.parse(stdout) as T;
	} catch (err) {
		const e = err as { stderr?: string; message?: string; code?: string };
		if (e.code === "ENOENT") {
			return `wf is not on PATH. Install it, or set WF_BIN to its location.`;
		}
		return (e.stderr || e.message || String(err)).trim();
	}
}

function priorityMark(priority: number): string {
	// kata's scale runs 0 (highest) to 4.
	return priority <= 1 ? "!" : " ";
}

function describeTask(task: Task): string {
	const bits: string[] = [];
	if (task.workflow) bits.push(task.workflow);
	if (task.state) bits.push(task.state);
	if (task.needsHuman) bits.push("needs you");
	if (task.lease) bits.push(task.lease.stale ? `stale lease (${task.lease.actor})` : `running as ${task.lease.actor}`);

	const suffix = bits.length > 0 ? `  — ${bits.join(", ")}` : "";
	return `${priorityMark(task.priority)} ${task.shortId.padEnd(6)} P${task.priority}  ${task.title}${suffix}`;
}

function renderTasks(tasks: Task[], empty: string): string {
	if (tasks.length === 0) return empty;
	return tasks.map(describeTask).join("\n");
}

function renderDetail(task: ShowTask): string {
	const lines = [
		`${task.shortId}  ${task.title}`,
		`id        ${task.id}`,
		`priority  ${task.priority}`,
	];
	if (task.workflow) lines.push(`workflow  ${task.workflow}`);
	if (task.labels?.length) lines.push(`labels    ${task.labels.join(", ")}`);
	if (task.state) lines.push(`state     ${task.state}`);
	if (task.owner) lines.push(`owner     ${task.owner}`);
	lines.push(`lease     ${task.lease ? `${task.lease.actor}${task.lease.stale ? " (stale)" : ""}` : "unheld"}`);
	lines.push(`session   ${task.session || "none"}`);
	if (task.runs && task.runs.length > 1) lines.push(`runs      ${task.runs.length}`);
	if (task.note) lines.push(`note      ${task.note}`);
	if (task.session) lines.push("", `Attach with: /wf attach ${task.shortId}`);
	if (task.body) lines.push("", task.body);
	return lines.join("\n");
}

function renderReview(r: Review): string {
	if (r.stopped) return `Stopped the viewer for ${r.ref}.`;
	if (r.kind === "doc") return `${r.ref} is a document, not a diff: ${r.note}`;

	const lines = [`Reviewing ${r.ref} (${r.kind}) at ${r.url}`];
	if (r.pr) lines.push(`  ${r.pr}`);
	else if (r.target) lines.push(`  ${r.target}${r.base ? ` vs ${r.base}` : ""}`);
	if (r.seeded) {
		lines.push(`  ${r.seeded} agent finding${r.seeded === 1 ? "" : "s"} seeded as comments`);
	}
	// The comments only come back by hand: difit keeps them in the page's
	// own localStorage and exposes no endpoint to read them.
	lines.push("", `Copy All Prompt in difit, then: /wf review comment ${r.ref}`);
	return lines.join("\n");
}

function renderRun(results: RunResult[]): string {
	if (results.length === 0) return "nothing ready";

	const lines: string[] = [];
	for (const r of results) {
		if (r.escalated) {
			lines.push(`${r.task.shortId} escalated — ${r.reason || "needs a human"}`);
			lines.push(`   /wf attach ${r.task.shortId}`);
		} else if (r.completed) {
			lines.push(`${r.task.shortId} run complete — ${r.task.title} (next: /wf run ${r.task.shortId} --workflow <name>, or wf close ${r.task.shortId})`);
		}
		for (const note of r.notes || []) lines.push(`   note: ${note}`);
		for (const ref of r.created || []) lines.push(`   follow-on: ${ref}`);
	}
	return lines.join("\n");
}

export default function wfExtension(pi: ExtensionAPI): void {
	pi.registerCommand({
		name: "wf",
		description: "Agent work queue: see what is ready, dispatch it, attach to a run",
		async handler(argString: string, ctx: ExtensionContext) {
			const args = argString.trim().split(/\s+/).filter(Boolean);
			const [sub = "ready", ...rest] = args;
			const cwd = ctx.cwd;

			switch (sub) {
				case "help":
				case "--help":
					return USAGE;

				case "ready": {
					const out = await wfJSON<{ tasks: Task[] }>(["ready"], cwd);
					if (typeof out === "string") return out;
					return renderTasks(out.tasks ?? [], "nothing ready");
				}

				case "escalations": {
					const out = await wfJSON<{ tasks: Task[] }>(["escalations"], cwd);
					if (typeof out === "string") return out;
					return renderTasks(out.tasks ?? [], "no escalations — nothing is waiting on you");
				}

				case "workflows": {
					const out = await wfJSON<{ workflows: { name: string; description?: string; labels?: string[] }[] }>(
						["workflows"],
						cwd,
					);
					if (typeof out === "string") return out;
					const flows = out.workflows ?? [];
					if (flows.length === 0) return "no workflows loaded";
					return flows
						.map((w) => `${w.name.padEnd(16)} ${w.description ?? ""}${w.labels?.length ? `  [${w.labels.join(", ")}]` : ""}`)
						.join("\n");
				}

				case "show": {
					if (rest.length === 0) return "usage: /wf show <ref>";
					const out = await wfJSON<ShowTask>(["show", rest[0]], cwd);
					if (typeof out === "string") return out;
					return renderDetail(out);
				}

				case "attach": {
					if (rest.length === 0) return "usage: /wf attach <ref>";
					const out = await wfJSON<Task>(["show", rest[0]], cwd);
					if (typeof out === "string") return out;

					const task = out;
					if (!task.session) {
						return `${task.shortId} has no agent session yet — it has not been dispatched.`;
					}

					// Switching in place is the whole point: the worker's
					// conversation opens here, including one that crashed.
					if (typeof ctx.switchSession === "function") {
						await ctx.switchSession(task.session);
						return `Switched to ${task.shortId}: ${task.title}`;
					}

					// Older pi builds have no session replacement; give the
					// user the command rather than failing.
					return [
						`This pi build cannot switch sessions in place. Open it in another terminal:`,
						"",
						`  pi --session ${task.session}`,
					].join("\n");
				}

				case "run": {
					const runArgs = ["run", ...rest];
					const out = await wfJSON<{ results: RunResult[] }>(runArgs, cwd);
					if (typeof out === "string") return out;
					return renderRun(out.results ?? []);
				}

				case "bind": {
					if (rest.length < 2) return "usage: /wf bind <ref> <note.md>";
					try {
						const { stdout } = await exec(wfBin(), ["bind", rest[0], rest[1]], { cwd });
						return stdout.trim();
					} catch (err) {
						const e = err as { stderr?: string; message?: string };
						return (e.stderr || e.message || String(err)).trim();
					}
				}

				case "review": {
					if (rest.length === 0) return "usage: /wf review <ref> [--stop]";
					const out = await wfJSON<Review>(["review", ...rest], cwd);
					if (typeof out === "string") return out;
					return renderReview(out);
				}

				default:
					return `unknown subcommand: ${sub}\n\n${USAGE}`;
			}
		},
	});
}
