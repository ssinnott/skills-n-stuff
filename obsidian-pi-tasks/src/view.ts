/**
 * Multi-instance, document-bound pi chat view.
 *
 * Each view owns its own PiConnection: spawned on open with
 * `--session <session file>` and cwd = vault base, destroyed on close.
 * The session binding {sessionId, notePath, title} travels through
 * Obsidian's view state so tabs survive workspace reloads.
 *
 * Streaming/rendering building blocks are imported from the unmodified
 * upstream subtree (sigilmakes/obsidian-pi-plugin, MIT).
 */

import { Component, ItemView, MarkdownRenderer, Notice, WorkspaceLeaf } from "obsidian";
import type { ViewStateResult } from "obsidian";
import { isAbsolute, join } from "path";

import { PiConnection } from "../upstream/src/rpc";
import { MessageRenderer } from "../upstream/src/renderer";
import { StreamHandler } from "../upstream/src/stream-handler";
import { generateMessageId } from "../upstream/src/message-types";
import type { ChatMessage } from "../upstream/src/message-types";
import { ChatInput } from "../upstream/src/input";
import type { Attachment } from "../upstream/src/input";
import { AttachmentPicker } from "../upstream/src/attachments";

import type PiTasksPlugin from "./main";

export const VIEW_TYPE_PI_TASKS_CHAT = "pi-tasks-chat";

/** Per-view state: which session this tab runs, and what note it belongs to. */
export interface SessionBinding {
    /** Vault-relative session file path, e.g. ".pi-sessions/<uuid>.jsonl" */
    sessionId: string;
    /** Bound note path, or null for free-standing task agents */
    notePath: string | null;
    /** Tab title */
    title: string;
    /** pi profile name (resolved to a config dir via plugin settings), or null */
    profile?: string | null;
}

type RpcEvent = Record<string, unknown>;

export class PiChatView extends ItemView {
    plugin: PiTasksPlugin;
    private binding: SessionBinding = { sessionId: "", notePath: null, title: "Pi Session" };
    private connection: PiConnection | null = null;
    private connectionHandler: ((event: RpcEvent) => void) | null = null;
    private eventListeners: Array<(event: RpcEvent) => void> = [];

    private renderer: MessageRenderer;
    private streamHandler: StreamHandler;
    private attachmentPicker: AttachmentPicker;
    private chatInput: ChatInput | null = null;
    private messagesContainer: HTMLElement | null = null;
    private inputContainer: HTMLElement | null = null;
    private abortBtn: HTMLButtonElement | null = null;
    private headerTitleEl: HTMLElement | null = null;
    private headerModelEl: HTMLElement | null = null;

    private messages: ChatMessage[] = [];
    private streaming = false;
    private userScrolledUp = false;

    /** Currently streaming assistant message element, used for live re-rendering */
    private streamingMessageEl: HTMLElement | null = null;
    /** Component for markdown renders during/after streaming */
    private streamingComponent: Component | null = null;
    /** Debounce timer for live markdown re-rendering during streaming */
    private streamRenderTimer: ReturnType<typeof setTimeout> | null = null;
    /** Latest streamed content waiting to be rendered */
    private pendingStreamContent: string | null = null;

    /** Resolved once the connection is up and prior history is rendered. */
    private readyResolve!: () => void;
    private ready: Promise<void> = new Promise((resolve) => {
        this.readyResolve = resolve;
    });
    private readyDone = false;

    constructor(leaf: WorkspaceLeaf, plugin: PiTasksPlugin, binding?: SessionBinding) {
        super(leaf);
        this.plugin = plugin;
        if (binding) this.binding = { ...binding };
        this.renderer = new MessageRenderer(this.app);
        this.attachmentPicker = new AttachmentPicker(this.app);
        this.streamHandler = new StreamHandler({
            onMessageUpdate: (msg) => this.handleStreamUpdate(msg),
            onMessageComplete: (msg) => this.handleStreamComplete(msg),
            onToolResult: (msg) => this.addMessage(msg),
            onCompaction: () => new Notice("pi conversation compacted"),
            onError: (err) => new Notice(`pi error: ${err}`),
        });
    }

    getViewType(): string {
        return VIEW_TYPE_PI_TASKS_CHAT;
    }

