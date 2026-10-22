---
name: pr-walkthrough
description: Walk a human through a PR or PR stack they are reviewing. Motivation first, then the code in reading order, as markdown slides in the review panel, one chapter at a time with a pause for questions. Use at the start of a review session, or for "walk me through this", "explain this PR", "what's the motivation", "tour of the stack".
---

# PR walkthrough

The human wants to understand the change before judging it. Explain; don't review yet. Hold findings until they ask, but write down anything that looks wrong under `Open questions` in `review/NOTES.md` as you go.

## Inputs

`review/STACK.md` (layers, ranges), `review/pr-<n>.md` (descriptions), `review/diffs/<n>.diff`, `review/numstat.tsv`. Run git from inside the checkout named in `STACK.md`.

## 1. Motivation

Before reading code in depth, work out why the PR exists:

- The description, and every ticket, issue, doc, or thread it links. Follow Linear, Slack, or Notion links with whatever tools this session has; if a source isn't reachable, say which.
- Commit messages: `git log --reverse <lo>..<hi>`.
- Earlier work on the same code: `git log --oneline -15 -- <main paths>` on the base, and `gh pr list --state all --search "<key term>"`.

Write slide 1, **Why**: the problem in the user's or system's terms, who it affects, the approach taken, and the alternatives it rules out. Mark everything you inferred rather than read as inferred. Add a slide 2, **The stack**, when there is more than one layer: one line per PR, saying what each layer adds.

## 2. Chapters

Chapters are ideas, not files or PRs. Read every source diff first; skim tests only to learn what behaviour they pin; skip generated files.

- Order: the core behaviour change first, the thing the rest exists to serve; then permission and security changes; then the main flows; then entry points (REST, MCP tools, CLI); then UI; then foundation (schema, migrations, types, queries) and tests last.
- 2 to 5 chapters for one PR, 4 to 8 for a stack. Every PR lands in at least one chapter.
- Each chapter is one slide:
  - `# <title>`, then one line saying why it matters.
  - 2 or 3 short paragraphs on the mechanism. Give numbers (limits, flags, sizes, timeouts) rather than adjectives.
  - At most one excerpt: the 5 to 15 lines the human must read, copied verbatim, preceded by its location as `repos/<repo>/<path>:<line>` (`gf` on that location in the panel opens the file there). Tag the fence with its language (`python`, `tsx`, `sql`, ...) so it is highlighted. When what matters is what changed, show it as a `diff` block with the `+`/`-` lines instead.
  - A diagram when the shape matters more than the lines: a flow across services, a sequence of calls, a state machine, a schema change. Use a fenced `mermaid` block; the panel renders it as a box drawing. Keep it small enough for a split pane: `flowchart LR` or `TD` with about 8 nodes at most and short labels, or a `sequenceDiagram` with 4 participants at most. One diagram per slide, and leave out the excerpt if the slide has a diagram and runs long.
  - Up to 3 **Check:** questions the human should answer for themselves ("Does any caller pass `None` here?"). Never phrase one as "Confirm that ...".
- End with a **Hotspots** slide: up to 6 decisions or risks across the change, each with a kind (Security, Data, Rollout, Scope, Perf, ...), a short title, and a sentence or two. Label what you inferred from the diff, since nothing has run.
- Then a **Worth verifying** slide: 2 to 5 things the human could check locally, each with what it would show. This is where `pr-verify` takes over.

## 3. Presenting

The deck is `review/walkthrough.md`, slides separated by a line containing only `---`. Write it incrementally:

1. Write the Why slide (and the Stack slide), run `.wt/bin/review-show slides review/walkthrough.md 1`, and summarise it in chat in two or three sentences.
2. Stop and let the human ask questions or say go on. Then append the next chapter and run `review-show slides review/walkthrough.md last`.
3. If they say to do the rest, write all the remaining slides at once and show slide 1 of them.

`review-show code repos/<repo>/<path>:<line>` opens a file beside the slides; `review-show diff <pr>` opens one layer in diffview; `review-show close` quits the panel. The human pages with `h`/`l` and opens locations with `gf`; mention that once, the first time you show the deck. If `review-show` says there is no panel, give the human the deck's path instead and keep going in chat.

Answer questions in chat, and add a slide only when an answer changes the picture. When the walkthrough is done, write the summary (motivation, chapters in one line each, hotspots) to `## Summary` in `review/NOTES.md`.

## Style

Write like you're briefing a senior engineer in a DM: plain words, concrete nouns, no filler, no "this slide shows". Use exact names from the code. Short slides read better in a split pane, so if one runs past about 40 lines, split it.
