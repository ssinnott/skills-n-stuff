/**
 * The kata web UI, framed inside Obsidian.
 *
 * Selection lives on this side, not inside the frame. An embedded page is a
 * foreign browsing context on the daemon's origin — it will not post messages
 * back, and kata's SPA has no embedding API — so Obsidian can never learn
 * what you clicked in there. Everything that drives the frame therefore
 * originates here: the queue pane, the active note's binding, or a command.
 *
 * Rendering prefers Electron's <webview> over <iframe>. A webview is a
 * separate top-level browsing context, so X-Frame-Options and
 * frame-ancestors do not apply to it — and whether the kata daemon sends
 * either is not documented. The iframe is the fallback for builds without
 * the webview tag.
 */

import { ItemView, TFile, WorkspaceLeaf } from "obsidian";

import type WfPlugin from "./main";

export const VIEW_TYPE_KATA_FRAME = "wf-kata-frame";

/** Frontmatter field naming the bound task; written by `wf bind`. */
export const KATA_ISSUE_KEY = "kata-issue";

export class KataFrameView extends ItemView {
    private plugin: WfPlugin;
    private frame: HTMLElement | null = null;
    private status: HTMLElement | null = null;
    /** When true, the frame follows whichever bound note you are reading. */
    private follow = true;
    private currentUrl = "";

    constructor(leaf: WorkspaceLeaf, plugin: WfPlugin) {
        super(leaf);
        this.plugin = plugin;
    }

    getViewType(): string {
        return VIEW_TYPE_KATA_FRAME;
    }

    getDisplayText(): string {
        return "kata";
    }

    getIcon(): string {
        return "list-checks";
    }

    async onOpen(): Promise<void> {
        this.containerEl.addClass("pi-kata-frame");
        const root = this.containerEl.children[1] as HTMLElement;
        root.empty();

        const bar = root.createDiv({ cls: "pi-kata-frame-bar" });
        const followToggle = bar.createEl("label", { cls: "pi-kata-frame-follow" });
        const checkbox = followToggle.createEl("input", { type: "checkbox" });
        checkbox.checked = this.follow;
        followToggle.createSpan({ text: "Follow note" });
        checkbox.addEventListener("change", () => {
            this.follow = checkbox.checked;
            if (this.follow) void this.syncToActiveNote();
        });

        const reload = bar.createEl("button", { text: "Reload" });
        reload.addEventListener("click", () => this.reload());

        this.status = bar.createSpan({ cls: "pi-kata-frame-status" });

        const host = root.createDiv({ cls: "pi-kata-frame-host" });
        this.frame = this.createFrame(host);

        // The frame tracks whatever bound note you move to, which is the
        // note-on-the-left, issue-on-the-right pairing this exists for.
        this.registerEvent(
            this.app.workspace.on("active-leaf-change", () => {
                if (this.follow) void this.syncToActiveNote();
            }),
        );

        await this.openOrigin();
        void this.syncToActiveNote();
    }

    /**
     * Build a webview when the tag exists, an iframe otherwise. An unknown
     * element parses as HTMLUnknownElement, which is the reliable test.
     */
    private createFrame(host: HTMLElement): HTMLElement {
        const webview = document.createElement("webview");
        if (!(webview instanceof HTMLUnknownElement)) {
            webview.setAttribute("allowpopups", "false");
            webview.addClass("pi-kata-frame-view");
            host.appendChild(webview);
            return webview;
        }

        const iframe = host.createEl("iframe", { cls: "pi-kata-frame-view" });
        iframe.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms allow-popups");
        return iframe;
    }

    private setStatus(text: string): void {
        if (this.status) this.status.setText(text);
    }

    private navigate(url: string): void {
        if (!this.frame || url === this.currentUrl) return;
        this.currentUrl = url;
        this.frame.setAttribute("src", url);
    }

    private reload(): void {
        if (!this.frame) return;
        const url = this.currentUrl;
        this.frame.setAttribute("src", "about:blank");
        window.setTimeout(() => this.frame?.setAttribute("src", url), 50);
    }

    /** Point the frame at the daemon root, resolving its port at runtime. */
    async openOrigin(): Promise<void> {
        try {
            const origin = await this.plugin.wf().uiUrl();
            this.navigate(origin);
            this.setStatus("");
        } catch (err) {
            this.setStatus(err instanceof Error ? err.message : String(err));
        }
    }

    /** Point the frame at one task. */
    async openTask(ref: string): Promise<void> {
        try {
            const url = await this.plugin.wf().uiUrl(ref);
            this.navigate(url);
            this.setStatus(ref);
        } catch (err) {
            this.setStatus(err instanceof Error ? err.message : String(err));
        }
    }

    /** Follow the active note's binding, if it has one. */
    private async syncToActiveNote(): Promise<void> {
        const file = this.app.workspace.getActiveFile();
        if (!file) return;
        const ref = this.boundIssue(file);
        if (!ref) return;
        await this.openTask(ref);
    }

    private boundIssue(file: TFile): string | null {
        const cache = this.app.metadataCache.getFileCache(file);
        const value = cache?.frontmatter?.[KATA_ISSUE_KEY];
        if (typeof value !== "string") return null;
        const trimmed = value.trim();
        if (!trimmed || trimmed === "—" || trimmed === "-") return null;
        return trimmed;
    }

    async onClose(): Promise<void> {
        this.frame = null;
    }
}
