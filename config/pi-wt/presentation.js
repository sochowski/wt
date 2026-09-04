import { spawn } from "node:child_process"

const CONTROL_CONTINUE = "Continue"
const CONTROL_ASK = "Ask about this"
const CONTROL_END = "End presentation"

const presentParameters = {
  type: "object",
  additionalProperties: false,
  required: ["title", "narrative", "artifact"],
  properties: {
    title: {
      type: "string",
      description: "Short title for this presentation scene",
    },
    narrative: {
      type: "string",
      description: "The explanation or context the user should read while viewing the artifact",
    },
    artifact: {
      type: "object",
      additionalProperties: false,
      required: ["kind"],
      properties: {
        kind: {
          type: "string",
          enum: ["file", "diff", "tree", "markdown"],
          description: "What Neovim should present",
        },
        path: {
          type: "string",
          description: "Worktree-relative path for file, diff, or markdown artifacts",
        },
        display: {
          type: "string",
          enum: ["source", "diff"],
          description: "Optional file display style; diff artifacts always use diff",
        },
        startLine: {
          type: "integer",
          minimum: 1,
          description: "First one-based line to center and highlight",
        },
        endLine: {
          type: "integer",
          minimum: 1,
          description: "Last one-based line to highlight",
        },
        label: {
          type: "string",
          description: "Short annotation displayed beside the highlighted range",
        },
        root: {
          type: "string",
          description: "Worktree-relative directory for a tree artifact",
        },
        view: {
          type: "string",
          enum: ["summary", "explorer"],
          description: "Tree rendering style. summary uses a scratch overview; explorer uses nvim-tree.nvim when available.",
        },
        focus: {
          type: "array",
          description: "Important paths to show in a tree artifact",
          items: {
            type: "object",
            additionalProperties: false,
            required: ["path"],
            properties: {
              path: { type: "string" },
              label: { type: "string" },
            },
          },
        },
        content: {
          type: "string",
          description: "Markdown content for a markdown artifact",
        },
      },
    },
    interaction: {
      type: "object",
      additionalProperties: false,
      properties: {
        kind: {
          type: "string",
          enum: ["continue", "choice", "text", "selection", "none"],
          description: "How the user should respond to this scene",
        },
        prompt: {
          type: "string",
          description: "Question shown when choice, text, or selection input is requested",
        },
        options: {
          type: "array",
          items: { type: "string" },
          description: "Available answers for a choice interaction",
        },
      },
    },
  },
}

const deckParameters = {
  type: "object",
  additionalProperties: false,
  required: ["title", "scenes"],
  properties: {
    title: {
      type: "string",
      description: "Short title for the presentation deck",
    },
    startIndex: {
      type: "integer",
      minimum: 1,
      description: "One-based slide index to show first",
    },
    scenes: {
      type: "array",
      minItems: 1,
      description: "Scenes Neovim can navigate locally without another model round trip",
      items: {
        type: "object",
        additionalProperties: false,
        required: ["title", "narrative", "artifact"],
        properties: {
          title: presentParameters.properties.title,
          narrative: presentParameters.properties.narrative,
          artifact: presentParameters.properties.artifact,
        },
      },
    },
  },
}

function runPresentationCommand(command, session, cwd, action, payload, signal) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, [action, "--session", session], {
      cwd,
      env: process.env,
      stdio: ["pipe", "pipe", "pipe"],
    })

    let stdout = ""
    let stderr = ""
    let settled = false
    const configuredTimeout = Number.parseInt(process.env.WT_PRESENT_TIMEOUT_MS || "15000", 10)
    const timeoutMs = Number.isFinite(configuredTimeout) && configuredTimeout > 0 ? configuredTimeout : 15000
    const timer = setTimeout(() => {
      child.kill("SIGTERM")
      finish(reject, new Error(`Presentation timed out after ${timeoutMs}ms`))
    }, timeoutMs)
    const finish = (fn, value) => {
      if (settled) return
      settled = true
      clearTimeout(timer)
      signal?.removeEventListener("abort", abort)
      fn(value)
    }
    const abort = () => {
      child.kill("SIGTERM")
      finish(reject, new Error("Presentation cancelled"))
    }

    if (signal?.aborted) abort()
    else signal?.addEventListener("abort", abort, { once: true })
    child.stdout.on("data", (data) => { stdout += data })
    child.stderr.on("data", (data) => { stderr += data })
    // The exit status and stderr are more useful if the child closes stdin
    // early; never let an incidental EPIPE crash the extension process.
    child.stdin.on("error", () => {})
    child.once("error", (error) => finish(reject, error))
    child.once("close", (code) => {
      if (code !== 0) {
        finish(reject, new Error(stderr.trim() || `wt-present exited ${code}`))
        return
      }
      try {
        finish(resolve, JSON.parse(stdout))
      } catch {
        finish(reject, new Error(`Invalid wt-present response: ${stdout.trim()}`))
      }
    })

    if (payload === undefined) child.stdin.end()
    else child.stdin.end(JSON.stringify(payload))
  })
}

