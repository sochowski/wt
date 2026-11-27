# wt

Workspaces of git worktrees, and a fuzzy finder to jump into them. The core
knows nothing about agents; kits add those per kind of work, and `wt pane`
gives them one way to drive panes in Rex or tmux.

```
wt new fix-oauth Runlayer mcps-by-runlayer   # or just `wt new` to pick interactively
wt                                           # search; enter cds into the workspace, tab into one repo
wt review https://github.com/o/r/pull/123    # a primed workspace for reviewing a PR stack
```

A workspace is `~/worktrees/.sessions/<name>/repos/<repo>`: one worktree per
repo, on a new branch `<name>` cut from that repo's default branch (no
upstream, so a first push sets its own). Files listed in a repo's `.wt/sync`
(`copy <path>` or `symlink <path>`) are brought over from the main checkout,
for `.env` files and the like.

Search also finds workspaces made by the old wt, whose `repos/` entries are
symlinks, and loose worktrees at `~/worktrees/<repo>/<branch>`.

## Building blocks

| Command | |
|---|---|
| `wt new NAME REPO@REF` | check out a branch, tag, sha or `pull/N/head` detached instead of cutting a branch |
| `wt add REPO[@REF][:DIR]` | add a checkout to the workspace you're in, e.g. `wt add Runlayer@abc123:Runlayer-base` |
| `wt kit [KIT...]` | prime the workspace you're in with kits; with no arguments, list them |
| `wt root`, `wt repos` | print the current workspace; list the repos wt can check out |
| `wt pane` | open, find, type into and read panes in Rex or tmux, whichever you're in (see [Panes](#panes)) |
| `wt panel ex CMD` | show something in your nvim in a pane beside the agent, opening it first (see [Panes](#panes)) |
| `wt skills` | link wt's own skills into `~/.agents/skills` and `~/.claude/skills` (see [Skills](#skills)) |
| `wt CMD` | run `wt-CMD` from a kit's `bin/`, wt's own `bin/`, or `PATH` |

## Kits

A kit primes a workspace for one kind of work, in a form every agent harness
reads: Codex, pi and opencode read `AGENTS.md` and `.agents/skills/`, and
Claude Code gets a `CLAUDE.md` of `@AGENTS.md` and a `.claude/skills` link.

A kit is a directory named after it, found in layers: wt's own `kits/`, then
`~/.config/wt/kits/`, then `.wt/kits/` in each repo of the workspace. Every
layer that has the kit contributes, in that order:

| In the kit | Becomes |
|---|---|
| `AGENTS.md` | appended to the workspace's `AGENTS.md` (`{{ws}}` is the workspace root) |
| `skills/*`, `.agents/skills/*` | linked into `.agents/skills/`; a later layer's skill of the same name wins |
| `bin/*` | linked into `.wt/bin/`; `bin/wt-CMD` is also `wt CMD` |
| `setup` | run from the workspace root, with `WT_WS` and `WT_KIT_DIR` set |

`wt kit` writes `AGENTS.md` itself: the checkouts, an index of the skills with
their descriptions, the tools, then each kit's part. The applied kits are kept
in `.wt/kits`, and every `wt kit` run regenerates the lot, so edit kits rather
than the generated files. Because `.agents/skills/` counts, a tool that
installs skills into a project (`runlayer skills add`, say) can install
straight into a kit directory.

## Reviewing a PR

`wt review URL` (or `OWNER/REPO#N`) makes `review-N`: the PR's repo checked
out detached at the top of the PR's stack, primed with the `review` kit. Its
setup follows base branches down and child PRs up to find the stack, and writes
`review/`:

- `STACK.md`: the layers, each with the exact diff of its own changes, and
  what moved since the last refresh;
- `pr-<n>.md`: each PR's description; `diffs/<n>.diff`, `numstat.tsv`,
  `prs.tsv`, `meta.env`: the layout of `guided-pr-review`'s `collect.sh`;
- `layers.tsv` and `history.tsv`: full SHAs, and every head seen.

Run `wt review` again, here or anywhere with the URL, to pick up new pushes.
`--only` skips the rest of the stack.

The kit's skills are `pr-walkthrough` (motivation, then the code in reading
order as slides, a chapter at a time) and `pr-verify` (local checks, including
before/after against the base). They show their work with
`.wt/bin/review-show`, which drives one nvim per workspace over a socket. In
Rex or tmux the nvim opens as a split beside the agent. It shows markdown slides
(split on `---`, reloaded as the agent writes them), docs, files, and a diff
pane under the slides. A slide's `Diff: #<pr> path:line` line moves the pane
there; `]p`/`[p`, `]f`/`[f`, `]c`/`[c` page it by PR, file and hunk; `gf` on a
diff line opens the file at that side, `gd` opens it in a tab (unified.nvim's
inline diff for the top PR, whose head is the checkout; diffview below it), `gs`
moves the pane beside the slides, and `gu` lists changed files not opened yet
(kept in `review/viewed.tsv`). The agent's notes in `review/annotations.tsv`
show above the lines they're about. Slides use render-markdown, so fenced code is highlighted by
treesitter (given the languages' parsers) and `mermaid` blocks render as box
drawings (given render-markdown-mermaid and `bm`). On a slide, `j`/`k`/`gg`/`G`
move as usual, `J`/`K` page (with a count), `L`/`H` jump to the last and first
slide, `gf` opens the `path:line` under the cursor (in the diff when the stack
changes it), and `g?` lists the rest.

`c` adds a comment, `a` then a letter picks an option, and `S` (or `:send`) submits
new comments to the agent pane. `review-show slides` binds the deck to the
presenting agent's pane and saves `<!-- agent: PANE -->` (a `wt pane` ref) in
the deck, so the recipient survives restarting nvim. Decks saved with the older
`<!-- agent: BLOCK SESSION -->` still work in Rex.
Only one send runs at a time. Failures stay visible; if the text was sent but
Enter failed, `:send` retries Enter without pasting the batch again. Submission
means the multiplexer accepted the input; the agent's reply in the deck confirms it was
handled. Send tracking lasts for the panel's lifetime; after restarting it,
comments without a reply can be submitted again. If the pane accepted a send
but the agent missed it, `:send!` explicitly resubmits comments without a reply.
Replies can use `> **agent:**`, `> **ChatGPT:**`, `> **Codex:**`, or the older
`> **claude:**` label.

If the recipient's session or pane disappears, comments stay in the deck and
the failed send stays visible. Before sending, the panel checks for existing
agents in **the same tab as nvim** (a Rex tab or a tmux window). It keeps the saved recipient if that
agent is still there, reconnects to the sole agent otherwise, or opens a picker
when several agents are running. `:Reconnect` explicitly finds an agent in
that tab and offers a choice when there are several. It detects Claude Code,
Codex, OpenCode and pi from foreground process metadata; shell panes and
agents in other tabs are excluded. If no agent is found, it keeps the comments
and reports that a reconnect is needed. It never starts an agent.

Reconnecting saves the new recipient and makes unreplied feedback available
to `:send` again. A batch pasted into the old pane is resent in full to the
new one. Reconnecting waits for any active send to finish. An agent can also
explicitly claim a deck with `.wt/bin/review-show bind path/to/deck.md`, or
present it again with `review-show slides`.

The panel asks for you by itself. When the agent shows slides, a doc, code or
a diff while you're looking elsewhere, the panel marks itself blocked with what
to look at ("Look at the slides: walkthrough.md"); looking at it clears that.
Looking means the pane has focus, or you typed, clicked or scrolled in it in
the last few seconds (`vim.g.wt_panel_recent`, 5 by default), so nothing is
raised while you're reading along. Unsent comments, sending, a failed send or
a lost recipient take precedence and stay until they're dealt with; a
successful submission clears them. An agent that needs an answer whether or
not you've looked runs
`.wt/bin/review-show status waiting 'Comments needed: deployment options'`,
which holds until the comments are sent, and `review-show status idle`
withdraws any ask, the automatic one included.

In Rex the panel reports this through Rex's program status protocol (OSC
7501), on its own pane, and Rex controls how activity appears and whether it
produces notifications or sounds. In tmux it rings the bell once on becoming
blocked, which flags the window in the status line, and puts the message in
its pane title (`#{pane_title}`, shown with `pane-border-status`). tmux passes
focus to nvim only with `set -g focus-events on`; without it, typing in the
pane is what clears the ask.

Personal or team review process goes in a layer of its own, e.g.
`~/.config/wt/kits/review/` with an `AGENTS.md` and extra skills.

## Subagents in tabs

`wt kit subagents` makes an agent ask, before it hands substantial work to
subagents, whether they should be its harness's built-in ephemeral ones or
full agent sessions in their own tabs (Rex or tmux windows), which you can
switch to and steer. For tabs, its `subagent-tabs` skill opens each one with
`wt pane window`, in
the current workspace or, for parallel edits, a fresh one from `wt new`. The
brief and the result pass through `.wt/agents/<name>/`. A result ends with
the decisions it needs from you, and when your answers change what running
subagents should do, they go back down as a round in `.wt/agents/_common.md`.

## Drilling down

`wt kit drilldown` makes an agent bring decisions to you as asks, not as a
wall of chat. Each ask gets a stable ID in `drilldown/asks.md`, and you read
them in the review panel as slides: an index of what's open, then one slide
per ask with its background, the options and what each would change, and the
agent's pick. Answer in shorthand (`Q3a Q5b`) whenever you like; whatever you
skip comes back in the next deck. Its `drilldown` skill also turns subagents'
results into asks, so the kit pairs with `subagents`. It shares the review
kit's panel, so `.wt/bin/review-show` is the same tool here.

## Keys

The pickers open in NORMAL mode, like the old wt:

| Key | NORMAL | |
|---|---|---|
| `j` / `k`, `g` / `G`, `ctrl-d` | move | |
| `l` or `enter` | open the workspace | |
| `tab` | pick one of its repos | |
| `i` or `/` | INSERT: type to filter by name | `esc` back to NORMAL |
| `h`, `q`, `esc` | quit | |
| `?` or `ctrl-p` | toggle the preview | |
| `space` | toggle a repo (in `wt new`) | |

`WT_FZF_VIM=0` turns modal keys off and gives plain fzf.

## Install

Requires `git`, `fzf`, and bash 4 or newer (on macOS, `brew install bash`).
The review kit also needs `gh`, `jq`, and `nvim`, with Rex or tmux for the panel.

```sh
ln -s "$PWD/bin/wt" ~/bin/wt
echo "source $PWD/shell/wt.sh" >> ~/.zshrc
```

The shell function is what lets `wt` change your directory.

## Panes

`wt pane` is the one place wt talks to a terminal multiplexer. Kits and skills
use it instead of `rex` or `tmux` directly, so the panel, `:send` and subagent
tabs work in either, and another multiplexer is one backend away.

```
wt pane here                                  # this pane's ref
wt pane split --label nvim -- nvim -i NONE    # open a pane beside this one; prints its ref
wt pane window api-tests -- claude "…"        # open a tab
wt pane agents                                # coding agents in this tab, as JSON
wt pane send REF 'text' && wt pane key REF enter
wt pane capture REF
```

A ref names one pane from anywhere, so it can be saved and used later from any
other pane: `rex:SESSION/BLOCK` or `tmux:%PANE@SOCKET`. Inside Rex
(`REX_SESSION` set) the backend is Rex; inside tmux (`TMUX`), tmux. An agent is
a pane whose foreground process is Claude Code, Codex, OpenCode or pi; in tmux
that's read from the foreground process group of the pane's terminal. Kits
that use it link it into `.wt/bin/wt-pane`. `tests/wt-pane.sh` checks both
backends (Rex against a fake `rex`, tmux against a server of its own).

`wt panel` keeps one nvim per workspace in a pane beside the agent, with your
own config, and runs Ex commands in it: `wt panel ex 'edit +120 src/app.py'`.
It opens the pane with `wt pane split` the first time, without taking focus;
`wt panel pane` prints its ref. Like the review panel, it asks for you by
itself: a file shown, a tab opened or a file reloaded while you're looking
elsewhere marks the pane blocked ("Look at src/app.py") in Rex, or rings the
bell and sets its title in tmux, until you look at it (see
[Reviewing a PR](#reviewing-a-pr) for what counts as looking). Both use
`nvim/attention.lua`; `tests/attention.lua` checks it.

## Skills

`skills/` holds skills for any harness, outside kits. `nvim-panel` steers
agents to show you code, docs and diffs in one reused nvim pane beside them
(Rex or tmux), with your own config and unified.nvim for diffs, instead of
HTML pages or fresh panes. `rex-restore` rebuilds your Rex sessions and tabs
after the server dies, from its journal and the Claude Code transcripts. It
resumes each conversation where it ran, then reports which agents were cut off
mid-task and which are waiting on you. Install them for every session with:

```sh
wt skills link            # wt skills lists them and what's linked
```

That links each into `~/.agents/skills` (Codex, pi, opencode) and
`~/.claude/skills` (Claude Code), pointing at this checkout. A copy already in
the way stays put unless you pass `--force`, which moves it to
`~/.local/state/wt/skills-aside/`.

## Profiling agents

`wt trace` profiles agent conversations from the transcripts their harness
already saves, so it needs no hooks. Claude Code is supported today; Codex, pi
and opencode get adapters next.

```sh
wt trace convert                                  # new or changed transcripts → spans
wt trace report --since 168h                      # where the time went, and the habits that waste it
wt trace perfetto resource-apis-fanout-planning   # a flame view of one workspace's agents
wt trace ls                                       # what's been converted
```

A query names a session id prefix, an agent, or a workspace. A workspace takes
in the subagent tabs its coordinator briefed (`.wt/agents/<name>/brief.md`)
and the built-in subagents each one launched.

Spans live in `~/.local/state/wt-trace/spans/`, one OTLP JSON file per
conversation, so an OpenTelemetry backend such as Braintrust can take them as
they are. They keep the first 200 characters of prompts and commands, so the
flame view is readable; `convert --no-text` keeps metadata only. The Perfetto
file opens in [ui.perfetto.dev](https://ui.perfetto.dev), which reads it in the
browser without uploading it.

Building it needs Go 1.26 (`trace/mise.toml` pins it):

```sh
(cd trace && go build -o ../bin/wt-trace .) && ln -s "$PWD/bin/wt-trace" ~/.local/bin/wt-trace
```

## Configuration

| Variable    | Default                   | Meaning                                    |
|-------------|---------------------------|--------------------------------------------|
| `WT_HOME`   | `~/worktrees/.sessions`   | Where workspaces live                      |
| `WT_REPOS`  | `~/workspace`             | Where to find repos (`:`-separated)        |
| `WT_LEGACY` | `~/worktrees`             | Where to look for loose legacy worktrees   |
| `WT_FZF_VIM` | `1`                      | `0` for plain fzf keys                     |
| `WT_KITS`   | `<wt>/kits:~/.config/wt/kits` | Kit layers before each repo's `.wt/kits` (`:`-separated) |

## The old wt

The tmux and agent manager this used to be is preserved on the
`legacy/tmux-wt` branch and the `legacy-tmux-wt` tag.