    getDisplayText(): string {
        return this.binding.title || "Pi Session";
    }

    getIcon(): string {
        return "message-circle";
    }

    // --- View state (multi-instance persistence) ---

    getState(): Record<string, unknown> {
        return {
            sessionId: this.binding.sessionId,
            notePath: this.binding.notePath,
            title: this.binding.title,
            profile: this.binding.profile ?? null,
        };
    }

    async setState(state: unknown, result: ViewStateResult): Promise<void> {
        const s = (state ?? {}) as Record<string, unknown>;
        if (typeof s.sessionId === "string" && s.sessionId) {
            this.binding = {
                sessionId: s.sessionId,
                notePath: typeof s.notePath === "string" ? s.notePath : null,
                title: typeof s.title === "string" && s.title ? s.title : "Pi Session",
                profile: typeof s.profile === "string" && s.profile ? s.profile : null,
            };
        }
        await super.setState(state, result);
        this.headerTitleEl?.setText(this.getDisplayText());
        // Refresh the tab header title (undocumented but stable API)
        (this.leaf as unknown as { updateHeader?: () => void }).updateHeader?.();
        this.connectIfNeeded();
    }

    getSessionId(): string {
        return this.binding.sessionId;
    }

    getConnection(): PiConnection | null {
        return this.connection;
    }

    /** Resolves after the connection is spawned and prior history rendered. */
    whenReady(): Promise<void> {
        return this.ready;
    }

    /** Register a listener for raw RPC events from this view's connection. */
    onConnectionEvent(handler: (event: RpcEvent) => void): void {
        this.eventListeners.push(handler);
    }

    // --- Lifecycle ---

    async onOpen(): Promise<void> {
        const container = this.contentEl;
        container.empty();
        container.addClass("pi-chat-container");

        // Header bar — session title + model badge
        const headerBar = container.createDiv({ cls: "pi-header-bar" });
        const left = headerBar.createDiv({ cls: "pi-header-left" });
        this.headerTitleEl = left.createSpan({
            cls: "pi-header-session-name",
            text: this.getDisplayText(),
        });
        this.headerModelEl = left.createSpan({ cls: "pi-header-model", text: "" });

        // Scrollable messages area
        const chatBody = container.createDiv({ cls: "pi-chat-body" });
        this.messagesContainer = chatBody.createDiv({ cls: "pi-messages" });

        // Input area
        this.inputContainer = container.createDiv({ cls: "pi-input-container" });
        this.chatInput = new ChatInput(this.inputContainer, {
            onSend: (text, attachments) => this.sendMessage(text, attachments),
            onAtTyped: () => this.triggerFilePicker(),
        });

        // Abort button (hidden until streaming)
        this.abortBtn = this.chatInput.getInputAreaEl().createEl("button", {
            cls: "pi-abort-btn",
            text: "Abort",
            attr: { style: "display: none;" },
        });
        this.abortBtn.addEventListener("click", () => this.abortStream());

        // Input disabled until the session is connected and history rendered
        this.chatInput.setEnabled(false);

        // Track whether user has scrolled away from bottom
        this.messagesContainer.addEventListener("scroll", () => {
            const el = this.messagesContainer;
            if (!el) return;
            const distFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
            this.userScrolledUp = distFromBottom > 100;
        });

        // On workspace restore, setState may run before or after onOpen —
        // connect from both sites; connectIfNeeded is idempotent.
        this.connectIfNeeded();
    }

    async onClose(): Promise<void> {
        // Shut down this view's pi process. PiConnection.destroy() is
        // upstream's dispose: it kills the child and rejects pending
        // requests. Unhook our event handler first so the exit event does
        // not surface a spurious error notice. Session state is durable in
        // the .jsonl session file, so this is loss-free.
        const conn = this.connection;
        this.connection = null;
        if (conn) {
            if (this.connectionHandler) conn.offEvent(this.connectionHandler);
            conn.destroy();
        }
        this.connectionHandler = null;
        this.eventListeners = [];
        this.finishReady();

        this.streamHandler.reset();
        if (this.streamRenderTimer) {
            clearTimeout(this.streamRenderTimer);
            this.streamRenderTimer = null;
        }
        this.pendingStreamContent = null;
        if (this.streamingComponent) {
            this.streamingComponent.unload();
            this.streamingComponent = null;
        }
        this.streamingMessageEl = null;

        if (this.chatInput) {
            this.chatInput.destroy();
            this.chatInput = null;
        }
        this.abortBtn = null;
        this.headerTitleEl = null;
        this.headerModelEl = null;
        this.messagesContainer = null;
        this.inputContainer = null;
        this.messages = [];
        this.contentEl.empty();
    }

