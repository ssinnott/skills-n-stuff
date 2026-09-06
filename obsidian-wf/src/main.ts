/**
 * obsidian-wf plugin entry point.
 *
 * Two panes over the wf agent work queue: a queue list and a framed kata UI,
 * joined to the vault by the id pair a bound note carries — `kata-issue` in
 * its frontmatter, `wf.doc` on the task.
 *
 * The only Obsidian plugin in this repo: obsidian-pi-tasks, which bound pi
 * sessions to documents, has been removed (see DESIGN-tasks.md for what it
 * proved and what this plugin inherits). Needs only the wf binary — no agent
 * runtime runs in the vault.
 *
 * Everything here is a projection: the plugin stores nothing, every row comes
 * from `wf --json`, and every action writes back through wf.
 */

import {
    App,
    FuzzySuggestModal,
    MarkdownView,
    Modal,
    Notice,
    Plugin,
    PluginSettingTab,
    Setting,
    TFile,
    WorkspaceLeaf,
} from "obsidian";

import { WfQueueView, VIEW_TYPE_WF_QUEUE } from "./queue";
import { KataFrameView, VIEW_TYPE_KATA_FRAME, KATA_ISSUE_KEY } from "./kataframe";
import { DifitFrameView, VIEW_TYPE_DIFIT_FRAME } from "./difitframe";
import { WfClient, WfError } from "./wf";
import type { WfReview, WfRecord, WfTask } from "./wf";
import { applyBlock } from "./taskblock";

export interface WfSettings {
    /** Path to the wf binary, which fronts the agent work queue. */
    wfBinaryPath: string;
    /**
     * Directory wf runs in. wf resolves its queue project from the workspace
     * it is invoked in, so this is the folder holding .kata.toml — often the
     * repo rather than the vault. Empty = the vault root.
     */
    wfWorkspace: string;
    /** Open the kata pane automatically when a bound note is opened. */
    autoOpenFrame: boolean;
}

export const DEFAULT_SETTINGS: WfSettings = {
    wfBinaryPath: "wf",
    wfWorkspace: "",
    autoOpenFrame: false,
};

/** Picker for binding the active note to a task off the ready queue. */
class TaskSuggestModal extends FuzzySuggestModal<WfTask> {
    private tasks: WfTask[];
    private onSelect: (task: WfTask) => void;

    constructor(app: App, tasks: WfTask[], onSelect: (task: WfTask) => void) {
        super(app);
        this.tasks = tasks;
        this.onSelect = onSelect;
        this.setPlaceholder("Bind this note to which task?");
    }

    getItems(): WfTask[] {
        return this.tasks;
    }

    getItemText(item: WfTask): string {
        return `${item.shortId}  ${item.title}`;
    }

    onChooseItem(item: WfTask): void {
        this.onSelect(item);
    }
}

/** Loose enough to match GitHub Enterprise hosts too, not just github.com. */
const PR_URL_RE = /https?:\/\/\S*\/pull\/\d+\S*/;

/**
 * Obsidian has no built-in text-prompt API, so "Review a pull request…"
 * needs its own small Modal: a Setting+addText, Enter submits, autofocus.
 */
class PRUrlModal extends Modal {
    private url: string;
    private onSubmit: (url: string) => void;

    constructor(app: App, prefill: string | null, onSubmit: (url: string) => void) {
        super(app);
        this.url = prefill ?? "";
        this.onSubmit = onSubmit;
    }

