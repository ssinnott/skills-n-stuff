/**
 * difit, framed inside Obsidian exactly the way kataframe.ts frames kata's
 * own UI, and for the same reason: reimplementing diff rendering here would
 * just be a second renderer to keep in sync with difit's. difit's server
 * sends neither X-Frame-Options nor frame-ancestors, so the plain <iframe>
 * fallback in frame.ts is not a compromise here the way it might be for kata.
 *
 * Unlike the kata pane, this one never follows the active note — reviewing
 * is an explicit action ("Review this note's task", a queue row's Review
 * button, "Review a pull request…"), not something to walk into by opening
 * a note.
 *
 * Unlike kata, this pane *can* read something back out of the frame — but
 * only on the webview path, and only by a side door. difit exposes no
 * comments API (`/api/diff` exists; there is no `/api/comments`, and nothing
 * here polls for one), but it does keep every comment thread in the guest
 * page's own localStorage, namespaced per repo+commit-range under keys
 * shaped like `difit-storage-v1/<repo hash>/<base>-<target>` (observed by
 * inspecting a running difit page — not documented, so treat the exact shape
 * as best-effort and lean on wf's parser to be the tolerant side of it). An
 * Electron `<webview>` is a separate top-level browsing context that the
 * host process can still reach into via `executeJavaScript`, which is how
 * readCommentStore() pulls that store out without difit ever having to push
 * anything. A plain `<iframe>` fallback has no such bridge — it is
 * cross-origin from Obsidian's own frame like any other iframe, so its
 * localStorage is genuinely unreachable from here, not just unimplemented.
 * On that path harvesting says so instead of quietly finding nothing, and
 * "Copy All Prompt" stays the fallback route.
 */

import { Notice, WorkspaceLeaf } from "obsidian";

import { FramedView } from "./frame";
import type WfPlugin from "./main";
import type { WfReview } from "./wf";

export const VIEW_TYPE_DIFIT_FRAME = "wf-difit-frame";

/**
 * Minimal shape of Electron's <webview> tag that this file drives. Kept
 * local rather than pulling in @types/electron for one method — frame.ts's
 * createFrame only needs DOM-generic methods (setAttribute, addClass), so
 * this is the first file that actually needs webview-specific typing.
 */
interface ElectronWebviewTag extends HTMLElement {
    executeJavaScript(code: string, userGesture?: boolean): Promise<unknown>;
}