    // --- Connection ownership ---

    /**
     * Spawn this view's own pi process bound to its session file.
     * Idempotent: no-op when already connected or unbound.
     */
    private connectIfNeeded(): void {
        if (this.connection || !this.binding.sessionId) return;

        const adapter = this.app.vault.adapter as unknown as { getBasePath?: () => string };
        if (typeof adapter.getBasePath !== "function") {
            new Notice("pi-tasks: cannot determine vault path (mobile is not supported)");
            this.finishReady();
            return;
        }
        const vaultRoot = adapter.getBasePath();
        const sessionArg = isAbsolute(this.binding.sessionId)
            ? this.binding.sessionId
            : join(vaultRoot, this.binding.sessionId);

        const conn = new PiConnection(
            this.plugin.settings.piBinaryPath,
            vaultRoot,
            ["--session", sessionArg],
        );
        this.connection = conn;
        const profileDir = this.plugin.resolveProfileDir(this.binding.profile ?? null);

        this.connectionHandler = (event: RpcEvent) => {
            this.streamHandler.handleEvent(event);
            const type = event.type as string;
            if (type === "agent_end") {
                this.setStreamingState(false);
                void this.refreshHeader();
            }
            for (const listener of this.eventListeners) {
                try {
                    listener(event);
                } catch (err) {
                    console.error("[pi-tasks] Event listener error:", err);
                }
            }
        };
        conn.onEvent(this.connectionHandler);
        conn.onDisconnect(() => this.handleDisconnect());
        // Upstream's PiConnection spawns with { ...process.env } and no env
        // parameter, and connect() spawns synchronously — so a profile's
        // config dir is passed by setting PI_CODING_AGENT_DIR around the
        // call. Single-threaded, so this cannot leak into other spawns.
        if (profileDir) {
            const prev = process.env.PI_CODING_AGENT_DIR;
            process.env.PI_CODING_AGENT_DIR = profileDir;
            try {
                conn.connect();
            } finally {
                if (prev === undefined) delete process.env.PI_CODING_AGENT_DIR;
                else process.env.PI_CODING_AGENT_DIR = prev;
            }
        } else {
            conn.connect();
        }

        void this.restoreHistory();
    }

    /**
     * Render prior session history: get_state (header info), then
     * get_messages rendered through the upstream renderer. Input is
     * enabled only after this completes.
     */
    private async restoreHistory(): Promise<void> {
        const conn = this.connection;
        if (!conn || !conn.isConnected()) {
            this.finishReady();
            return;
        }

        try {
            const response = await conn.send({ type: "get_state" });
            this.applyHeaderState(response.data as Record<string, unknown> | undefined);
        } catch (err) {
            console.warn("[pi-tasks] get_state failed:", err);
        }

        try {
            const response = await conn.send({ type: "get_messages" });
            const data = response.data as Record<string, unknown> | undefined;
            const rawMessages = data?.messages as Array<Record<string, unknown>> | undefined;
            if (Array.isArray(rawMessages)) {
                for (const raw of rawMessages) {
                    const msg = this.convertAgentMessage(raw);
                    if (msg) {
                        this.messages.push(msg);
                        this.renderMessage(msg);
                    }
                }
                this.scrollToBottom();
            }
        } catch (err) {
            console.warn("[pi-tasks] get_messages failed:", err);
        }

        this.finishReady();
    }

    private finishReady(): void {
        if (this.readyDone) return;
        this.readyDone = true;
        this.chatInput?.setEnabled(true);
        this.chatInput?.focus();
        this.readyResolve();
    }

    private async refreshHeader(): Promise<void> {
        const conn = this.connection;
        if (!conn?.isConnected()) return;
        try {
            const response = await conn.send({ type: "get_state" });
            this.applyHeaderState(response.data as Record<string, unknown> | undefined);
        } catch {
            // Non-fatal — header is informational
        }
    }

