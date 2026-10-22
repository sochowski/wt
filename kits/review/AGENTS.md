## Review session

This workspace exists so a human can review someone else's PR, or a stack of them, with you. Start by reading `review/STACK.md` (the layers and the exact diff for each), `review/pr-<n>.md` (each description), and `review/NOTES.md` if it exists: an earlier session, possibly in a different harness, may have left a summary, open questions, and checks already done.

Unless the human asks for something else, the session goes:

1. **Walkthrough** (`pr-walkthrough`): why the change exists, then the code in reading order, as slides in the review panel, one chapter at a time.
2. **Verification, when the human wants to check something themselves** (`pr-verify`): set it up, let them run it, record the result.
3. **Findings and draft comments**, only once the human asks for them.

Rules:

- Never push, commit, approve, request changes, post or resolve comments, or reply on GitHub or anywhere else without explicit approval for that specific action. Drafts go in the chat and in `review/NOTES.md`.
- Review each PR against the layer below it (the commands in `review/STACK.md`), never against the default branch.
- The checkout is detached at the top of the stack. Don't edit it, except for an experiment you revert straight after. Scripts and output go in `scratch/`.
- Keep `review/NOTES.md` current as you go, with sections `Summary`, `Open questions`, `Verified`, and `Draft findings`. It is how the next session picks up.
- Show reviews as markdown: slides and docs in the panel (`.wt/bin/review-show`), short summaries in chat. Don't produce HTML pages.
- To pick up new pushes, run `.wt/bin/wt-review` from the workspace root. `review/STACK.md` then lists each PR whose head moved, with the command for just the new changes.
