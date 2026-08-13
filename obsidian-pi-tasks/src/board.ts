/**
 * Kanban board over the vault's pi task lines.
 *
 * A pure projection: every card is rendered from a task line via
 * docbind.scanTasks and every action routes through the same plugin entry
 * points as the cursor commands — the board stores nothing of its own, so
 * the documents stay authoritative. Clicking a card opens its document at
 * the task line; card buttons launch agents, reopen sessions, and open
 * reviews.
 *
 * A document's tasks appear on the board when the document carries pi
 * wiring (a pi-session frontmatter binding or any %% pi:... %% marker) or
 * opts in with `pi-board: true` frontmatter — plain vault checklists stay
 * off the board.
 */

import { App, ItemView, MarkdownRenderer, MarkdownView, Modal, Setting, TFile, WorkspaceLeaf } from "obsidian";

import * as docbind from "./docbind";
import type { TaskScan } from "./docbind";
import type PiTasksPlugin from "./main";

export const VIEW_TYPE_PI_TASKS_BOARD = "pi-tasks-board";

interface BoardCard {
    file: TFile;
    task: TaskScan;
    hasSession: boolean;
    hasReview: boolean;
}

/** Inbox document created when a task is added and no board doc exists. */
const INBOX_PATH = "Pi Tasks.md";

const COLUMNS: Array<{ status: TaskScan["status"]; title: string }> = [
    { status: "todo", title: "To do" },
    { status: "running", title: "Running" },
    { status: "review", title: "In review" },
    { status: "blocked", title: "Blocked" },
    { status: "done", title: "Done" },
];

/**
 * Modal behind the board's "New task" button: task text plus a target
 * document picked from the board's contributing docs. Submitting appends
 * a plain `- [ ] …` line — the card appears in To do, ready to dispatch.
 */
class NewTaskModal extends Modal {
    private files: TFile[];
    private onSubmit: (docPath: string | null, text: string) => void;

    constructor(app: App, files: TFile[], onSubmit: (docPath: string | null, text: string) => void) {
        super(app);
        this.files = files;
        this.onSubmit = onSubmit;
    }

    onOpen(): void {
        this.titleEl.setText("New pi task");
        // Preselect the most recently modified doc — most likely the one
        // being worked; null target means "create the inbox doc".
        let docPath: string | null = this.files.length
            ? [...this.files].sort((a, b) => b.stat.mtime - a.stat.mtime)[0].path
            : null;
        let text = "";

        const submit = () => {
            this.close();
            this.onSubmit(docPath, text);
        };

        new Setting(this.contentEl)
            .setName("Task")
            .setDesc("Name a workflow in the text (like /plan-to-pr) to have the agent follow it.")
            .addText((t) => {
                t.setPlaceholder("Summarize [[meeting-notes]] into a decision list")
                    .onChange((v) => { text = v; });
                t.inputEl.style.width = "100%";
                t.inputEl.addEventListener("keydown", (e) => {
                    if (e.key === "Enter") {
                        e.preventDefault();
                        submit();
                    }
                });
                window.setTimeout(() => t.inputEl.focus(), 0);
            });

        new Setting(this.contentEl)
            .setName("Add to")
            .addDropdown((d) => {
                if (this.files.length === 0) {
                    d.addOption("", `${INBOX_PATH} (new)`);
                } else {
                    for (const f of this.files) d.addOption(f.path, f.path);
                    d.setValue(docPath ?? "");
                }
                d.onChange((v) => { docPath = v || null; });
            });

        new Setting(this.contentEl)
            .addButton((b) => b.setButtonText("Create").setCta().onClick(submit));
    }

    onClose(): void {
        this.contentEl.empty();
    }
}

export class PiBoardView extends ItemView {
    plugin: PiTasksPlugin;
    private refreshTimer: ReturnType<typeof setTimeout> | null = null;

