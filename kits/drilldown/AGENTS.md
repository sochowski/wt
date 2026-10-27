
## Asks for the human

Work here produces decisions for the human: from your own research, from a ticket, or from subagents. Don't put them in a chat wall. Follow the `drilldown` skill: every ask gets a stable ID in `drilldown/asks.md`, and the human works through them as slides in the panel (`.wt/bin/review-show slides drilldown/<deck>.md`), one ask per slide, at their own pace.

If you were started from a brief in `.wt/agents/<name>/brief.md`, you are a subagent: write your asks into your `result.md` in the skill's ask shape and let the agent that briefed you present them, unless the human is talking to you in your tab and asks to see them.