    private applyHeaderState(data: Record<string, unknown> | undefined): void {
        if (!data || !this.headerModelEl) return;
        const model = data.model as Record<string, unknown> | undefined;
        const modelName = (model?.name as string) ?? "";
        const thinkingLevel = (data.thinkingLevel as string) ?? "";
        let modelText = modelName;
        if (modelText && thinkingLevel && thinkingLevel !== "off") {
            modelText += ` :${thinkingLevel}`;
        }
        this.headerModelEl.setText(modelText);
        this.headerModelEl.style.display = modelText ? "" : "none";
    }

    /**
     * Reset view state after an RPC disconnect during use.
     */
    private handleDisconnect(): void {
        this.streamHandler.reset();
        this.setStreamingState(false);

        if (this.streamingMessageEl) {
            const contentEl = this.streamingMessageEl.querySelector(".pi-message-content");
            if (contentEl) {
                const existing = (contentEl as HTMLElement).getText();
                (contentEl as HTMLElement).setText(existing + "\n\n*[Connection lost]*");
            }
            this.streamingMessageEl = null;
        }
        if (this.streamingComponent) {
            this.streamingComponent.unload();
            this.streamingComponent = null;
        }

        this.connection = null;
        this.connectionHandler = null;
        this.finishReady();
        new Notice("pi disconnected — close and reopen this tab to resume the session");
    }

    // --- Messaging ---

    /**
     * Send a user message to this view's pi process.
     * During streaming, the message is sent as a steering interrupt.
     */
    sendMessage(text: string, attachments: Attachment[] = []): void {
        const conn = this.connection;
        if (!conn?.isConnected()) {
            new Notice("pi is not connected");
            return;
        }

        this.userScrolledUp = false;
        const isSteering = this.streaming;

        let displayText = text;
        const fileAttachments = attachments.filter((a) => a.type === "file");
        if (fileAttachments.length > 0) {
            const names = fileAttachments.map((a) => a.name).join(", ");
            displayText += `\n\n📎 Attached: ${names}`;
        }
        const imageAttachments = attachments.filter((a) => a.type === "image");
        if (imageAttachments.length > 0) {
            displayText += `\n\n🖼 ${imageAttachments.length} image(s) attached`;
        }

        this.addMessage({
            id: generateMessageId(),
            role: "user",
            content: displayText,
            timestamp: Date.now(),
            isSteering: isSteering || undefined,
        });

        if (!isSteering) {
            this.setStreamingState(true);
        }

        let message = text;
        for (const att of fileAttachments) {
            message += `\n\n<file path="${att.name}">\n${att.content}\n</file>`;
        }

        const command: Record<string, unknown> = {
            type: isSteering ? "steer" : "prompt",
            message,
        };
        if (imageAttachments.length > 0) {
            command.images = imageAttachments.map((img) => ({
                type: "image",
                data: img.content,
                mimeType: img.mimeType || "image/png",
            }));
        }

        try {
            conn.send(command).catch((err) => {
                console.warn("[pi-tasks] prompt request did not settle:", err);
            });
        } catch (err) {
            console.error("[pi-tasks] Failed to send message:", err);
            new Notice("Failed to send message to pi");
            if (!isSteering) {
                this.setStreamingState(false);
            }
        }
    }

    private addMessage(msg: ChatMessage): void {
        this.messages.push(msg);
        this.renderMessage(msg);
        this.scrollToBottom();
    }

    private scrollToBottom(): void {
        if (this.userScrolledUp || !this.messagesContainer) return;
        this.messagesContainer.scrollTop = this.messagesContainer.scrollHeight;
    }

    private setStreamingState(streaming: boolean): void {
        this.streaming = streaming;
        if (this.abortBtn) {
            this.abortBtn.style.display = streaming ? "inline-block" : "none";
        }
        if (this.chatInput) {
            this.chatInput.setPlaceholder(
                streaming
                    ? "Send a message to steer pi…"
                    : "Message pi… (@ for files)",
            );
        }
    }

    private abortStream(): void {
        const conn = this.connection;
        try {
            if (conn?.isConnected()) {
                conn.send({ type: "abort" }).catch(() => undefined);
            }
        } catch (err) {
            console.warn("[pi-tasks] Failed to send abort:", err);
        } finally {
            this.setStreamingState(false);
        }
    }

