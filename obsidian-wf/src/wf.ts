/**
 * Client for the `wf` CLI.
 *
 * The plugin talks to the queue through wf rather than through kata
 * directly, for the same reason the pi extension does: one process knows how
 * a task is shaped, and swapping the queue backend does not touch this file.
 * Everything here is a subprocess call returning wf's JSON protocol.
 */

import { execFile } from "child_process";

export interface WfLease {
    actor: string;
    host: string;
    renewed: string;
    stale: boolean;
}

export interface WfTask {
    id: string;
    shortId: string;
    title: string;
    body?: string;
    priority: number;
    labels?: string[];
    owner?: string;
    state?: string;
    workflow?: string;
    lease?: WfLease;
    session?: string;
    cwd?: string;
    /** Vault-relative path of the bound note, when there is one. */
    note?: string;
    needsHuman?: boolean;
}

/**
 * One typed reference wf's ledger holds against a task — a produced PR, a
 * worktree, a session, a bound note. Mirrors `jsonBinding` in wf's own
 * `cmd/wf/output.go`. `stateLabel` is the word to show a human, carried
 * rather than derived so a client that translated `state` for itself could
 * not drift from what `wf show` prints for the same binding.
 */
export interface WfBinding {
    kind: string;
    ref: string;
    label?: string;
    state?: string;
    stateLabel?: string;
    /** RFC3339 timestamp. */
    at?: string;
    /** The run id that produced this binding, empty for the task's own. */
    via?: string;
    /** Set only for a binding whose referent lives on one machine. */
    host?: string;
    meta?: Record<string, string>;
}

/** One dispatch and what it produced. Mirrors `jsonRun` in output.go. */
export interface WfRun {
    id: string;
    workflow?: string;
    profile?: string;
    model?: string;
    host?: string;
    /** RFC3339 timestamp. */
    started?: string;
    /** RFC3339 timestamp, present only once the run has ended. */
    ended?: string;
    outcome?: string;
    /** This run's own bindings — what it produced. */
    bindings?: WfBinding[];
}

/**
 * `wf show <ref> --json`: the task fields at the top level (mirrors
 * `jsonTask` in wf's `cmd/wf/output.go`), plus the task's own bindings, its
 * runs in chronological order (each with the bindings that run produced),
 * and the ledger record's last-update time. There is no `task` / `record`
 * wrapper — this is the whole response. This is what the Obsidian plugin
 * renders into a note's managed block (see taskblock.ts).
 */
export type WfShow = WfTask & {
    /** The task's own bindings — the ones no run produced. */
    bindings?: WfBinding[];
    /** Chronological, oldest first. */
    runs?: WfRun[];
    /** RFC3339 timestamp — the clock the note block's ages are anchored to. */
    updated?: string;
};

export interface WfRunResult {
    task: WfTask;
    closed: boolean;
    escalated: boolean;
    reason?: string;
    session?: string;
    notes?: string[];
    created?: string[];
}

export interface WfWorkflow {
    name: string;
    description?: string;
    profile?: string;
    labels?: string[];
    bindDocs?: boolean;
    vaultDir?: string;
}

/**
 * `wf review <ref> --json`. A discriminated union on `kind` rather than one
 * flat interface with everything optional: a document review has no url/
 * port/pid at all, and this shape makes that a compile error to forget
 * rather than a null the caller has to remember to check.
 */
export type WfReview = WfReviewSession | WfReviewDoc;

export interface WfReviewSession {
    ref: string;
    kind: "pr" | "worktree" | "branch";
    url: string;
    port: number;
    pid: number;
    repo?: string;
    target?: string;
    base?: string;
    /** PR URL, present only when kind is "pr". */
    pr?: string;
    /** Agent findings wf pre-seeded as difit comments, if any. */
    seeded?: number;
}

export interface WfReviewDoc {
    ref: string;
    kind: "doc";
    /** Vault-relative path — a document is reviewed by opening it, not framed. */
    note: string;
    seeded?: number;
}

interface WfReviewStop {
    ref: string;
    stopped: boolean;
}

/** `wf review comment <ref> --format difit --json` response shape. */
interface WfReviewComment {
    ref: string;
    commented: boolean;
    count: number;
}

export class WfError extends Error {
    /** True when the binary itself is missing, which needs a settings fix. */
    readonly missingBinary: boolean;

    constructor(message: string, missingBinary = false) {
        super(message);
        this.name = "WfError";
        this.missingBinary = missingBinary;
    }
}

export class WfClient {
    private bin: string;
    private cwd: string;

    /**
     * @param bin  path to the wf binary
     * @param cwd  a kata workspace directory — wf resolves the project from
     *             it, so this is usually the repo or vault holding .kata.toml
     */
    constructor(bin: string, cwd: string) {
        this.bin = bin || "wf";
        this.cwd = cwd;
    }

