---
name: drilldown
description: Put decisions in front of the human as asks they can drill into. Each ask has a stable ID in a ledger, and they are shown as slides in the review panel, one self-contained ask per slide, answered in shorthand at the human's own pace. Use whenever you have decisions, open questions or subagent results needing the human, or they say "what needs my attention", "walk me through the decisions", "present", "slides", "rapid fire".
---

# Drilldown

Fanning out sends work down to agents; drilling down brings decisions up to the human. The unit that travels up is the **ask**. Asks live in a ledger, the human reads them as slides, and their answers go back down as instructions.

The human drills in three levels, and each needs its own form:

1. **Index:** every open ask with its urgency, on one screen.
2. **Ask:** one slide with enough context to decide without having followed the work.
3. **Evidence:** `gf` on a `path:line` opens the code beside the slides; run histories and docs are linked from the slide.

## The ledger

`drilldown/asks.md` in the workspace root is the one list of asks for this effort. You own it; subagents never write it. Keep it readable, since the human may open it:

```markdown
# Asks: v2 foundation

| ID | Ask | From | Urgency | Status | Answer |
|---|---|---|---|---|---|
| Q1 | Where to enforce the 10-key cap | auth | 🔴 | answered | drop the cap (round 2) |
| Q7 | `grant`'s parent: agent or agent account | registry | 🟡 | answered | separate `/agent-accounts` resource |
| Q15 | A cancelled write: keep the lock or release it? | base | 🔴 | open | |
```

- **IDs are global and never reused.** Q1 stays Q1 in every deck, chat message and round file. Don't restart at Q1 per deck, and don't renumber when an ask is dropped; mark it `retired` with the reason.
- **Status** is `open`, `answered`, `retired`, or `rewritten` (replaced by a newer ask; name it).
- **Answer** is the human's call in a few words, and where it went (a round file, a decision record, a ticket).
- Urgency: 🔴 blocks an agent, or a security posture · 🟠 a public contract, a team convention, or reshapes a ticket · 🟡 layout or naming · 🟢 confirm only.

## Decks

Decks are markdown in `drilldown/`, slides separated by a line containing only `---`, shown with `.wt/bin/review-show slides drilldown/<deck>.md 1`. Name them by round (`round-3.md`) or by topic (`ENG-7440.md`). Write a new deck for each round and leave old ones in place. The panel reloads as you write, so fix a slide in place and reshow it.

Mention the keys the first time you show a deck: `J`/`K` next and previous, `H`/`L` first and last, `gf` opens the `path:line` under the cursor, `g?` the rest. Then say in one chat line what the deck holds and how to answer. If `review-show` says there is no panel, give the deck's path and carry on in chat.

### Slide 1: the index

```markdown
# Round 4: what I changed, plus 2 questions from base

| ID | Decision | From | Urgency |
|---|---|---|---|
| Q15 | A cancelled write: keep the lock or release it? | base | 🔴 blocks the agent |
| Q16 | `ownable` vs `owner` in the registry | base | 🟡 |
| Q9 | Synthetic org actions (still open from round 1) | needs | 🟡 |

Answer like `Q15a Q16a`. Skip any you want to leave for later.
```

Order by urgency. Carry every still-open ask from the ledger into the index, marked as such.