function wrapPlain(text, width) {
  const limit = Math.max(10, width)
  const rendered = []
  for (const sourceLine of String(text || "").split("\n")) {
    if (sourceLine.length === 0) {
      rendered.push("")
      continue
    }
    let remaining = sourceLine
    while (remaining.length > limit) {
      let cut = remaining.lastIndexOf(" ", limit)
      if (cut < Math.floor(limit / 2)) cut = limit
      rendered.push(remaining.slice(0, cut))
      remaining = remaining.slice(cut).replace(/^\s+/, "")
    }
    rendered.push(remaining)
  }
  return rendered
}

function plainComponent(text) {
  return {
    render(width) { return wrapPlain(text, width) },
    invalidate() {},
  }
}

function artifactSummary(artifact = {}) {
  if (artifact.kind === "markdown") return "markdown"
  if (artifact.kind === "tree") return `tree ${artifact.root || "."}`
  const range = artifact.startLine
    ? `:${artifact.startLine}${artifact.endLine && artifact.endLine !== artifact.startLine ? `-${artifact.endLine}` : ""}`
    : ""
  return `${artifact.kind || "artifact"} ${artifact.path || "?"}${range}`
}

async function safelyClear(run, ctx) {
  try {
    await run(ctx, "clear")
  } catch {
    // Neovim may already be gone during shutdown or explicit cancellation.
  }
}

async function collectInteraction(scene, ctx, run, signal) {
  const interaction = scene.interaction || { kind: "continue" }
  const kind = interaction.kind || "continue"
  if (kind === "none") return { action: "shown" }
  if (!ctx.hasUI) return { action: "shown", unavailable: "Interactive UI is not available" }

  const title = `Presentation — ${scene.title}`
  if (kind === "continue") {
    const selected = await ctx.ui.select(title, [CONTROL_CONTINUE, CONTROL_ASK, CONTROL_END], { signal })
    if (selected === CONTROL_CONTINUE) return { action: "continue" }
    if (selected === CONTROL_ASK) {
      const question = await ctx.ui.input("Ask about this presentation", "Type a question", { signal })
      return question ? { action: "ask", answer: question } : { action: "continue" }
    }
    return { action: "end" }
  }

  if (kind === "choice") {
    const options = Array.isArray(interaction.options) ? interaction.options : []
    const labels = options.map((option, index) => `${index + 1}. ${option}`)
    const selected = await ctx.ui.select(interaction.prompt || title, [...labels, CONTROL_ASK, CONTROL_END], { signal })
    const index = labels.indexOf(selected)
    if (index >= 0) return { action: "answer", answer: options[index] }
    if (selected === CONTROL_ASK) {
      const question = await ctx.ui.input("Ask for more context", "Type a question", { signal })
      return question ? { action: "ask", answer: question } : { action: "continue" }
    }
    return { action: "end" }
  }

  if (kind === "text") {
    const answer = await ctx.ui.input(interaction.prompt || title, "Type your answer", { signal })
    return answer ? { action: "answer", answer } : { action: "end" }
  }

  if (kind === "selection") {
    const selected = await ctx.ui.select(
      interaction.prompt || title,
      ["Use current Neovim selection", "Answer with text", CONTROL_END],
      { signal },
    )
    if (selected === "Use current Neovim selection") {
      try {
        return { action: "selection", editorContext: await run(ctx, "context", undefined, signal) }
      } catch (error) {
        return { action: "selection", error: error.message }
      }
    }
    if (selected === "Answer with text") {
      const answer = await ctx.ui.input("Answer about this code", "Type your answer", { signal })
      return answer ? { action: "answer", answer } : { action: "continue" }
    }
    return { action: "end" }
  }

  return { action: "continue" }
}

function resultText(result) {
  switch (result.action) {
    case "continue": return "User continued the presentation. Present the next scene or conclude."
    case "ask": return `User asked about the current scene: ${result.answer}`
    case "answer": return `User answered: ${result.answer}`
    case "selection": {
      const context = result.editorContext
      if (!context?.selection) return "User requested the Neovim selection, but no visual selection was available."
      return `User selected ${context.path}:${context.selection.startLine}-${context.selection.endLine}\n\n${context.selection.text}`
    }
    case "end": return "User ended the presentation. Do not present another scene unless asked."
    default: return "Presentation scene shown."
  }
}