    private triggerFilePicker(): void {
        this.attachmentPicker.trigger((attachment) => {
            if (this.chatInput) {
                const current = this.chatInput.getValue();
                if (current.endsWith("@")) {
                    this.chatInput.setValue(current.slice(0, -1));
                }
                this.chatInput.addAttachment(attachment);
                this.chatInput.focus();
            }
        });
    }

    // --- Streaming render (structure follows upstream's view) ---

    private handleStreamUpdate(msg: ChatMessage): void {
        if (!this.messagesContainer) return;
        if (!this.streamingMessageEl) {
            this.streamingMessageEl = this.messagesContainer.createDiv({
                cls: "pi-message pi-message-assistant",
            });
            const label = this.streamingMessageEl.createDiv({ cls: "pi-message-label" });
            label.createSpan({ text: "Pi", cls: "pi-message-label-text" });
            this.streamingMessageEl.createDiv({ cls: "pi-message-content" });
        }

        const contentEl = this.streamingMessageEl.querySelector(".pi-message-content");
        if (!contentEl) return;

        if (msg.content) {
            const liveThinking = this.streamingMessageEl.querySelector(".pi-thinking-live");
            if (liveThinking) {
                (liveThinking as HTMLDetailsElement).open = false;
                liveThinking.removeClass("pi-thinking-live");
            }

            this.pendingStreamContent = msg.content;
            if (!this.streamRenderTimer) {
                this.streamRenderTimer = setTimeout(() => {
                    this.streamRenderTimer = null;
                    this.renderStreamingMarkdown();
                }, 100);
            }
        } else if (msg.thinkingContent) {
            let thinkingEl = this.streamingMessageEl.querySelector(".pi-thinking-live") as HTMLDetailsElement | null;
            if (!thinkingEl) {
                thinkingEl = createEl("details", { cls: "pi-thinking pi-thinking-live" });
                thinkingEl.open = true;
                thinkingEl.createEl("summary", { text: "Thinking…" });
                thinkingEl.createDiv({ cls: "pi-thinking-content" });
                this.streamingMessageEl.insertBefore(thinkingEl, contentEl);
            }
            const thinkingContentEl = thinkingEl.querySelector(".pi-thinking-content");
            if (thinkingContentEl) {
                (thinkingContentEl as HTMLElement).setText(msg.thinkingContent);
            }
        }

        this.scrollToBottom();
    }

    private renderStreamingMarkdown(): void {
        if (!this.streamingMessageEl || !this.pendingStreamContent) return;

        const contentEl = this.streamingMessageEl.querySelector(".pi-message-content");
        if (!contentEl) return;

        if (this.streamingComponent) {
            this.streamingComponent.unload();
        }
        this.streamingComponent = new Component();
        this.streamingComponent.load();

        // Neutralize mermaid/dataview/etc. fences during streaming —
        // they break when re-rendered on partial content.
        const safeContent = this.pendingStreamContent.replace(
            /```(mermaid|dataview|dataviewjs|query)/g,
            "```$1-preview",
        );

        contentEl.empty();
        try {
            MarkdownRenderer.render(
                this.app,
                safeContent,
                contentEl as HTMLElement,
                "",
                this.streamingComponent,
            );
        } catch (err) {
            console.error("[pi-tasks] Streaming markdown render error:", err);
            (contentEl as HTMLElement).setText(this.pendingStreamContent);
        }

        this.scrollToBottom();
    }