    /**
     * @param stdin  when given, written to the child's stdin and closed —
     *               every existing caller omits it and behaves exactly as
     *               before (the pipe is left open, which no current
     *               subcommand reads from anyway); `review comment` is the
     *               first caller that needs to hand wf a payload too big
     *               for an argv string.
     */
    private exec(args: string[], stdin?: string): Promise<string> {
        return new Promise((resolve, reject) => {
            const child = execFile(
                this.bin,
                args,
                { cwd: this.cwd, maxBuffer: 16 * 1024 * 1024 },
                (err, stdout, stderr) => {
                    if (!err) {
                        resolve(stdout);
                        return;
                    }
                    const code = (err as NodeJS.ErrnoException).code;
                    if (code === "ENOENT") {
                        reject(new WfError(`wf not found at "${this.bin}"`, true));
                        return;
                    }
                    reject(new WfError((stderr || err.message || String(err)).trim()));
                },
            );
            if (stdin !== undefined) {
                child.stdin?.end(stdin, "utf8");
            }
        });
    }

    private async json<T>(args: string[], stdin?: string): Promise<T> {
        const stdout = await this.exec([...args, "--json"], stdin);
        const trimmed = stdout.trim();
        if (!trimmed) return {} as T;
        try {
            return JSON.parse(trimmed) as T;
        } catch {
            throw new WfError(`wf ${args.join(" ")} returned unparseable output`);
        }
    }

    async ready(limit = 30): Promise<WfTask[]> {
        const out = await this.json<{ tasks?: WfTask[] }>(["ready", "--limit", String(limit)]);
        return out.tasks ?? [];
    }

    async escalations(): Promise<WfTask[]> {
        const out = await this.json<{ tasks?: WfTask[] }>(["escalations"]);
        return out.tasks ?? [];
    }

    /**
     * `wf show <ref> --json`: one object, the task's own fields plus its
     * bindings, runs, and the ledger record's last-update time. The
     * Obsidian plugin renders it straight into a note's managed block.
     */
    async show(ref: string): Promise<WfShow> {
        const out = await this.json<Partial<WfShow>>(["show", ref]);
        if (!out.id) throw new WfError(`no task ${ref}`);
        return out as WfShow;
    }

    async workflows(): Promise<WfWorkflow[]> {
        const out = await this.json<{ workflows?: WfWorkflow[] }>(["workflows"]);
        return out.workflows ?? [];
    }

    /** Dispatch one task, or the top of the queue when ref is omitted. */
    async runOnce(ref?: string): Promise<WfRunResult[]> {
        const args = ref ? ["run", "--ref", ref] : ["run", "--once"];
        const out = await this.json<{ results?: WfRunResult[] }>(args);
        return out.results ?? [];
    }

    async run(max: number): Promise<WfRunResult[]> {
        const out = await this.json<{ results?: WfRunResult[] }>(["run", "--max", String(max)]);
        return out.results ?? [];
    }

    /** Bind a task to a vault note, writing both halves of the id pair. */
    async bind(ref: string, notePath: string): Promise<void> {
        await this.exec(["bind", ref, notePath]);
    }

    /**
     * Deep link for a task, or the daemon's origin when ref is omitted.
     * The port is not fixed, so this is asked for rather than configured.
     */
    async uiUrl(ref?: string): Promise<string> {
        const args = ref ? ["ui", ref] : ["ui"];
        const out = await this.exec(args);
        return out.trim();
    }

    /**
     * Start (or resume) reviewing a task. wf decides what "reviewing" means
     * for this ref — a difit session over a PR, worktree, or branch, or just
     * the bound document — this client only reports what came back.
     */
    async review(ref: string): Promise<WfReview> {
        return this.json<WfReview>(["review", ref]);
    }

    /**
     * The ad-hoc counterpart to review(ref): a PR that never went through a
     * task (opened by hand, or predating the queue) has no ref to review by,
     * so this hands wf the PR URL directly and lets it derive one. Same
     * response shape as review() — kind "pr", with `ref` filled in as
     * something like "owner/repo#123" rather than left empty.
     */
    async reviewPR(url: string): Promise<WfReview> {
        return this.json<WfReview>(["review", "--pr", url]);
    }

    /** Stop the difit process wf started for this ref, if any is running. */
    async stopReview(ref: string): Promise<void> {
        await this.json<WfReviewStop>(["review", ref, "--stop"]);
    }

    /**
     * Hand wf a comment store harvested out of difit's own frame (see
     * DifitFrameView.readCommentStore) so it can be recorded against the
     * task. The store can be sizeable — potentially several diffs' worth of
     * threads sitting in one origin's localStorage — so it goes over stdin
     * rather than argv, unlike every other call in this file. Returns how
     * many comments wf actually recorded for this ref (the store may carry
     * threads for other repos/commit-ranges that share the origin; wf sorts
     * that out, this client just reports the count it hands back).
     */
    async reviewCommentDifit(ref: string, storeJson: string): Promise<number> {
        const out = await this.json<WfReviewComment>(
            ["review", "comment", ref, "--format", "difit"],
            storeJson,
        );
        return out.count ?? 0;
    }
}