/** What came back from trying to harvest and send difit's comments. */
type HarvestOutcome =
    | { kind: "saved"; count: number }
    | { kind: "none" }
    | { kind: "unsupported" };

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

    /**
     * Stop the difit process — but harvest first, always. difit's comment
     * store is scoped to this review's own origin (localhost:<port>, plus a
     * key that itself embeds the commit range under review), and neither
     * half of that is guaranteed to come back: wf may hand the next review
     * of this same ref a different port, and a new commit on the same PR
     * opens a different key with no prior comments. Once this process is
     * dead, whatever a viewer left in that page's localStorage may be
     * unreachable forever — there is no "reconnect and try again".
     *
     * So harvesting must run, and succeed (or find nothing to harvest),
     * BEFORE stopReview is ever called. If the harvest or the send to wf
     * fails, this returns without stopping anything — the viewer stays
     * alive, the comments are still sitting in the page, and the user can
     * fall back to difit's own "Copy All Prompt". Treating a failed harvest
     * as "stop anyway" is exactly the bug this ordering exists to prevent.
     */
    private async stop(): Promise<void> {
        if (!this.review) return;
        const ref = this.review.ref;
        if (this.stopBtn) this.stopBtn.disabled = true;

        let harvestMsg: string;
        try {
            harvestMsg = this.describeHarvest(await this.saveComments());
        } catch (err) {
            const detail = err instanceof Error ? err.message : String(err);
            this.setStatus(`Not stopping — could not save comments: ${detail}`);
            if (this.stopBtn) this.stopBtn.disabled = false;
            return;
        }

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
        new Notice(harvestMsg);
    }

    /**
     * Harvest difit's comment store from the frame and send it to the task,
     * without stopping anything. Shared by the Stop flow above and the
     * standalone "Save review comments to the task" command — both need
     * exactly this, Stop just refuses to proceed past a failure here.
     */
    async saveComments(): Promise<HarvestOutcome> {
        if (!this.review) return { kind: "none" };
        if (this.frameKind !== "webview") return { kind: "unsupported" };

        const raw = await this.readCommentStore();
        if (!raw || !storeHasThreads(raw)) return { kind: "none" };

        const count = await this.plugin.wf().reviewCommentDifit(this.review.ref, raw);
        return count > 0 ? { kind: "saved", count } : { kind: "none" };
    }

    /** Entry point for the standalone command: harvest, send, and report. */
    async saveCommentsCommand(): Promise<void> {
        if (!this.review) {
            new Notice("Nothing under review.");
            return;
        }
        try {
            new Notice(this.describeHarvest(await this.saveComments()));
        } catch (err) {
            new Notice(err instanceof Error ? err.message : String(err), 8000);
        }
    }

    private describeHarvest(outcome: HarvestOutcome): string {
        switch (outcome.kind) {
            case "saved":
                return `${outcome.count} comment${outcome.count === 1 ? "" : "s"} saved to the task.`;
            case "none":
                return "No review comments to save.";
            case "unsupported":
                return 'Can\'t harvest comments in this build (no webview available) — use difit\'s "Copy All Prompt" instead.';
        }
    }

    /**
     * Pull every difit-storage-v1/* key out of the frame's localStorage and
     * return them as one JSON object (key -> raw stored value), or null when
     * none exist yet. This has to enumerate rather than getItem a fixed key:
     * difit namespaces storage per repo+commit-range, so a single origin can
     * hold several such keys (one per diff it has ever shown on this port),
     * and there is no way to know the exact key for *this* review without
     * reproducing difit's own hashing. Handing wf the whole map and letting
     * it pick out this ref's entries is simpler and more robust than trying
     * to derive the key here.
     *
     * Only valid on the webview path — callers must check frameKind first;
     * this assumes it and has nothing sensible to do against an iframe.
     */
    private async readCommentStore(): Promise<string | null> {
        const webview = this.frame as ElectronWebviewTag;
        const script = `(() => {
            const out = {};
            for (let i = 0; i < localStorage.length; i++) {
                const k = localStorage.key(i);
                if (k && k.startsWith('difit-storage-v1/')) out[k] = localStorage.getItem(k);
            }
            return JSON.stringify(out);
        })()`;
        const result = await webview.executeJavaScript(script);
        if (typeof result !== "string" || result === "{}") return null;
        return result;
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

/**
 * True when the harvested map has at least one thread anywhere in it. A
 * store can carry a difit-storage-v1/* key with zero threads (the user
 * opened the diff and left, or cleared its comments) — that must count as
 * "nothing to save" so Stop is never blocked by an empty store, and so the
 * standalone save command doesn't round-trip to wf for nothing.
 *
 * Parses defensively: the value shape is difit's own (observed, not
 * documented — see the class comment above), so anything that doesn't look
 * like `{ threads: [...] }` is treated as no threads rather than an error.
 */
function storeHasThreads(raw: string): boolean {
    let map: Record<string, unknown>;
    try {
        map = JSON.parse(raw) as Record<string, unknown>;
    } catch {
        return false;
    }
    for (const value of Object.values(map)) {
        const entry = typeof value === "string" ? safeParse(value) : value;
        const threads = (entry as { threads?: unknown } | null)?.threads;
        if (Array.isArray(threads) && threads.length > 0) return true;
    }
    return false;
}

function safeParse(raw: string): unknown {
    try {
        return JSON.parse(raw);
    } catch {
        return null;
    }
}
