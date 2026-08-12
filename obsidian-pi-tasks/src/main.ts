/**
 * pi-tasks plugin entry point.
 *
 * Registers the multi-instance chat view and three commands that route
 * document context into pi sessions:
 *   - Open pi session for this note (resolve-or-create pi-session frontmatter)
 *   - Launch pi agent for task line (new session, status written back to the line)
 *   - Resolve @pi comments in this note (sent to the note's bound session)
 *
 * Sessions are files under .pi-sessions/ in the vault so they travel with it.
 */

import { App, FuzzySuggestModal, MarkdownView, Notice, Plugin, PluginSettingTab, Setting, TFile, WorkspaceLeaf } from "obsidian";
import { randomUUID } from "crypto";
import { spawn, type ChildProcess } from "child_process";

import { PiChatView, VIEW_TYPE_PI_TASKS_CHAT } from "./view";
import type { SessionBinding } from "./view";
import * as docbind from "./docbind";

const SESSION_DIR = ".pi-sessions";

export interface PiTasksSettings {
    piBinaryPath: string;
    /** Command used to launch difit (the diff review UI); run with shell. */
    difitCommand: string;
    /** Launch difit automatically when a task agent finishes with DONE. */
    difitOnDone: boolean;
}

export const DEFAULT_SETTINGS: PiTasksSettings = {
    piBinaryPath: "pi",
    difitCommand: "npx difit",
    difitOnDone: true,
};

interface ModelOption {
    id: string;
    name: string;
    provider: string;
}

/** Modal for selecting a model for the active view's connection. */
class ModelSwitchModal extends FuzzySuggestModal<ModelOption> {
    private models: ModelOption[];
    private onSelect: (model: ModelOption) => void;

    constructor(app: App, models: ModelOption[], onSelect: (model: ModelOption) => void) {
        super(app);
        this.models = models;
        this.onSelect = onSelect;
        this.setPlaceholder("Select a model...");
    }

    getItems(): ModelOption[] {
        return this.models;
    }

    getItemText(item: ModelOption): string {
        return `${item.name} (${item.provider})`;
    }

    onChooseItem(item: ModelOption): void {
        this.onSelect(item);
    }
}

export default class PiTasksPlugin extends Plugin {
    settings: PiTasksSettings = DEFAULT_SETTINGS;

    async onload(): Promise<void> {
        await this.loadSettings();
        this.addSettingTab(new PiTasksSettingTab(this.app, this));

        this.registerView(
            VIEW_TYPE_PI_TASKS_CHAT,
            (leaf: WorkspaceLeaf) => new PiChatView(leaf, this),
        );

        this.addCommand({
            id: "open-note-session",
            name: "Open pi session for this note",
            callback: () => void this.openNoteSession(),
        });

        this.addCommand({
            id: "launch-task-agent",
            name: "Launch pi agent for task line",
            callback: () => void this.launchTaskAgent(),
        });

        this.addCommand({
            id: "resolve-pi-comments",
            name: "Resolve @pi comments in this note",
            callback: () => void this.resolveComments(),
        });

        this.addCommand({
            id: "switch-model",
            name: "Switch model (active pi tab)",
            callback: () => void this.switchModel(),
        });

        this.addCommand({
            id: "review-in-difit",
            name: "Review changes in difit",
            callback: () => this.launchDifit(),
        });
    }

    async onunload(): Promise<void> {
        // View instances are unloaded by Obsidian when the plugin unloads;
        // each PiChatView.onClose destroys its own pi process.
        this.stopDifit();
    }

    // --- difit (diff review UI) ---

    private difitProcess: ChildProcess | null = null;

    /**
     * Launch difit over the vault's uncommitted changes ("difit ." — the
     * changes a task agent just made). One difit at a time: relaunching
     * replaces the previous instance. difit opens the browser itself; its
     * line comments flow back via its Copy All Prompt button, pasted into
     * the task's session tab.
     */
    launchDifit(target = "."): void {
        const adapter = this.app.vault.adapter as unknown as { getBasePath?: () => string };
        if (typeof adapter.getBasePath !== "function") {
            new Notice("difit review requires the desktop app");
            return;
        }
        this.stopDifit();
        const cmd = `${this.settings.difitCommand} ${target}`;
        const child = spawn(cmd, { shell: true, cwd: adapter.getBasePath() });
        this.difitProcess = child;
        child.on("error", (err) => {
            new Notice(`difit failed to start: ${err.message}`);
            if (this.difitProcess === child) this.difitProcess = null;
        });
        child.on("exit", (code) => {
            if (this.difitProcess === child) this.difitProcess = null;
            // Non-zero without ever serving usually means not a git repo
            // or difit missing; surface it once rather than silently.
            if (code !== null && code !== 0) {
                new Notice(`difit exited with code ${code} — is the vault a git repo?`);
            }
        });
        new Notice("Launching difit review…");
    }

