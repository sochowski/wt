## Review session

This workspace exists so a human can review someone else's PR, or a stack of them, with you. Start by reading `review/STACK.md` (the layers and the exact diff for each), `review/pr-<n>.md` (each description), and `review/NOTES.md` if it exists: an earlier session, possibly in a different harness, may have left a summary, open questions, and checks already done.

Unless the human asks for something else, the session goes:

1. **Walkthrough** (`pr-walkthrough`): why the change exists, then the code in reading order, as slides in the review panel, one chapter at a time.
2. **Verification, when the human wants to check something themselves** (`pr-verify`): set it up, let them run it, record the result.
3. **Findings and draft comments**, only once the human asks for them.

Rules:

- Never push, commit, approve, request changes, post or resolve comments, or reply on GitHub or anywhere else without explicit approval for that specific action. Drafts go in the chat and in `review/NOTES.md`.
- Write comments in the reviewer's own voice. No AI attribution or "drafted by" signatures, and don't copy sign-offs from other people's comments on the PR.
- Review each PR against the layer below it (the commands in `review/STACK.md`), never against the default branch.
- The checkout is detached at the top of the stack. Don't edit it, except for an experiment you revert straight after. Scripts and output go in `scratch/`.
- Keep `review/NOTES.md` current as you go, with sections `Summary`, `Open questions`, `Verified`, and `Draft findings`. It is how the next session picks up.
- Show reviews as markdown: slides and docs in the panel (`.wt/bin/review-show`), short summaries in chat. Don't produce HTML pages.
- The panel has a diff pane under the slides, paged by PR, file and hunk. Point it at code with a `Diff: #<pr> repos/<repo>/<path>:<line>` line on a slide, or `review-show diff <pr> <path>:<line>`. In any doc you show (checkpoint, findings package), cite code as `repos/<repo>/<path>:<line>`: `gf` there opens the diff at that line, which is where an inline comment on it would go. `review/viewed.tsv` lists the files the human has opened in the pane. For a full-file view, `gd` in the pane uses unified.nvim (`:Unified <base>`, the working tree against one ref), which only fits the top PR of a stack since the checkout sits at its head; lower PRs open in diffview. Don't drive `:Unified` yourself for a lower PR: it would show the whole stack above that PR's base.
- To pick up new pushes, run `.wt/bin/wt-review` from the workspace root. `review/STACK.md` then lists each PR whose head moved, with the command for just the new changes.
