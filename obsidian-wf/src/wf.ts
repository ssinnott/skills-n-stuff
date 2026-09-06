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
    runs?: number;
    /** Vault-relative path of the bound note, when there is one. */
    note?: string;
    needsHuman?: boolean;
}

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

    private exec(args: string[]): Promise<string> {
        return new Promise((resolve, reject) => {
            execFile(
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
        });
    }

    private async json<T>(args: string[]): Promise<T> {
        const stdout = await this.exec([...args, "--json"]);
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

    async show(ref: string): Promise<WfTask> {
        const out = await this.json<{ task?: WfTask }>(["show", ref]);
        if (!out.task) throw new WfError(`no task ${ref}`);
        return out.task;
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

    /** Stop the difit process wf started for this ref, if any is running. */
    async stopReview(ref: string): Promise<void> {
        await this.json<WfReviewStop>(["review", ref, "--stop"]);
    }
}
