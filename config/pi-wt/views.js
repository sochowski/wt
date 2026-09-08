export default function registerViews(pi, run, root) {
  pi.registerTool({
    name: "wt_view", label: "Worktree views",
    description: "Manage WT views without raw tmux. Every window has a mandatory left master (default 60%) and vertically stacked right support views. Create/place defaults to joining the caller window stack; placement window creates a background window. Anchor chooses the window, not arbitrary geometry. Promote selects a master by stable view ID; master-width sets its percentage (20..80). Reflow preserves focus, identities and ownership, including pinned/peer panes. Destructive source moves/close/park still protect pinned, human-active and peer-managed views. Inspect list and reuse views. Prefer a checkout-targeted editor with files:[\".\"] for repository browsing instead of a bare editor. When making edits, proactively open/reuse a diff in the editing agent's window, preserving focus and active presentations. Target the actual checkout alias, not root plus repos shortcuts. Managed diffs use Unified to show checkout-wide changes including untracked files against an exact commit, with debounced external refresh and selected-file/reading-position recovery. Unsaved buffers are not overwritten. They are not per-file/per-agent author proof and may include others' changes. Large selected files can take time to render. Conversations and named shells keep separate windows. Only humans open/focus views.",
    parameters: { type: "object", additionalProperties: false, required: ["action"], properties: {
      action: { type: "string", enum: ["list", "show", "create", "place", "park", "close", "promote", "master-width"] },
      view: { type: "string" }, kind: { type: "string", enum: ["editor", "diff"] },
      target: { type: "string" }, files: { type: "array", items: { type: "string" } }, base: { type: "string" },
      placement: { type: "string", enum: ["window", "split"] },
      percent: { type: "integer", minimum: 20, maximum: 80, description: "Master width percentage for master-width." },
      anchor: { type: "string", description: "caller (default), focused, managed view ID, or root-local tmux pane ID." },
    } },
    async execute(_id, p) {
      let args = ["view", p.action, root]
      if (p.action === "create") {
        if (!p.kind) throw new Error("kind required")
        args.push(p.kind, p.target || "root")
        for (const file of p.files || []) args.push("--file", file)
        if (p.base) args.push("--base", p.base)
      } else if (p.action !== "list") {
        if (!p.view) throw new Error("view required")
        args.push(p.view)
      }
      if (p.action === "create" || p.action === "place") {
        if (p.placement) args.push("--placement", p.placement)
        if (p.anchor) args.push("--anchor", p.anchor)
      }
      if (p.action === "master-width") {
        if (!Number.isInteger(p.percent) || p.percent < 20 || p.percent > 80) throw new Error("percent must be 20..80")
        args.push(String(p.percent))
      }
      const result = await run(args)
      const text = typeof result === "string" ? result : JSON.stringify(result)
      return { content: [{ type: "text", text: (text || "Done").slice(0, 32768) }], details: {} }
    },
  })
}
