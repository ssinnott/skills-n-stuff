/**
 * difit, framed inside Obsidian exactly the way kataframe.ts frames kata's
 * own UI, and for the same reason: reimplementing diff rendering here would
 * just be a second renderer to keep in sync with difit's. difit's server
 * sends neither X-Frame-Options nor frame-ancestors, so the plain <iframe>
 * fallback in frame.ts is not a compromise here the way it might be for kata.
 *
 * Unlike the kata pane, this one never follows the active note — reviewing
 * is an explicit action ("Review this note's task", a queue row's Review
 * button), not something to walk into by opening a note. And unlike kata,
 * this pane cannot read anything back out of the frame at all: difit keeps
 * comments in the embedded page's own localStorage, with no API to poll, so
 * the loop this pane supports ends at "Copy All Prompt" — the user pastes
 * that into the task's session by hand. wf.ts's WfClient has no method that
 * reads comments back, on purpose.
 */

import { WorkspaceLeaf } from "obsidian";

import { FramedView } from "./frame";
import type WfPlugin from "./main";
import type { WfReview } from "./wf";

export const VIEW_TYPE_DIFIT_FRAME = "wf-difit-frame";

export class DifitFrameView extends FramedView {
    private plugin: WfPlugin;
    private review: WfReview | null = null;
    private refEl: HTMLElement | null = null;
    private emptyEl: HTMLElement | null = null;
    private stopBtn: HTMLButtonElement | null = null;

    constructor(leaf: WorkspaceLeaf, plugin: WfPlugin) {
        super(leaf);
        this.plugin = plugin;
    }

    getViewType(): string {
        return VIEW_TYPE_DIFIT_FRAME;
    }

    getDisplayText(): string {
        return "review";
    }

    getIcon(): string {
        return "git-compare";
    }

    protected get cssPrefix(): string {
        return "pi-difit-frame";
    }

    protected buildToolbar(bar: HTMLElement): void {
        this.refEl = bar.createSpan({ cls: "pi-difit-frame-ref" });

        this.addReloadButton(bar);

        const stop = bar.createEl("button", { text: "Stop" });
        stop.addEventListener("click", () => void this.stop());
        this.stopBtn = stop;
    }

    protected async afterOpen(): Promise<void> {
        // A bare iframe with no src reads as broken, not as "nothing yet" —
        // say the latter in words instead of leaving the pane blank.
        if (this.host) {
            this.emptyEl = this.host.createDiv({
                cls: "pi-difit-frame-empty",
                text: 'Nothing under review. Run "Review this note\'s task" or a queue row\'s Review button.',
            });
        }
        this.showEmptyState();
    }

    /** Point the frame at a review session that wf has already started. */
    async openReview(review: WfReview): Promise<void> {
        if (review.kind === "doc") {
            // A document is not a diff — callers are expected to have already
            // routed doc-kind reviews to opening the note instead of here.
            this.setStatus(`${review.ref} is a document, not a diff — nothing to frame.`);
            return;
        }

        this.review = review;
        this.showFrame();
        if (this.refEl) {
            this.refEl.setText(`${review.ref} · ${review.kind}${review.pr ? ` · ${review.pr}` : ""}`);
        }
        if (this.stopBtn) this.stopBtn.disabled = false;
        this.navigate(review.url);
        this.setStatus("");
    }

    private async stop(): Promise<void> {
        if (!this.review) return;
        const ref = this.review.ref;
        if (this.stopBtn) this.stopBtn.disabled = true;

        try {
            await this.plugin.wf().stopReview(ref);
        } catch (err) {
            this.setStatus(err instanceof Error ? err.message : String(err));
            if (this.stopBtn) this.stopBtn.disabled = false;
            return;
        }

        this.review = null;
        this.blank();
        this.showEmptyState();
    }

    private showEmptyState(): void {
        this.setStatus("");
        if (this.refEl) this.refEl.setText("");
        if (this.stopBtn) this.stopBtn.disabled = true;
        if (this.frame) this.frame.hidden = true;
        if (this.emptyEl) this.emptyEl.hidden = false;
    }

    private showFrame(): void {
        if (this.frame) this.frame.hidden = false;
        if (this.emptyEl) this.emptyEl.hidden = true;
    }

    async onClose(): Promise<void> {
        this.review = null;
        await super.onClose();
    }
}