    onOpen(): void {
        this.titleEl.setText("Review a pull request");

        const submit = () => {
            const trimmed = this.url.trim();
            if (!trimmed) return;
            this.close();
            this.onSubmit(trimmed);
        };

        new Setting(this.contentEl)
            .setName("Pull request URL")
            .addText((t) => {
                t.setValue(this.url)
                    .setPlaceholder("https://github.com/org/repo/pull/123")
                    .onChange((v) => {
                        this.url = v;
                    });
                t.inputEl.style.width = "100%";
                t.inputEl.addEventListener("keydown", (e) => {
                    if (e.key === "Enter") {
                        e.preventDefault();
                        submit();
                    }
                });
                // Prefilled from a detected link, so select-all lets a
                // pasted replacement overwrite it in one keystroke.
                window.setTimeout(() => {
                    t.inputEl.focus();
                    t.inputEl.select();
                }, 0);
            });

        new Setting(this.contentEl)
            .addButton((b) => b.setButtonText("Review").setCta().onClick(submit));
    }

    onClose(): void {
        this.contentEl.empty();
    }
}

export default class WfPlugin extends Plugin {
    settings: WfSettings = DEFAULT_SETTINGS;

    async onload(): Promise<void> {
        await this.loadSettings();
        this.addSettingTab(new WfSettingTab(this.app, this));

        this.registerView(
            VIEW_TYPE_WF_QUEUE,
            (leaf: WorkspaceLeaf) => new WfQueueView(leaf, this),
        );

        this.registerView(
            VIEW_TYPE_KATA_FRAME,
            (leaf: WorkspaceLeaf) => new KataFrameView(leaf, this),
        );

        this.registerView(
            VIEW_TYPE_DIFIT_FRAME,
            (leaf: WorkspaceLeaf) => new DifitFrameView(leaf, this),
        );

        this.addRibbonIcon("list-ordered", "Open agent queue", () => void this.openQueue());

        // A note that moves must not leave the tracker pointing at its old
        // path — that is how a binding rots silently over a reorganization.
        this.registerEvent(
            this.app.vault.on("rename", (file, oldPath) => {
                if (file instanceof TFile) void this.rebindRenamedNote(file, oldPath);
            }),
        );

        // Opening a bound note refreshes its task block unconditionally,
        // and — opt-in — brings its issue up beside it.
        this.registerEvent(
            this.app.workspace.on("file-open", (file) => {
                if (!file) return;
                const ref = this.boundTask(file);
                if (!ref) return;
                void this.refreshTaskBlock(file, ref);
                if (this.settings.autoOpenFrame) void this.showTaskInFrame(ref);
            }),
        );

        this.addCommand({
            id: "open-queue",
            name: "Open agent queue",
            callback: () => void this.openQueue(),
        });

        this.addCommand({
            id: "open-kata-pane",
            name: "Open kata UI pane",
            callback: () => void this.openKataFrame(),
        });

        this.addCommand({
            id: "show-note-task",
            name: "Show this note's task in the kata pane",
            callback: () => void this.showNoteTask(),
        });

        this.addCommand({
            id: "bind-note-to-task",
            name: "Bind this note to a task",
            callback: () => void this.bindNoteToTask(),
        });

        this.addCommand({
            id: "dispatch-next",
            name: "Dispatch the next ready task",
            callback: () => void this.dispatchNext(),
        });

        this.addCommand({
            id: "review-note-task",
            name: "Review this note's task",
            callback: () => void this.reviewNoteTask(),
        });

        this.addCommand({
            id: "review-pull-request",
            name: "Review a pull request…",
            callback: () => void this.reviewPullRequestCommand(),
        });

        this.addCommand({
            id: "save-review-comments",
            name: "Save review comments to the task",
            callback: () => void this.saveReviewComments(),
        });

        this.addCommand({
            id: "refresh-note-task-block",
            name: "Refresh this note's task block",
            callback: () => void this.refreshNoteTaskBlockCommand(),
        });
    }

    /**
     * A client for the wf CLI. Built per call so a settings change takes
     * effect without reloading the plugin.
     */
    wf(): WfClient {
        const adapter = this.app.vault.adapter as unknown as { getBasePath?: () => string };
        const vaultRoot = typeof adapter.getBasePath === "function" ? adapter.getBasePath() : ".";
        const cwd = this.settings.wfWorkspace.trim() || vaultRoot;
        return new WfClient(this.settings.wfBinaryPath, cwd);
    }