    private stopDifit(): void {
        if (this.difitProcess) {
            this.difitProcess.kill();
            this.difitProcess = null;
        }
    }

    async loadSettings(): Promise<void> {
        const raw = (await this.loadData()) as Partial<PiTasksSettings> | null;
        this.settings = Object.assign({}, DEFAULT_SETTINGS, raw ?? {});
    }

    async saveSettings(): Promise<void> {
        await this.saveData(this.settings);
    }

    // --- Commands ---

    /**
     * Open (or reveal) the chat tab bound to the active note, creating and
     * writing a pi-session frontmatter binding if the note has none.
     */
    private async openNoteSession(): Promise<void> {
        const file = this.getActiveMarkdownFile();
        if (!file) return;

        try {
            const sessionId = await this.resolveOrCreateBinding(file);
            await this.openChatTab({
                sessionId,
                notePath: file.path,
                title: file.basename,
            });
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            new Notice(`pi-tasks: ${msg}`);
        }
    }

    /**
     * Launch a dedicated agent for the task line under the cursor.
     * The task line (not the frontmatter) records the outcome: the line is
     * marked running now, and done/failed when the agent settles.
     */
    private async launchTaskAgent(): Promise<void> {
        const mdView = this.app.workspace.getActiveViewOfType(MarkdownView);
        const file = mdView?.file;
        if (!mdView || !file) {
            new Notice("No active markdown editor");
            return;
        }

        const line = mdView.editor.getCursor().line;
        const task = docbind.taskAt(mdView.editor.getValue(), line);
        if (!task) {
            new Notice("Cursor is not on a task line");
            return;
        }

        try {
            const sessionId = await this.newSessionPath();
            await this.app.vault.process(file, (text) =>
                docbind.setTaskStatus(text, line, "running"),
            );

            // Resolve [[links]] in the task text to vault paths
            const linkedPaths = docbind.wikiLinks(task.text)
                .map((link) => this.app.metadataCache.getFirstLinkpathDest(link, file.path)?.path)
                .filter((p): p is string => typeof p === "string");

            const view = await this.openChatTab({
                sessionId,
                notePath: null,
                title: task.slug,
            });
            await view.whenReady();
            view.sendMessage(docbind.taskPrompt(task, file.path, linkedPaths));
            this.watchTaskCompletion(view, file, line);
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            new Notice(`pi-tasks: ${msg}`);
        }
    }

    /**
     * When the task agent settles, read its last assistant text and write
     * done/failed back to the task line.
     */
    private watchTaskCompletion(view: PiChatView, file: TFile, line: number): void {
        let settled = false;
        view.onConnectionEvent((event) => {
            const type = event.type as string;
            // agent_settled is the settle signal; accept agent_end as a
            // fallback in case the running pi version only emits that.
            if (settled || (type !== "agent_settled" && type !== "agent_end")) return;
            settled = true;
            void (async () => {
                let text = "";
                try {
                    const conn = view.getConnection();
                    if (conn?.isConnected()) {
                        const response = await conn.send({ type: "get_last_assistant_text" });
                        const data = response.data;
                        text = typeof data === "string"
                            ? data
                            : String((data as Record<string, unknown> | undefined)?.text ?? "");
                    }
                } catch (err) {
                    console.warn("[pi-tasks] get_last_assistant_text failed:", err);
                }

                const status = !/\bBLOCKED\b/.test(text) && /\bDONE\b/.test(text)
                    ? "done" as const
                    : "failed" as const;
                try {
                    await this.app.vault.process(file, (t) =>
                        docbind.setTaskStatus(t, line, status),
                    );
                } catch (err) {
                    console.error("[pi-tasks] Failed to write task status:", err);
                }
                new Notice(status === "done"
                    ? `Task done: ${file.basename}`
                    : `Task did not complete: ${file.basename}`);
                if (status === "done" && this.settings.difitOnDone) {
                    this.launchDifit();
                }
            })();
        });
    }

    /**
     * Send the @pi comment resolution instruction to the note's bound session.
     */
    private async resolveComments(): Promise<void> {
        const file = this.getActiveMarkdownFile();
        if (!file) return;

        try {
            const text = await this.app.vault.read(file);
            const comments = docbind.findComments(text);
            if (comments.length === 0) {
                new Notice("No @pi comments in this note");
                return;
            }

            const sessionId = await this.resolveOrCreateBinding(file);
            const view = await this.openChatTab({
                sessionId,
                notePath: file.path,
                title: file.basename,
            });
            await view.whenReady();
            view.sendMessage(
                `Resolve the @pi comment markers in ${file.path}: apply each ` +
                `instruction scoped to its surrounding text, then delete each ` +
                `%% @pi: ... %% marker. Do not change anything else.`,
            );
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            new Notice(`pi-tasks: ${msg}`);
        }
    }

