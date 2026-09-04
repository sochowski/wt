---
name: wt-presentations
description: Present code, diffs, project structure, or Mermaid-first generated Markdown in a wt-managed Neovim canvas. Use when the user asks for a walkthrough, review, demo, orientation, explanation of non-trivial code/changes, or when visual navigation would make the answer clearer than text alone.
---

# WT presentations

Use a presentation when the user asks to be walked through something, when you
need to explain multiple files or changes, or when a code-grounded visual aid
would reduce back-and-forth. Do not use it for tiny answers, single facts, or
when Neovim is unavailable.

## Prefer the native present tool

If your agent exposes a `present` tool, use it directly. Build one complete deck
up front rather than showing one slide per tool call. Neovim owns navigation
after the deck is shown.

Good triggers:

- “walk me through this”
- “show me how this works”
- “review these changes”
- “explain this architecture/flow”
- “give me a visual map of the repo/package”
- after implementing a multi-file change, when a visual summary would help

Avoid presentations when:

- the user asked for only a terse answer
- the answer is not tied to visible files, diffs, a tree, or Markdown
- you would need to interrupt an active user workflow without clear benefit

## Deck shape

Each scene should be self-contained:

- `title`: short slide title
- `narrative`: what the user should understand while looking at the artifact
- `artifact`: one visual target

Use these artifact kinds:

- `file`: show a source file, with `startLine`/`endLine` and a short `label`
- `diff`: show changes for a file; falls back to source if diff tooling is absent
- `tree`: show a project/package map; use `focus` entries for important paths
- `markdown`: show generated diagrams, summaries, checklists, or prose

## Prefer Mermaid in Markdown

Treat fenced Mermaid diagrams as the default visual language for relationships.
When a Markdown scene explains architecture, workflows, request or data flow,
sequences, state transitions, dependencies, lifecycles, or decision trees, use a
`mermaid` code fence unless there is no meaningful relationship to diagram.
Do not substitute a hand-authored ASCII diagram when Mermaid can express it.

Choose the diagram family that matches the explanation:

- `flowchart` for architecture, control flow, dependencies, and decisions
- `sequenceDiagram` for interactions ordered over time
- `stateDiagram-v2` for states and transitions
- `classDiagram` or `erDiagram` for structural and data relationships

Design Mermaid for an 80-column terminal canvas:

- default flowcharts to `TD`; use `LR` only for three or fewer short nodes across
- keep each horizontal rank to three nodes or fewer
- limit sequence diagrams to four participants
- keep labels under 24 characters per line and use `<br/>` for intentional wraps
- split a larger graph across scenes instead of relying on horizontal scrolling

Keep one primary diagram focused on one idea per scene. Add a short heading,
legend, or conclusion in the surrounding Markdown when it helps, and split a
crowded diagram into multiple scenes. Mermaid stays embedded in the Markdown;
the user's Neovim Mermaid plugin owns its visual rendering. If that plugin is
unavailable, the fenced source remains a readable fallback.

Keep labels concise. Prefer multiple slides over overcrowding one slide.

## Navigation to mention

After presenting, tell the user:

- `L` / Right: next slide
- `H` / Left: previous slide
- `?`: help
- `q`: close and restore the editor

## Fallback transport for agents without a present tool

When running in a wt session with Neovim available, agents can send a deck with
`wt-present` directly:

```bash
printf '%s' '{
  "version": 1,
  "title": "Walkthrough",
  "scenes": [
    {
      "title": "Entry point",
      "narrative": "This is where the flow begins.",
      "artifact": { "kind": "file", "path": "src/main.js", "startLine": 1, "label": "entry point" }
    }
  ]
}' | wt-present deck-show
```

Use `wt-present clear` to end the active presentation. If `wt-present` reports
that no wt session or Neovim socket is available, fall back to a normal textual
answer.