    async openQueue(): Promise<void> {
        await this.revealView(VIEW_TYPE_WF_QUEUE, "left");
    }

    async openKataFrame(): Promise<KataFrameView | null> {
        const leaf = await this.revealView(VIEW_TYPE_KATA_FRAME, "right");
        const view = leaf?.view;
        return view instanceof KataFrameView ? view : null;
    }

    /** Point the kata pane at a task, opening the pane if it is closed. */
    async showTaskInFrame(ref: string): Promise<void> {
        const view = await this.openKataFrame();
        await view?.openTask(ref);
    }

    async openDifitFrame(): Promise<DifitFrameView | null> {
        const leaf = await this.revealView(VIEW_TYPE_DIFIT_FRAME, "right");
        const view = leaf?.view;
        return view instanceof DifitFrameView ? view : null;
    }

    /**
     * Start review for a task and route the result. Kept in one place
     * because both the command and the queue row's Review button need
     * exactly this call-then-route sequence; only the "how do I get a ref"
     * step differs between callers.
     */
    async reviewTask(ref: string): Promise<void> {
        let review: WfReview;
        try {
            review = await this.wf().review(ref);
        } catch (err) {
            this.notifyWfError(err);
            return;
        }
        await this.routeReview(review);
    }

    /**
     * The ad-hoc counterpart to reviewTask: a PR you opened yourself, or one
     * predating the queue, has no task to review through. Same routing once
     * wf has answered — the two entry points only differ in how they ask.
     */
    async reviewPR(url: string): Promise<void> {
        let review: WfReview;
        try {
            review = await this.wf().reviewPR(url);
        } catch (err) {
            this.notifyWfError(err);
            return;
        }
        await this.routeReview(review);
    }

    /**
     * Where a review result goes once wf has answered: a document isn't a
     * diff, so it opens in the vault like any other note, never in the
     * difit frame; everything else reveals the difit pane pointed at wf's
     * session. Shared by reviewTask and reviewPR so the routing exists once.
     */
    private async routeReview(review: WfReview): Promise<void> {
        if (review.seeded) {
            new Notice(`${review.seeded} agent finding${review.seeded === 1 ? "" : "s"} seeded`);
        }

        if (review.kind === "doc") {
            await this.openVaultPath(review.note);
            return;
        }

        const view = await this.openDifitFrame();
        await view?.openReview(review);
    }

    private async reviewNoteTask(): Promise<void> {
        const file = this.app.workspace.getActiveFile();
        const ref = file ? this.boundTask(file) : null;
        if (!ref) {
            new Notice("This note is not bound to a task.");
            return;
        }
        await this.reviewTask(ref);
    }

    /**
     * Source a PR URL from wherever the user's attention already is (a link
     * on the current line or selection) before falling back to asking, so
     * the common case — cursor sitting on a PR link — is one keystroke.
     */
    private async reviewPullRequestCommand(): Promise<void> {
        new PRUrlModal(this.app, this.detectPRUrl(), (url) => void this.reviewPR(url)).open();
    }

    /** A PR link under the selection, or on the cursor's line, if any. */
    private detectPRUrl(): string | null {
        const editor = this.app.workspace.getActiveViewOfType(MarkdownView)?.editor;
        if (!editor) return null;
        const text = editor.getSelection() || editor.getLine(editor.getCursor().line);
        return text.match(PR_URL_RE)?.[0] ?? null;
    }

    /** The open difit pane, if there is one — commands that act on it (Save
     * review comments) should not conjure one into existence just to fail. */
    private difitView(): DifitFrameView | null {
        const leaf = this.app.workspace.getLeavesOfType(VIEW_TYPE_DIFIT_FRAME)[0];
        return leaf?.view instanceof DifitFrameView ? leaf.view : null;
    }

    private async saveReviewComments(): Promise<void> {
        const view = this.difitView();
        if (!view) {
            new Notice("Open the review pane first — there's nothing to harvest from.");
            return;
        }
        await view.saveCommentsCommand();
    }