Before the asks, when they apply:
- **What I just changed**, if you acted on the human's last answers: before and after, in a few bullets.
- **Where things stand**: one status slide (a stack, a pipeline, each agent's state), ideally a small `mermaid` graph. It carries no ID; status isn't an ask.
- **Primer** slides (`Primer 1/3`), when the human is new to the domain: how it works today, the terms the asks will use. Say what was read from code and at which commit, and mark anything unverified.

### One slide per ask

```markdown
# Q15 · A cancelled write: keep the lock or release it?

**Background:** what exists today, what was built and why, in plain terms. Define every
term someone who hasn't followed the work wouldn't know. Cite code as
`repos/Runlayer/backend/app/api/v2/contract/idempotency.py:120` so `gf` opens it.

**The question:** exactly what's being asked, in one sentence.

**Options**
- **a. Keep the claim on cancel (as built).** What happens if you pick it.
  - The cost: who notices, what breaks, what else changes.
- **b. Release the claim on cancel.** What happens.
  - The cost.

**My pick: a.** Why, in two to four bullets. Name the follow-up ticket if one is needed.
```

- Options are letters under the ID, so answers read `Q15a`. When the current state is a real option, it's one of them ("as built").
- Every option states its **effect**: behaviour, who sees it, scope, which agent or ticket it moves. "Cleaner" isn't an effect.
- Always give your pick and why.
- If the ask came out of a loop that reversed itself (a review asking to undo an earlier fix), add a table of run → what it asked for, so the flip-flop is visible.
- Cut background that doesn't change the choice. If a slide runs past a screen, it is two asks, or it carries background the human doesn't need.
- Explain why the work exists, not only what was built. The human weighs options by their purpose.

### Proposal decks

When one change needs a decision (a ticket, a new concept the human asked for), keep it to about five slides: the problem → today → the change → cost and plan → **Your call**, a numbered list of the open points, each with your lean. Each open point becomes an ask in the ledger. Not one slide per sub-point.

## Taking answers

The human answers in shorthand, mixed with pushback and new ideas: `1. drop the cap 2. per operation 9. i dont follow`.

1. **Read it back** in one line per ID before acting: `Q1 → drop the cap`. If an answer's number doesn't fit its content, say which ask you think they meant and ask.
2. **Pushback isn't an option pick.** "This concept is garbage, find a better abstraction" means research it and come back with a proposal deck, under a new ID; mark the old ask `rewritten`.
3. **"I don't follow"** means the slide failed. Rewrite it plainer, reshow it, and keep the ID.
4. **Questions back** ("is that right?", "thoughts?") get an answer in chat first, then the ask stays open until they decide.
5. **Unanswered asks stay open.** Don't chase them in chat; they come back in the next index.
6. **Rapid fire**, when asked for: one ask per chat message, each still with a few lines of background. If the human asks for more context, go back to slides.
7. Record each answer in the ledger, then wherever it's acted on: the round file subagents read (see `subagent-tabs`), a decision record, or the ticket once a conclusion is settled. Ask before editing tickets.

### Answers on the slides

The human can also answer in the deck itself. On a slide, `c` adds a comment and `a` then a letter adds a pick, as `> **<name>:** …` lines at the bottom of the slide in the deck file (their `$USER`, or `vim.g.wt_review_name`). `]n` / `[n` jump between slides still in discussion. `:send` types every comment not yet sent into your terminal as one chat message, so they can comment and send as often as they like.

- **Make each deck sendable.** Its first line is `<!-- agent: $REX_BLOCK $REX_SESSION -->`, with the values expanded from your shell, so `:send` reaches you. The agent that will act on the answers owns the deck; a subagent presenting in its own tab uses its own block. The slides don't show this line.
- **Reply under every comment** with `> **claude:** …`. A reply marks the comments above it as handled. When an ask is settled, the reply starts with `✅ Q15 → b`; `]n` skips slides with one.
- **Never edit or delete the human's lines,** and reread the deck before every edit: they may have commented since you last read it.
- **On a `:send` message,** reread the deck and take each comment by the rules above: read it back in chat, record picks in the ledger, rewrite slides they didn't follow in place (say so in the reply), and turn pushback into a new ask.

## Asks from subagents

A subagent's `result.md` ends with a **Needs human decision** section, each item already in ask shape (background, the question, options with effects, its pick). Lift them into the ledger with new IDs, then onto slides.

When the human asks what finished agents did, give one slide per finished agent before the asks: **the work and why it exists**, **decisions it made on its own, and why**, then **its open questions**, listed by ledger ID. Agents still running go on the one status slide.
