/**
 * The wf queue, as an Obsidian pane.
 *
 * A projection, like the task board: it stores nothing. Every row is read
 * from `wf --json` and every action writes through wf, so the tracker stays
 * the single source of truth and this view can be closed and reopened
 * without losing anything.
 *
 * Selection lives here rather than in the framed kata UI, because an
 * embedded page cannot tell Obsidian what you clicked. Clicking a row opens
 * its bound note and points the frame at the task — the join the id pair
 * exists to make possible.
 */

import { ItemView, Notice, WorkspaceLeaf } from "obsidian";

import type PiTasksPlugin from "./main";
import type { WfTask } from "./wf";
import { WfError } from "./wf";

export const VIEW_TYPE_WF_QUEUE = "pi-tasks-wf-queue";

export class WfQueueView extends ItemView {
    private plugin: PiTasksPlugin;
    private body: HTMLElement | null = null;
    private refreshing = false;

    constructor(leaf: WorkspaceLeaf, plugin: PiTasksPlugin) {
        super(leaf);
        this.plugin = plugin;
    }

    getViewType(): string {
        return VIEW_TYPE_WF_QUEUE;
    }

    getDisplayText(): string {
        return "Agent queue";
    }

    getIcon(): string {
        return "list-ordered";
    }

    async onOpen(): Promise<void> {
        this.containerEl.addClass("pi-wf-queue");
        const root = this.containerEl.children[1] as HTMLElement;
        root.empty();

        const bar = root.createDiv({ cls: "pi-wf-bar" });
        const refresh = bar.createEl("button", { text: "Refresh" });
        refresh.addEventListener("click", () => void this.refresh());

        const dispatch = bar.createEl("button", { text: "Dispatch next", cls: "mod-cta" });
        dispatch.addEventListener("click", () => void this.dispatch());

        this.body = root.createDiv({ cls: "pi-wf-body" });
        await this.refresh();
    }

    async refresh(): Promise<void> {
        if (!this.body || this.refreshing) return;
        this.refreshing = true;

        try {
            const wf = this.plugin.wf();
            const [escalations, ready] = await Promise.all([wf.escalations(), wf.ready()]);

            this.body.empty();

            // Escalations first: they are the only rows that need a person,
            // and burying them under the queue is how they get missed.
            if (escalations.length > 0) {
                this.renderSection("Needs you", escalations, true);
            }

            const queued = ready.filter((t) => !t.needsHuman);
            if (queued.length > 0) {
                this.renderSection("Ready", queued, false);
            }

            if (escalations.length === 0 && queued.length === 0) {
                this.body.createDiv({ cls: "pi-wf-empty", text: "Queue is empty." });
            }
        } catch (err) {
            this.renderError(err);
        } finally {
            this.refreshing = false;
        }
    }

    private renderError(err: unknown): void {
        if (!this.body) return;
        this.body.empty();

        const box = this.body.createDiv({ cls: "pi-wf-error" });
        if (err instanceof WfError && err.missingBinary) {
            box.createEl("p", { text: err.message });
            box.createEl("p", { text: "Set the wf binary path in pi-tasks settings." });
            return;
        }
        box.createEl("p", { text: err instanceof Error ? err.message : String(err) });
    }

    private renderSection(title: string, tasks: WfTask[], urgent: boolean): void {
        if (!this.body) return;

        const section = this.body.createDiv({ cls: "pi-wf-section" });
        section.createDiv({ cls: "pi-wf-section-title", text: `${title} (${tasks.length})` });

        for (const task of tasks) {
            this.renderRow(section, task, urgent);
        }
    }

    private renderRow(parent: HTMLElement, task: WfTask, urgent: boolean): void {
        const row = parent.createDiv({ cls: urgent ? "pi-wf-row pi-wf-row-urgent" : "pi-wf-row" });

        const head = row.createDiv({ cls: "pi-wf-row-head" });
        head.createSpan({ cls: "pi-wf-ref", text: task.shortId });
        head.createSpan({ cls: "pi-wf-title", text: task.title });

        const meta = row.createDiv({ cls: "pi-wf-meta" });
        meta.createSpan({ cls: "pi-wf-chip", text: `P${task.priority}` });
        if (task.workflow) meta.createSpan({ cls: "pi-wf-chip", text: task.workflow });
        if (task.state) meta.createSpan({ cls: "pi-wf-chip", text: task.state });
        if (task.lease) {
            meta.createSpan({
                cls: task.lease.stale ? "pi-wf-chip pi-wf-chip-warn" : "pi-wf-chip",
                text: task.lease.stale ? `stale: ${task.lease.actor}` : `running: ${task.lease.actor}`,
            });
        }
        if (task.runs && task.runs > 1) meta.createSpan({ cls: "pi-wf-chip", text: `${task.runs} runs` });

        const actions = row.createDiv({ cls: "pi-wf-actions" });

        // Opening the row is the common case: note on the left, its issue
        // in the frame on the right.
        const open = actions.createEl("button", { text: task.note ? "Open note" : "Open in kata" });
        open.addEventListener("click", () => void this.open(task));

        if (!task.needsHuman) {
            const dispatch = actions.createEl("button", { text: "Dispatch" });
            dispatch.addEventListener("click", () => void this.dispatch(task.shortId));
        }

        if (task.session) {
            const attach = actions.createEl("button", { text: "Copy attach" });
            attach.addEventListener("click", () => {
                // Obsidian cannot host an interactive terminal, so the
                // handoff is the command itself.
                void navigator.clipboard.writeText(`wf attach ${task.shortId}`);
                new Notice(`Copied: wf attach ${task.shortId}`);
            });
        }
    }

    /** Open a task's note if it has one, and always point the frame at it. */
    private async open(task: WfTask): Promise<void> {
        await this.plugin.showTaskInFrame(task.shortId);
        if (task.note) {
            await this.plugin.openVaultPath(task.note);
        }
    }

    private async dispatch(ref?: string): Promise<void> {
        new Notice(ref ? `Dispatching ${ref}…` : "Dispatching the top of the queue…");
        try {
            const results = await this.plugin.wf().runOnce(ref);
            if (results.length === 0) {
                new Notice("Nothing ready.");
                return;
            }
            for (const result of results) {
                if (result.escalated) {
                    new Notice(`${result.task.shortId} needs you: ${result.reason ?? "escalated"}`, 8000);
                } else if (result.closed) {
                    new Notice(`${result.task.shortId} closed.`);
                }
                for (const note of result.notes ?? []) {
                    new Notice(`Note written: ${note}`, 6000);
                }
            }
        } catch (err) {
            new Notice(err instanceof Error ? err.message : String(err), 8000);
        } finally {
            await this.refresh();
        }
    }
}