    /** Same missing-binary guidance the queue pane gives, as a one-shot Notice. */
    private notifyWfError(err: unknown): void {
        if (err instanceof WfError && err.missingBinary) {
            new Notice(`${err.message}. Set the wf binary path in wf Agent Queue settings.`, 8000);
            return;
        }
        new Notice(err instanceof Error ? err.message : String(err), 8000);
    }

    async openVaultPath(path: string): Promise<void> {
        const file = this.app.vault.getAbstractFileByPath(path);
        if (!(file instanceof TFile)) {
            new Notice(`Note not found in vault: ${path}`);
            return;
        }
        await this.app.workspace.getLeaf(false).openFile(file, { active: true });
    }

    /** The task bound to a note, or null when it carries no binding. */
    boundTask(file: TFile): string | null {
        const value = this.app.metadataCache.getFileCache(file)?.frontmatter?.[KATA_ISSUE_KEY];
        if (typeof value !== "string") return null;
        const trimmed = value.trim();
        return trimmed && trimmed !== "—" && trimmed !== "-" ? trimmed : null;
    }

    private async showNoteTask(): Promise<void> {
        const file = this.app.workspace.getActiveFile();
        const ref = file ? this.boundTask(file) : null;
        if (!ref) {
            new Notice("This note is not bound to a task.");
            return;
        }
        await this.showTaskInFrame(ref);
    }

    /** Bind the active note to a task chosen from the ready queue. */
    private async bindNoteToTask(): Promise<void> {
        const file = this.app.workspace.getActiveFile();
        if (!file) {
            new Notice("Open a note first.");
            return;
        }

        let tasks: WfTask[];
        try {
            tasks = await this.wf().ready();
        } catch (err) {
            new Notice(err instanceof Error ? err.message : String(err), 8000);
            return;
        }
        if (tasks.length === 0) {
            new Notice("No ready tasks to bind to.");
            return;
        }

        new TaskSuggestModal(this.app, tasks, async (task) => {
            try {
                await this.wf().bind(task.shortId, file.path);
                new Notice(`Bound ${task.shortId} to ${file.path}`);
                await this.showTaskInFrame(task.shortId);
            } catch (err) {
                new Notice(err instanceof Error ? err.message : String(err), 8000);
            }
        }).open();
    }