    constructor(leaf: WorkspaceLeaf, plugin: PiTasksPlugin) {
        super(leaf);
        this.plugin = plugin;
    }

    getViewType(): string {
        return VIEW_TYPE_PI_TASKS_BOARD;
    }

    getDisplayText(): string {
        return "Pi task board";
    }

    getIcon(): string {
        return "layout-dashboard";
    }

    async onOpen(): Promise<void> {
        this.contentEl.addClass("pi-board-container");
        // Any vault change can move a card between columns; refreshes are
        // debounced and cheap (cachedRead + regex scan), so listen broadly.
        this.registerEvent(this.app.vault.on("modify", () => this.scheduleRefresh()));
        this.registerEvent(this.app.vault.on("rename", () => this.scheduleRefresh()));
        this.registerEvent(this.app.vault.on("delete", () => this.scheduleRefresh()));
        this.registerEvent(this.app.metadataCache.on("resolved", () => this.scheduleRefresh()));
        await this.refresh();
    }

    async onClose(): Promise<void> {
        if (this.refreshTimer) {
            clearTimeout(this.refreshTimer);
            this.refreshTimer = null;
        }
        this.contentEl.empty();
    }

    private scheduleRefresh(): void {
        if (this.refreshTimer) clearTimeout(this.refreshTimer);
        this.refreshTimer = setTimeout(() => {
            this.refreshTimer = null;
            void this.refresh();
        }, 400);
    }

    // --- Read model ---

    /** Documents contributing to the board, refreshed with the cards —
     *  the targets the new-task modal offers. */
    private boardFiles: TFile[] = [];

    private async collectCards(): Promise<BoardCard[]> {
        const cards: BoardCard[] = [];
        const boardFiles: TFile[] = [];
        const files = this.app.vault.getMarkdownFiles()
            .sort((a, b) => a.path.localeCompare(b.path));
        for (const file of files) {
            const cache = this.app.metadataCache.getFileCache(file);
            const hasTasks = cache?.listItems?.some((li) => li.task !== undefined) ?? false;
            const fm = cache?.frontmatter as Record<string, unknown> | undefined;
            const flagged = fm?.["pi-board"] === true || fm?.["pi-board"] === "true";
            const bound = typeof fm?.["pi-session"] === "string" && fm["pi-session"] !== "—";
            if (!hasTasks && !flagged && !bound) continue;
            const text = await this.app.vault.cachedRead(file);
            if (!flagged && !bound && !text.includes("%% pi:")) continue;
            boardFiles.push(file);
            if (!hasTasks) continue;
            const children = docbind.findPRChildren(text);
            for (const task of docbind.scanTasks(text)) {
                const kids = children.filter((c) => docbind.parentTaskOf(text, c.line) === task.line);
                cards.push({
                    file,
                    task,
                    hasSession: docbind.taskSessionRef(text, task.line) !== null,
                    hasReview: docbind.wikiLinks(task.text).length > 0 || kids.length > 0,
                });
            }
        }
        this.boardFiles = boardFiles;
        return cards;
    }

    // --- Task creation ---

    /**
     * Append a fresh task line to a board document — the board's create
     * button, still writing through the doc like everything else. With no
     * board documents yet, an inbox doc is created that opts itself in
     * via pi-board frontmatter.
     */
    private async createTask(docPath: string | null, taskText: string): Promise<void> {
        const text = taskText.trim();
        if (!text) return;
        let file = docPath ? this.app.vault.getFileByPath(docPath) : null;
        if (!file) {
            file = this.app.vault.getFileByPath(INBOX_PATH)
                ?? await this.app.vault.create(INBOX_PATH, "---\npi-board: true\n---\n\n# Pi tasks\n");
        }
        await this.app.vault.process(file, (t) => docbind.appendTask(t, text));
        this.scheduleRefresh();
    }

    // --- Rendering ---