    private handleStreamComplete(msg: ChatMessage): void {
        this.setStreamingState(false);
        this.chatInput?.focus();

        if (this.streamRenderTimer) {
            clearTimeout(this.streamRenderTimer);
            this.streamRenderTimer = null;
        }
        this.pendingStreamContent = null;

        if (this.streamingMessageEl) {
            if (this.streamingComponent) {
                this.streamingComponent.unload();
                this.streamingComponent = null;
            }

            const contentEl = this.streamingMessageEl.querySelector(".pi-message-content");
            if (contentEl) {
                contentEl.empty();
                if (msg.content) {
                    this.streamingComponent = new Component();
                    this.streamingComponent.load();
                    try {
                        MarkdownRenderer.render(
                            this.app,
                            msg.content,
                            contentEl as HTMLElement,
                            "",
                            this.streamingComponent,
                        );
                    } catch (err) {
                        console.error("[pi-tasks] Markdown rendering error:", err);
                        (contentEl as HTMLElement).setText(msg.content);
                    }
                }
            }

            // Replace live thinking block with the final collapsed version
            const liveThinking = this.streamingMessageEl.querySelector(".pi-thinking-live, .pi-thinking");
            if (liveThinking) liveThinking.remove();

            if (msg.thinkingContent && contentEl) {
                if (!this.streamingComponent) {
                    this.streamingComponent = new Component();
                    this.streamingComponent.load();
                }
                const thinkingEl = createEl("details", { cls: "pi-thinking" });
                thinkingEl.createEl("summary", { text: "Thinking…" });
                const thinkingContentEl = thinkingEl.createDiv({ cls: "pi-thinking-content" });
                try {
                    MarkdownRenderer.render(
                        this.app,
                        msg.thinkingContent,
                        thinkingContentEl,
                        "",
                        this.streamingComponent,
                    );
                } catch (err) {
                    console.error("[pi-tasks] Thinking render error:", err);
                    thinkingContentEl.setText(msg.thinkingContent);
                }
                this.streamingMessageEl.insertBefore(thinkingEl, contentEl);
            }

            this.streamingMessageEl = null;
        }

        this.messages.push(msg);
        this.scrollToBottom();
    }

    private renderMessage(msg: ChatMessage): void {
        if (!this.messagesContainer) return;
        try {
            switch (msg.role) {
                case "user":
                    this.renderer.renderUserMessage(this.messagesContainer, msg.content, msg.isSteering);
                    break;
                case "assistant":
                    this.renderer.renderAssistantMessage(
                        this.messagesContainer,
                        msg.content,
                        "",
                        this,
                        msg.thinkingContent,
                    );
                    break;
                case "tool":
                    this.renderer.renderToolCall(
                        this.messagesContainer,
                        msg.toolName ?? "tool",
                        "",
                        msg.content,
                        msg.isError ?? false,
                        this,
                    );
                    break;
            }
        } catch (err) {
            console.error("[pi-tasks] Message render error:", err);
            const errorEl = this.messagesContainer.createDiv({ cls: "pi-message pi-render-error" });
            errorEl.createEl("p", { text: "⚠️ Failed to render message" });
            errorEl.createEl("pre", { text: msg.content });
        }
    }

    // --- History conversion (structure follows upstream's view) ---

    /**
     * Convert a pi AgentMessage from get_messages into a ChatMessage.
     */
    private convertAgentMessage(raw: Record<string, unknown>): ChatMessage | null {
        const role = raw.role as string;
        const timestamp = (raw.timestamp as number) || Date.now();

        if (role === "user") {
            const text = this.extractMessageText(raw.content);
            if (!text) return null;
            return { id: generateMessageId(), role: "user", content: text, timestamp };
        }

        if (role === "assistant") {
            const content = raw.content;
            if (!Array.isArray(content)) return null;

            const blocks = content as Array<Record<string, unknown>>;
            const text = blocks
                .filter((b) => b.type === "text" && b.text)
                .map((b) => b.text as string)
                .join("\n");
            const thinking = blocks
                .filter((b) => b.type === "thinking" && b.thinking)
                .map((b) => b.thinking as string)
                .join("\n\n");

            if (!text && !thinking) return null;
            return {
                id: generateMessageId(),
                role: "assistant",
                content: text,
                timestamp,
                thinkingContent: thinking || undefined,
            };
        }

        if (role === "toolResult") {
            const text = this.extractMessageText(raw.content);
            return {
                id: generateMessageId(),
                role: "tool",
                content: text,
                timestamp,
                toolName: (raw.toolName as string) || "tool",
                toolCallId: (raw.toolCallId as string) || undefined,
                isError: (raw.isError as boolean) || undefined,
            };
        }

        return null;
    }

    /**
     * Extract plain text from a pi message content field
     * (a string, or an array of content blocks).
     */
    private extractMessageText(content: unknown): string {
        if (typeof content === "string") return content;
        if (Array.isArray(content)) {
            return (content as Array<Record<string, unknown>>)
                .filter((b) => b.type === "text" && b.text)
                .map((b) => b.text as string)
                .join("\n");
        }
        return "";
    }
}
