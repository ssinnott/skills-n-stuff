/**
 * pi-tasks plugin entry point.
 *
 * Registers the multi-instance chat view, the task board (a kanban
 * projection of the vault's task lines), and the commands that route
 * document context into pi sessions — opening a note's bound session,
 * launching agents for task lines (status written back to the line),
 * resolving @pi comments, and reopening sessions/reviews from a task
 * line. Task-line actions are callable from the cursor commands and from
 * board cards alike.
 *
 * Sessions are files under .pi-sessions/ in the vault so they travel with it.
 */

import { App, FuzzySuggestModal, MarkdownView, Notice, Plugin, PluginSettingTab, Setting, TFile, WorkspaceLeaf } from "obsidian";
import { randomUUID } from "crypto";
import { spawn, type ChildProcess } from "child_process";
import { homedir } from "os";

import { PiChatView, VIEW_TYPE_PI_TASKS_CHAT } from "./view";
import type { SessionBinding } from "./view";
import { PiBoardView, VIEW_TYPE_PI_TASKS_BOARD } from "./board";
import * as docbind from "./docbind";

const SESSION_DIR = ".pi-sessions";

export interface PiTasksSettings {
    piBinaryPath: string;
    /** Command used to launch difit (the diff review UI); run with shell. */
    difitCommand: string;
    /** Path to the GitHub CLI, used to refresh PR states. */
    ghPath: string;
    /**
     * Named pi profiles: profile name -> config directory passed to pi as
     * PI_CODING_AGENT_DIR. Each directory is a full ~/.pi/agent equivalent
     * (its settings.json decides packages/skills/extensions).
     */
    profiles: Record<string, string>;
    /** Profile used when a task/note names none. Empty = pi's default dir. */
    defaultProfile: string;
}

