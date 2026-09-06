/**
 * Shared mechanics for a pane that frames an external local web app inside
 * Obsidian, pulled out of kataframe.ts once difit needed the same shape:
 * webview-preferred-with-iframe-fallback frame creation, a status line, and
 * navigate/reload. What stays with each subclass is the toolbar's content and
 * what points the frame at what — that is specific to what is being framed
 * (kata's follow-the-note behaviour, difit's explicit review target), not to
 * framing itself.
 *
 * Rendering prefers Electron's <webview> over <iframe>. A webview is a
 * separate top-level browsing context, so X-Frame-Options and
 * frame-ancestors do not apply to it — the iframe is only a fallback for
 * builds without the webview tag, and for difit it is not even a compromise:
 * difit's server sends neither header.
 */

import { ItemView, WorkspaceLeaf } from "obsidian";

export abstract class FramedView extends ItemView {
    protected frame: HTMLElement | null = null;
    protected host: HTMLElement | null = null;
    protected status: HTMLElement | null = null;
    protected currentUrl = "";
    /**
     * Which branch createFrame actually took. A subclass that wants to do
     * something only a webview can (read the guest page's localStorage via
     * executeJavaScript, for instance) needs to know this for certain rather
     * than assume — frame mechanics is this class's job, so the fact of
     * which frame kind got built is exposed here rather than re-derived
     * (e.g. re-testing HTMLUnknownElement) in every subclass that cares.
     */
    protected frameKind: "webview" | "iframe" = "iframe";

    constructor(leaf: WorkspaceLeaf) {
        super(leaf);
    }

    /** CSS class prefix for this pane's elements, e.g. "pi-kata-frame". */
    protected abstract get cssPrefix(): string;

    /** Add this pane's own controls to the toolbar, left of the status line. */
    protected abstract buildToolbar(bar: HTMLElement): void;

    /** Run once the frame and host exist, e.g. to point the frame somewhere. */
    protected abstract afterOpen(): Promise<void>;

    async onOpen(): Promise<void> {
        this.containerEl.addClass(this.cssPrefix);
        const root = this.containerEl.children[1] as HTMLElement;
        root.empty();

        const bar = root.createDiv({ cls: `${this.cssPrefix}-bar` });
        this.buildToolbar(bar);
        this.status = bar.createSpan({ cls: `${this.cssPrefix}-status` });

        const host = root.createDiv({ cls: `${this.cssPrefix}-host` });
        this.host = host;
        this.frame = this.createFrame(host);

        await this.afterOpen();
    }

    /**
     * Build a webview when the tag exists, an iframe otherwise. An unknown
     * element parses as HTMLUnknownElement, which is the reliable test.
     */
    private createFrame(host: HTMLElement): HTMLElement {
        const webview = document.createElement("webview");
        if (!(webview instanceof HTMLUnknownElement)) {
            webview.setAttribute("allowpopups", "false");
            webview.addClass(`${this.cssPrefix}-view`);
            host.appendChild(webview);
            this.frameKind = "webview";
            return webview;
        }

        const iframe = host.createEl("iframe", { cls: `${this.cssPrefix}-view` });
        iframe.setAttribute("sandbox", "allow-scripts allow-same-origin allow-forms allow-popups");
        this.frameKind = "iframe";
        return iframe;
    }

    protected setStatus(text: string): void {
        if (this.status) this.status.setText(text);
    }

    protected navigate(url: string): void {
        if (!this.frame || url === this.currentUrl) return;
        this.currentUrl = url;
        this.frame.setAttribute("src", url);
    }

    protected reload(): void {
        if (!this.frame) return;
        const url = this.currentUrl;
        this.frame.setAttribute("src", "about:blank");
        window.setTimeout(() => this.frame?.setAttribute("src", url), 50);
    }

    /** Drop the frame back to nothing, e.g. once whatever it showed has ended. */
    protected blank(): void {
        this.currentUrl = "";
        this.frame?.setAttribute("src", "about:blank");
    }

    /** Convenience for the common case: a subclass just wants a Reload button. */
    protected addReloadButton(bar: HTMLElement): void {
        const reload = bar.createEl("button", { text: "Reload" });
        reload.addEventListener("click", () => this.reload());
    }

    async onClose(): Promise<void> {
        this.frame = null;
        this.host = null;
    }
}