    private async refresh(): Promise<void> {
        const cards = await this.collectCards();
        const el = this.contentEl;
        el.empty();

        const header = el.createDiv({ cls: "pi-board-header" });
        header.createSpan({ cls: "pi-board-title", text: "Pi tasks" });
        const headerActions = header.createDiv({ cls: "pi-board-header-actions" });
        const newBtn = headerActions.createEl("button", {
            cls: "pi-board-new mod-cta",
            text: "New task",
        });
        newBtn.addEventListener("click", () => {
            new NewTaskModal(this.app, this.boardFiles,
                (docPath, text) => void this.createTask(docPath, text)).open();
        });
        const refreshBtn = headerActions.createEl("button", { cls: "pi-board-refresh", text: "Refresh" });
        refreshBtn.addEventListener("click", () => void this.refresh());

        const columnsEl = el.createDiv({ cls: "pi-board-columns" });
        for (const col of COLUMNS) {
            const colCards = cards.filter((c) => c.task.status === col.status);
            const colEl = columnsEl.createDiv({ cls: "pi-board-column" });
            const head = colEl.createDiv({ cls: "pi-board-column-header" });
            head.createSpan({ text: col.title });
            head.createSpan({ cls: "pi-board-count", text: String(colCards.length) });
            const list = colEl.createDiv({ cls: "pi-board-cards" });
            if (colCards.length === 0) {
                list.createDiv({ cls: "pi-board-empty", text: "—" });
            }
            for (const card of colCards) this.renderCard(list, card);
        }

        if (cards.length === 0) {
            el.createDiv({
                cls: "pi-board-hint",
                text: "No pi tasks found. A document's checkbox tasks appear here " +
                    "once it has pi wiring (a session binding or launched task), or " +
                    "add pi-board: true to its frontmatter to include it up front.",
            });
        }
    }

    private renderCard(parent: HTMLElement, card: BoardCard): void {
        const el = parent.createDiv({ cls: `pi-board-card pi-board-card-${card.task.status}` });

        const body = el.createDiv({ cls: "pi-board-card-text" });
        void MarkdownRenderer.render(
            this.app, card.task.text || "*(empty task)*", body, card.file.path, this);

        el.createDiv({ cls: "pi-board-card-meta", text: card.file.basename });

        const actions = el.createDiv({ cls: "pi-board-card-actions" });
        const btn = (label: string, cb: () => void) => {
            const b = actions.createEl("button", { cls: "pi-board-card-btn", text: label });
            b.addEventListener("click", (e) => {
                e.stopPropagation();
                cb();
            });
        };
        if (card.task.status === "todo" || card.task.status === "blocked") {
            btn(card.task.status === "blocked" ? "Relaunch" : "Launch",
                () => void this.plugin.launchTaskAgentAt(card.file, card.task.line));
        }
        if (card.hasSession) {
            btn("Session", () => void this.plugin.openTaskSessionAt(card.file, card.task.line));
        }
        if (card.hasReview && (card.task.status === "review" || card.task.status === "done")) {
            btn("Review", () => void this.plugin.openTaskReviewAt(card.file, card.task.line));
        }

        // The whole card opens the document at the task line; clicks on
        // rendered links or the action buttons keep their own behavior.
        el.addEventListener("click", (e) => {
            if ((e.target as HTMLElement).closest("a, button")) return;
            void this.openAtLine(card.file, card.task.line);
        });
    }

    /**
     * Open the card's document at its task line — reusing a tab that
     * already shows the file, and never replacing the board's own leaf.
     */
    private async openAtLine(file: TFile, line: number): Promise<void> {
        const existing = this.app.workspace.getLeavesOfType("markdown").find((l) =>
            l.view instanceof MarkdownView && l.view.file?.path === file.path);
        const leaf = existing ?? this.app.workspace.getLeaf("tab");
        await leaf.openFile(file, { active: true, eState: { line } });
        this.app.workspace.revealLeaf(leaf);
    }
}