    /**
     * Switch the model of the active pi chat tab's connection.
     */
    private async switchModel(): Promise<void> {
        const view = this.app.workspace.getActiveViewOfType(PiChatView);
        const conn = view?.getConnection();
        if (!conn?.isConnected()) {
            new Notice("No active pi chat tab");
            return;
        }

        try {
            const response = await conn.send({ type: "get_available_models" });
            const data = response.data as Record<string, unknown> | undefined;
            const models = (data?.models as Array<Record<string, unknown>>) ?? [];
            if (models.length === 0) {
                new Notice("No models available");
                return;
            }

            const options: ModelOption[] = models.map((m) => ({
                id: (m.id as string) ?? "",
                name: (m.name as string) ?? (m.id as string) ?? "Unknown",
                provider: (m.provider as string) ?? "",
            })).filter((m) => m.id);

            new ModelSwitchModal(this.app, options, async (selected) => {
                try {
                    await conn.send({
                        type: "set_model",
                        provider: selected.provider,
                        modelId: selected.id,
                    });
                    new Notice(`Switched to ${selected.name}`);
                } catch (err) {
                    const msg = err instanceof Error ? err.message : String(err);
                    new Notice(`Failed to switch model: ${msg}`);
                }
            }).open();
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            new Notice(`Failed to fetch models: ${msg}`);
        }
    }

    // --- Session binding helpers ---

    private getActiveMarkdownFile(): TFile | null {
        const file = this.app.workspace.getActiveFile();
        if (!file || file.extension !== "md") {
            new Notice("No active note");
            return null;
        }
        return file;
    }

    /**
     * Read the note's pi-session binding; create one (and write it into the
     * frontmatter via vault.process) if the note is unbound.
     */
    private async resolveOrCreateBinding(file: TFile): Promise<string> {
        const text = await this.app.vault.read(file);
        const existing = docbind.getSessionId(text);
        if (existing) return existing;

        const sessionId = await this.newSessionPath();
        await this.app.vault.process(file, (t) => docbind.setSessionId(t, sessionId));
        return sessionId;
    }

    /**
     * Mint a new vault-relative session file path under .pi-sessions/,
     * creating the folder if missing. `pi --session` accepts a path, so
     * sessions live in the vault and travel with it.
     */
    private async newSessionPath(): Promise<string> {
        const adapter = this.app.vault.adapter;
        if (!(await adapter.exists(SESSION_DIR))) {
            await adapter.mkdir(SESSION_DIR);
        }
        return `${SESSION_DIR}/${randomUUID()}.jsonl`;
    }

    /**
     * Reveal the existing tab bound to this session, or open a new one.
     * The binding travels through the leaf's view state so tabs survive
     * workspace reloads.
     */
    async openChatTab(binding: SessionBinding): Promise<PiChatView> {
        const { workspace } = this.app;

        for (const leaf of workspace.getLeavesOfType(VIEW_TYPE_PI_TASKS_CHAT)) {
            const view = leaf.view;
            if (view instanceof PiChatView && view.getSessionId() === binding.sessionId) {
                workspace.revealLeaf(leaf);
                return view;
            }
        }

        const leaf = workspace.getLeaf("tab");
        await leaf.setViewState({
            type: VIEW_TYPE_PI_TASKS_CHAT,
            active: true,
            state: { ...binding },
        });
        workspace.revealLeaf(leaf);

        const view = leaf.view;
        if (!(view instanceof PiChatView)) {
            throw new Error("Failed to open pi chat view");
        }
        return view;
    }
}

class PiTasksSettingTab extends PluginSettingTab {
    plugin: PiTasksPlugin;

    constructor(app: App, plugin: PiTasksPlugin) {
        super(app, plugin);
        this.plugin = plugin;
    }

    display(): void {
        const { containerEl } = this;
        containerEl.empty();

        new Setting(containerEl)
            .setName("pi binary path")
            .setDesc("Path to the pi executable")
            .addText((text) =>
                text
                    .setPlaceholder("pi")
                    .setValue(this.plugin.settings.piBinaryPath)
                    .onChange(async (value) => {
                        this.plugin.settings.piBinaryPath = value || "pi";
                        await this.plugin.saveSettings();
                    })
            );

        new Setting(containerEl)
            .setName("difit command")
            .setDesc("Command used to launch the difit diff review UI")
            .addText((text) =>
                text
                    .setPlaceholder("npx difit")
                    .setValue(this.plugin.settings.difitCommand)
                    .onChange(async (value) => {
                        this.plugin.settings.difitCommand = value || "npx difit";
                        await this.plugin.saveSettings();
                    })
            );

        new Setting(containerEl)
            .setName("Review in difit when a task finishes")
            .setDesc("Automatically open difit on the vault's uncommitted changes when a task agent reports DONE")
            .addToggle((toggle) =>
                toggle
                    .setValue(this.plugin.settings.difitOnDone)
                    .onChange(async (value) => {
                        this.plugin.settings.difitOnDone = value;
                        await this.plugin.saveSettings();
                    })
            );
    }
}