    private async dispatchNext(): Promise<void> {
        new Notice("Dispatching the top of the queue…");
        try {
            const results = await this.wf().runOnce();
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
                void this.refreshTaskBlockForTask(result.task);
            }
        } catch (err) {
            new Notice(err instanceof Error ? err.message : String(err), 8000);
        } finally {
            await this.refreshQueueViews();
        }
    }

    /** Refresh every open queue pane after something changes the tracker. */
    async refreshQueueViews(): Promise<void> {
        for (const leaf of this.app.workspace.getLeavesOfType(VIEW_TYPE_WF_QUEUE)) {
            const view = leaf.view;
            if (view instanceof WfQueueView) await view.refresh();
        }
    }

    /**
     * Re-renders a note's managed task block from `wf show <ref> --json`,
     * writing it only when the bytes actually change. Called on opening a
     * bound note and after a dispatch; a fetch or render failure is a
     * console.warn, never a Notice — the note is still usable without the
     * block, and a subprocess hiccup on every open would be worse than a
     * stale block.
     */
    async refreshTaskBlock(file: TFile, ref: string): Promise<void> {
        let record: WfRecord;
        try {
            ({ record } = await this.wf().show(ref));
        } catch (err) {
            console.warn(`wf: could not fetch task ${ref} for its note block`, err);
            return;
        }

        try {
            const current = await this.app.vault.read(file);
            const next = applyBlock(current, record);
            if (next === current) return;
            await this.app.vault.process(file, (text) => applyBlock(text, record));
        } catch (err) {
            console.warn(`wf: could not render the task block for ${ref}`, err);
        }
    }

    /** Refresh the note bound to a dispatched task, if it has one open in the vault. */
    private async refreshTaskBlockForTask(task: WfTask): Promise<void> {
        if (!task.note) return;
        const file = this.app.vault.getAbstractFileByPath(task.note);
        if (file instanceof TFile) await this.refreshTaskBlock(file, task.shortId || task.id);
    }

    private async refreshNoteTaskBlockCommand(): Promise<void> {
        const file = this.app.workspace.getActiveFile();
        const ref = file ? this.boundTask(file) : null;
        if (!file || !ref) {
            new Notice("This note is not bound to a task.");
            return;
        }
        await this.refreshTaskBlock(file, ref);
    }

    /**
     * Follow a note move on the tracker side. Without this every binding
     * decays as the vault is reorganized, and the decay is silent.
     */
    private async rebindRenamedNote(file: TFile, oldPath: string): Promise<void> {
        if (!file.path.endsWith(".md")) return;
        const ref = this.boundTask(file);
        if (!ref) return;

        try {
            await this.wf().bind(ref, file.path);
        } catch (err) {
            // A tracker that is down must not block a rename; say so once.
            const detail = err instanceof Error ? err.message : String(err);
            new Notice(`Could not update the task's note path after moving ${oldPath}: ${detail}`, 8000);
        }
    }

    private async revealView(type: string, side: "left" | "right"): Promise<WorkspaceLeaf | null> {
        const existing = this.app.workspace.getLeavesOfType(type);
        if (existing.length > 0) {
            await this.app.workspace.revealLeaf(existing[0]);
            return existing[0];
        }
        const leaf = side === "left"
            ? this.app.workspace.getLeftLeaf(false)
            : this.app.workspace.getRightLeaf(false);
        if (!leaf) return null;
        await leaf.setViewState({ type, active: true });
        await this.app.workspace.revealLeaf(leaf);
        return leaf;
    }

    async loadSettings(): Promise<void> {
        const raw = (await this.loadData()) as Partial<WfSettings> | null;
        this.settings = Object.assign({}, DEFAULT_SETTINGS, raw ?? {});
    }

    async saveSettings(): Promise<void> {
        await this.saveData(this.settings);
    }
}

class WfSettingTab extends PluginSettingTab {
    private plugin: WfPlugin;

    constructor(app: App, plugin: WfPlugin) {
        super(app, plugin);
        this.plugin = plugin;
    }

    display(): void {
        const { containerEl } = this;
        containerEl.empty();

        new Setting(containerEl)
            .setName("wf binary")
            .setDesc("Path to the wf CLI, which fronts the agent work queue.")
            .addText((text) =>
                text
                    .setPlaceholder("wf")
                    .setValue(this.plugin.settings.wfBinaryPath)
                    .onChange(async (value) => {
                        this.plugin.settings.wfBinaryPath = value.trim() || "wf";
                        await this.plugin.saveSettings();
                    })
            );

        new Setting(containerEl)
            .setName("wf workspace")
            .setDesc(
                "Directory wf runs in — the folder holding .kata.toml, often the repo rather than the vault. Empty uses the vault root.",
            )
            .addText((text) =>
                text
                    .setPlaceholder("(vault root)")
                    .setValue(this.plugin.settings.wfWorkspace)
                    .onChange(async (value) => {
                        this.plugin.settings.wfWorkspace = value.trim();
                        await this.plugin.saveSettings();
                    })
            );

        new Setting(containerEl)
            .setName("Open the kata pane automatically")
            .setDesc("Open the framed kata UI when you open a note that is bound to a task.")
            .addToggle((toggle) =>
                toggle
                    .setValue(this.plugin.settings.autoOpenFrame)
                    .onChange(async (value) => {
                        this.plugin.settings.autoOpenFrame = value;
                        await this.plugin.saveSettings();
                    })
            );
    }
}
