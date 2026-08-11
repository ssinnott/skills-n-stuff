// Example pi extension that doubles as a template for this repo.
// Pi compiles TypeScript on the fly (via jiti); the type-only import below is
// erased at runtime, so this file needs no installed dependencies.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  pi.on("session_start", async (_event, ctx) => {
    ctx.ui.notify("skills-n-stuff example-extension loaded ✅", "info");
  });

  pi.registerCommand("drawer-ping", {
    description: "Confirm the skills-n-stuff extension is active",
    handler: async (_args, ctx) => {
      ctx.ui.notify("example-extension is active", "info");
    },
  });
}