export default function registerPresentation(pi, { session }) {
  const command = process.env.WT_PRESENT || `${process.env.HOME}/bin/wt-present`
  const run = (ctx, action, payload, signal) =>
    runPresentationCommand(command, session, ctx.cwd, action, payload, signal)

  pi.registerTool({
    name: "present",
    label: "Present",
    description: "Present one user-paced scene through wt's Neovim canvas. It can show source, an inline diff, a project tree, or markdown, and can ask for a choice, text, or a Neovim selection.",
    promptSnippet: "Present explanations and code-grounded questions through the wt Neovim canvas",
    promptGuidelines: [
      "Use present when the user asks for a walkthrough or when a question would benefit from a visible artifact.",
      "Put the complete explanation for the current scene in present's narrative field.",
      "Call present exactly once per assistant response and wait for the user's returned action before advancing.",
      "Use present artifact kind diff for branch changes, file for existing source, tree for project structure, and markdown for non-code material.",
    ],
    parameters: presentParameters,
    executionMode: "sequential",

    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const scene = {
        version: 1,
        title: params.title,
        narrative: params.narrative,
        artifact: params.artifact,
        interaction: params.interaction || { kind: "continue" },
      }

      let editor
      let editorError
      try {
        editor = await run(ctx, "show", scene, signal)
      } catch (error) {
        editorError = error.message
        if (ctx.hasUI) ctx.ui.notify(`Editor presentation unavailable: ${editorError}`, "warning")
      }

      const interaction = await collectInteraction(scene, ctx, run, signal)
      if (interaction.action === "end") await safelyClear(run, ctx)

      return {
        content: [{ type: "text", text: resultText(interaction) }],
        details: { scene, interaction, editor, editorError },
      }
    },

    renderCall(args) {
      const title = args?.title || "Presentation"
      const narrative = args?.narrative || ""
      return plainComponent(`${title}\n${artifactSummary(args?.artifact)}\n\n${narrative}`)
    },

    renderResult(result) {
      const interaction = result?.details?.interaction || {}
      const warning = result?.details?.editorError ? `\nEditor unavailable: ${result.details.editorError}` : ""
      return plainComponent(`${resultText(interaction)}${warning}`)
    },
  })

  pi.registerTool({
    name: "present_deck",
    label: "Present Deck",
    description: "Present a complete navigable deck through wt's Neovim canvas. Neovim owns slide navigation locally with H/L/q so the user can move without model round trips.",
    promptSnippet: "Use present_deck for slide-like walkthroughs that should be navigable in Neovim",
    promptGuidelines: [
      "Use present_deck when the user wants a slide-like walkthrough or back/forward navigation.",
      "Keep each scene self-contained; Neovim, not the agent, owns deck navigation after the deck is shown.",
      "Use H/L for previous/next and q to end the deck in Neovim.",
    ],
    parameters: deckParameters,
    executionMode: "sequential",

    async execute(_toolCallId, params, signal, _onUpdate, ctx) {
      const deck = {
        version: 1,
        title: params.title,
        startIndex: params.startIndex || 1,
        scenes: params.scenes,
      }

      let editor
      let editorError
      try {
        editor = await run(ctx, "deck-show", deck, signal)
      } catch (error) {
        editorError = error.message
        if (ctx.hasUI) ctx.ui.notify(`Editor deck unavailable: ${editorError}`, "warning")
      }

      return {
        content: [{ type: "text", text: editorError ? `Deck unavailable: ${editorError}` : "Deck shown in Neovim. Use H/L to navigate and q to close." }],
        details: { deck, editor, editorError },
      }
    },

    renderCall(args) {
      const count = Array.isArray(args?.scenes) ? args.scenes.length : 0
      return plainComponent(`${args?.title || "Presentation deck"}\n${count} slides\n\nNeovim-local navigation: H/L/q`)
    },

    renderResult(result) {
      const warning = result?.details?.editorError ? `\nEditor unavailable: ${result.details.editorError}` : ""
      return plainComponent(`Deck shown in Neovim. Use H/L to navigate and q to close.${warning}`)
    },
  })

  pi.registerCommand("presentation-end", {
    description: "End the active wt presentation and restore Neovim",
    handler: async (_args, ctx) => {
      await safelyClear(run, ctx)
      if (ctx.hasUI) ctx.ui.notify("Presentation ended", "info")
    },
  })

  pi.on("session_shutdown", async (_event, ctx) => {
    await safelyClear(run, ctx)
  })
}
