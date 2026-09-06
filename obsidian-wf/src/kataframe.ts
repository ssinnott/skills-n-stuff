/**
 * The kata web UI, framed inside Obsidian.
 *
 * Selection lives on this side, not inside the frame. An embedded page is a
 * foreign browsing context on the daemon's origin — it will not post messages
 * back, and kata's SPA has no embedding API — so Obsidian can never learn
 * what you clicked in there. Everything that drives the frame therefore
 * originates here: the queue pane, the active note's binding, or a command.
 *
 * Frame mechanics (webview/iframe creation, navigate/reload/status) live in
 * frame.ts, shared with difitframe.ts. What stays here is kata-specific:
 * following the active note's binding, and the toolbar's follow toggle.
 */

import { TFile, WorkspaceLeaf } from "obsidian";

import { FramedView } from "./frame";
import type WfPlugin from "./main";

export const VIEW_TYPE_KATA_FRAME = "wf-kata-frame";

/** Frontmatter field naming the bound task; written by `wf bind`. */
export const KATA_ISSUE_KEY = "kata-issue";

export class KataFrameView extends FramedView {
    private plugin: WfPlugin;
    /** When true, the frame follows whichever bound note you are reading. */
    private follow = true;

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

    protected get cssPrefix(): string {
        return "pi-kata-frame";
    }

    protected buildToolbar(bar: HTMLElement): void {
        const followToggle = bar.createEl("label", { cls: "pi-kata-frame-follow" });
        const checkbox = followToggle.createEl("input", { type: "checkbox" });
        checkbox.checked = this.follow;
        followToggle.createSpan({ text: "Follow note" });
        checkbox.addEventListener("change", () => {
            this.follow = checkbox.checked;
            if (this.follow) void this.syncToActiveNote();
        });

        this.addReloadButton(bar);
    }

    protected async afterOpen(): Promise<void> {
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
}