export const DEFAULT_SETTINGS: PiTasksSettings = {
    piBinaryPath: "pi",
    difitCommand: "npx difit",
    ghPath: "gh",
    profiles: {},
    defaultProfile: "",
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

        this.registerView(
            VIEW_TYPE_PI_TASKS_BOARD,
            (leaf: WorkspaceLeaf) => new PiBoardView(leaf, this),
        );
        this.addRibbonIcon("layout-dashboard", "Open pi task board", () => void this.openBoard());

        this.addCommand({
            id: "open-task-board",
            name: "Open pi task board",
            callback: () => void this.openBoard(),
        });

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

        this.addCommand({
            id: "open-task-session",
            name: "Open session for task under cursor",
            callback: () => void this.openTaskSession(),
        });

        this.addCommand({
            id: "open-task-review",
            name: "Open review for task under cursor",
            callback: () => void this.openTaskReview(),
        });

        this.addCommand({
            id: "refresh-pr-statuses",
            name: "Update PR statuses in this note",
            callback: () => void this.refreshPRStatuses(),
        });
    }

    // --- PR tracking ---

    /**
     * Sweep the note's PR/Issue child lines, ask `gh` for each one's state,
     * write changes back, and complete any parent task whose PRs are all
     * merged. Only PR children gate the parent — an Issue child is a
     * produced artifact whose closing isn't the task's obligation.
     */
    private async refreshPRStatuses(): Promise<void> {
        const file = this.getActiveMarkdownFile();
        if (!file) return;
        const text = await this.app.vault.read(file);
        const children = docbind.findPRChildren(text);
        if (children.length === 0) {
            new Notice("No PR or issue lines in this note");
            return;
        }
        new Notice(`Checking ${children.length} PR/issue line${children.length === 1 ? "" : "s"}…`);
        const states = new Map<string, "open" | "merged" | "closed">();
        for (const child of children) {
            const state = await this.ghChildState(child.kind, child.url);
            if (state) states.set(child.url, state);
        }
        if (states.size === 0) {
            new Notice("Could not read PR states — is `gh` installed and authenticated?");
            return;
        }
        await this.app.vault.process(file, (t) => {
            // Re-find lines against the current text, update each child,
            // then complete parents whose children are all merged.
            for (const child of docbind.findPRChildren(t)) {
                const state = states.get(child.url);
                if (state && state !== child.state) {
                    t = docbind.setPRChildState(t, child.line, state);
                }
            }
            const parents = new Set<number>();
            const updated = docbind.findPRChildren(t);
            for (const child of updated) {
                const parent = docbind.parentTaskOf(t, child.line);
                if (parent !== null) parents.add(parent);
            }
            for (const parent of parents) {
                const kids = updated.filter((c) =>
                    c.kind === "PR" && docbind.parentTaskOf(t, c.line) === parent);
                if (kids.length && kids.every((c) => c.state === "merged")) {
                    t = docbind.setTaskStatus(t, parent, "done");
                }
            }
            return t;
        });
        new Notice("PR statuses updated");
    }

    /** Query one PR's or issue's state via the gh CLI; null if unavailable. */
    private ghChildState(kind: "PR" | "Issue", url: string): Promise<"open" | "merged" | "closed" | null> {
        return new Promise((resolve) => {
            const sub = kind === "PR" ? "pr" : "issue";
            const child = spawn(this.settings.ghPath, [sub, "view", url, "--json", "state"], {});
            let out = "";
            child.stdout?.on("data", (d) => { out += String(d); });
            child.on("error", () => resolve(null));
            child.on("exit", (code) => {
                if (code !== 0) return resolve(null);
                try {
                    const state = String(JSON.parse(out).state ?? "").toUpperCase();
                    resolve(state === "MERGED" ? "merged" : state === "CLOSED" ? "closed" : state === "OPEN" ? "open" : null);
                } catch {
                    resolve(null);
                }
            });
        });
    }

    // --- Task-line hub: session and review, from the cursor or the board ---

    /** Reopen the session recorded on the task line under the cursor. */
    private async openTaskSession(): Promise<void> {
        const ctx = this.taskLineContext();
        if (!ctx) return;
        await this.openTaskSessionFrom(ctx.file, ctx.line, ctx.text);
    }

    /** Board entry point: reopen a task's session by file + line. */
    async openTaskSessionAt(file: TFile, line: number): Promise<void> {
        await this.openTaskSessionFrom(file, line, await this.app.vault.read(file));
    }

    private async openTaskSessionFrom(file: TFile, line: number, text: string): Promise<void> {
        const ref = docbind.taskSessionRef(text, line);
        if (!ref) {
            new Notice("No session recorded on this task line");
            return;
        }
        const task = docbind.taskAt(text, line);
        await this.openChatTab({
            sessionId: ref,
            notePath: null,
            title: task?.slug ?? "pi task",
            profile: docbind.taskProfileRef(text, line) ?? this.noteProfile(text),
        });
    }

    /**
     * Open the review target of the task line under the cursor. Documents
     * (the task's [[review]] link) open in the review pane; a task with
     * PRs and a repo ref gets difit over the PR's branch range in that
     * repo. Diff review is only for PRs — never for documents.
     */
    private async openTaskReview(): Promise<void> {
        const ctx = this.taskLineContext();
        if (!ctx) return;
        await this.openTaskReviewFrom(ctx.file, ctx.line, ctx.text);
    }

    /** Board entry point: open a task's review target by file + line. */
    async openTaskReviewAt(file: TFile, line: number): Promise<void> {
        await this.openTaskReviewFrom(file, line, await this.app.vault.read(file));
    }

    private async openTaskReviewFrom(file: TFile, line: number, text: string): Promise<void> {
        const task = docbind.taskAt(text, line);
        const links = task ? docbind.wikiLinks(task.text) : [];
        const target = links.length
            ? this.app.metadataCache.getFirstLinkpathDest(links[links.length - 1], file.path)
            : null;
        if (target) {
            await this.openInReviewPane(target.path);
            return;
        }
        const repo = docbind.taskRepoRef(text, line);
        const kids = docbind.findPRChildren(text)
            .filter((c) => docbind.parentTaskOf(text, c.line) === line);
        // Diff review is only for PRs; an Issue child just opens on the host.
        const pr = kids.find((c) => c.kind === "PR" && c.state === "open")
            ?? kids.find((c) => c.kind === "PR");
        if (repo && pr) {
            await this.reviewPRInDifit(repo, pr.url);
        } else if (pr ?? kids[0]) {
            window.open((pr ?? kids[0]).url);
        } else {
            new Notice("No review link, PRs, or issues on this task line");
        }
    }

    /** difit over a PR's branch range, in the repo where the work happened. */
    private async reviewPRInDifit(repoPath: string, prUrl: string): Promise<void> {
        const refs = await new Promise<{ head?: string; base?: string } | null>((resolve) => {
            const child = spawn(this.settings.ghPath,
                ["pr", "view", prUrl, "--json", "headRefName,baseRefName"], {});
            let out = "";
            child.stdout?.on("data", (d) => { out += String(d); });
            child.on("error", () => resolve(null));
            child.on("exit", (code) => {
                if (code !== 0) return resolve(null);
                try {
                    const j = JSON.parse(out);
                    resolve({ head: j.headRefName, base: j.baseRefName });
                } catch { resolve(null); }
            });
        });
        if (refs?.head && refs.base) {
            this.launchDifit(`${refs.head} ${refs.base}`, repoPath);
        } else {
            new Notice("Could not resolve PR branches via gh — opening on the host");
            window.open(prUrl);
        }
    }

    private taskLineContext(): { file: TFile; line: number; text: string } | null {
        const mdView = this.app.workspace.getActiveViewOfType(MarkdownView);
        const file = mdView?.file;
        if (!mdView || !file) {
            new Notice("No active markdown editor");
            return null;
        }
        return { file, line: mdView.editor.getCursor().line, text: mdView.editor.getValue() };
    }

    // --- Task board ---

    /** Reveal the board if one is open, else open it in a new tab. */
    async openBoard(): Promise<void> {
        const existing = this.app.workspace.getLeavesOfType(VIEW_TYPE_PI_TASKS_BOARD)[0];
        if (existing) {
            this.app.workspace.revealLeaf(existing);
            return;
        }
        const leaf = this.app.workspace.getLeaf("tab");
        await leaf.setViewState({ type: VIEW_TYPE_PI_TASKS_BOARD, active: true });
        this.app.workspace.revealLeaf(leaf);
    }

    // --- Review pane ---

    private reviewLeaf: WorkspaceLeaf | null = null;

    /**
     * Open a document in the dedicated review pane — one right-side split
     * reused across reviews, so "what we are reviewing" is a stable place.
     */
    async openInReviewPane(path: string): Promise<void> {
        const file = this.app.vault.getFileByPath(path)
            ?? this.app.metadataCache.getFirstLinkpathDest(path, "");
        if (!file) {
            new Notice(`Review target not found: ${path}`);
            return;
        }
        const detached = !this.reviewLeaf
            || (this.reviewLeaf as unknown as { parent?: unknown }).parent == null;
        if (detached) {
            this.reviewLeaf = this.app.workspace.getLeaf("split", "vertical");
        }
        await this.reviewLeaf!.openFile(file, { active: true });
    }

    async onunload(): Promise<void> {
        // View instances are unloaded by Obsidian when the plugin unloads;
        // each PiChatView.onClose destroys its own pi process.
        this.stopDifit();
    }

    // --- difit (diff review UI) ---

    private difitProcess: ChildProcess | null = null;

    /**
     * Launch difit — the diff review surface, used for PRs only (documents
     * are reviewed in the review pane). One difit at a time: relaunching
     * replaces the previous instance. difit opens the browser itself; its
     * line comments flow back via its Copy All Prompt button, pasted into
     * the task's session tab.
     */
    launchDifit(target = ".", cwd?: string): void {
        const adapter = this.app.vault.adapter as unknown as { getBasePath?: () => string };
        if (typeof adapter.getBasePath !== "function") {
            new Notice("difit review requires the desktop app");
            return;
        }
        this.stopDifit();
        const cmd = `${this.settings.difitCommand} ${target}`;
        const child = spawn(cmd, { shell: true, cwd: cwd ?? adapter.getBasePath() });
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
            const profile = this.noteProfile(await this.app.vault.read(file));
            await this.openChatTab({
                sessionId,
                notePath: file.path,
                title: file.basename,
                profile,
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
        const ctx = this.taskLineContext();
        if (!ctx) return;
        await this.launchTaskAgentFrom(ctx.file, ctx.line, ctx.text);
    }

    /** Board entry point: launch a task's agent by file + line. */
    async launchTaskAgentAt(file: TFile, line: number): Promise<void> {
        await this.launchTaskAgentFrom(file, line, await this.app.vault.read(file));
    }

    private async launchTaskAgentFrom(file: TFile, line: number, noteText: string): Promise<void> {
        const task = docbind.taskAt(noteText, line);
        if (!task) {
            new Notice("Cursor is not on a task line");
            return;
        }

        try {
            // Profile precedence: task-line marker, note frontmatter, default.
            // Record the resolved name on the line so reopening matches.
            const profile = docbind.taskProfileRef(noteText, line)
                ?? this.noteProfile(noteText);
            const sessionId = await this.newSessionPath();
            await this.app.vault.process(file, (text) => {
                text = docbind.attachSessionRef(
                    docbind.setTaskStatus(text, line, "running"),
                    line, sessionId,
                );
                return profile ? docbind.attachProfileRef(text, line, profile) : text;
            });

            // Resolve [[links]] in the task text to vault paths
            const linkedPaths = docbind.wikiLinks(task.text)
                .map((link) => this.app.metadataCache.getFirstLinkpathDest(link, file.path)?.path)
                .filter((p): p is string => typeof p === "string");

            const view = await this.openChatTab({
                sessionId,
                notePath: null,
                title: task.slug,
                profile,
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

                const { status, reviewPath, repoPath, prs, issues, next } = docbind.parseOutcome(text);
                const suffix = reviewPath
                    ? `([[${reviewPath.replace(/\.md$/, "")}|review]])`
                    : undefined;
                try {
                    await this.app.vault.process(file, (t) => {
                        t = docbind.setTaskStatus(t, line, status, suffix);
                        if (repoPath) t = docbind.attachRepoRef(t, line, repoPath);
                        t = docbind.appendPRChildren(t, line, prs, issues);
                        return docbind.appendNextTasks(t, line, next);
                    });
                } catch (err) {
                    console.error("[pi-tasks] Failed to write task status:", err);
                }
                new Notice(status === "done" ? `Task done: ${file.basename}`
                    : status === "review" ? `Task in review (${prs.length} PR${prs.length === 1 ? "" : "s"}): ${file.basename}`
                    : `Task did not complete: ${file.basename}`);
                // Documents open in the review pane. Diff review is only
                // for PRs (via "Open review for task"); nothing auto-opens
                // for plain DONE.
                if (status === "done" && reviewPath) {
                    await this.openInReviewPane(reviewPath);
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
                profile: this.noteProfile(text),
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

    // --- Profiles ---

    /**
     * Resolve a profile name to its config directory (PI_CODING_AGENT_DIR).
     * Falls back to the default profile when no name is given; returns null
     * (pi's own ~/.pi/agent) when neither applies. An unknown name warns
     * rather than silently running with a different toolset.
     */
    resolveProfileDir(name: string | null): string | null {
        const effective = name || this.settings.defaultProfile;
        if (!effective) return null;
        const dir = this.settings.profiles[effective];
        if (!dir) {
            new Notice(`pi-tasks: unknown profile "${effective}" — using pi's default config`);
            return null;
        }
        return dir.replace(/^~(?=\/|$)/, homedir());
    }

    /** The profile a note's sessions should use: frontmatter, else default. */
    private noteProfile(noteText: string): string | null {
        return docbind.getProfile(noteText) ?? (this.settings.defaultProfile || null);
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
            .setName("gh path")
            .setDesc("GitHub CLI used to refresh PR statuses on task lines")
            .addText((text) =>
                text
                    .setPlaceholder("gh")
                    .setValue(this.plugin.settings.ghPath)
                    .onChange(async (value) => {
                        this.plugin.settings.ghPath = value || "gh";
                        await this.plugin.saveSettings();
                    })
            );

        new Setting(containerEl)
            .setName("Profiles")
            .setDesc(
                "One per line: name = config directory (used as PI_CODING_AGENT_DIR; " +
                "each is a full ~/.pi/agent equivalent whose settings.json decides " +
                "packages, skills, and extensions). Tasks pick one via a " +
                "%% pi:profile=name %% marker or pi-profile frontmatter.",
            )
            .addTextArea((text) => {
                text
                    .setPlaceholder("research = ~/.pi-profiles/research\nshipping = ~/.pi-profiles/shipping")
                    .setValue(Object.entries(this.plugin.settings.profiles)
                        .map(([k, v]) => `${k} = ${v}`).join("\n"))
                    .onChange(async (value) => {
                        const profiles: Record<string, string> = {};
                        for (const line of value.split("\n")) {
                            const m = line.match(/^\s*([\w-]+)\s*=\s*(.+?)\s*$/);
                            if (m) profiles[m[1]] = m[2];
                        }
                        this.plugin.settings.profiles = profiles;
                        await this.plugin.saveSettings();
                    });
                text.inputEl.rows = 4;
                text.inputEl.style.width = "100%";
            });

        new Setting(containerEl)
            .setName("Default profile")
            .setDesc("Profile used when a task or note names none. Empty = pi's own ~/.pi/agent.")
            .addText((text) =>
                text
                    .setPlaceholder("")
                    .setValue(this.plugin.settings.defaultProfile)
                    .onChange(async (value) => {
                        this.plugin.settings.defaultProfile = value.trim();
                        await this.plugin.saveSettings();
                    })
            );
    }
}
