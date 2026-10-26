# wt

Workspaces of git worktrees, and a fuzzy finder to jump into them. The core
knows nothing about tmux, agents, or panes; kits add those per kind of work.

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
| `wt CMD` | run `wt-CMD` from a kit's `bin/`, or from `PATH` |

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
Rex the nvim opens as a split beside the agent. It shows markdown slides
(split on `---`, reloaded as the agent writes them), docs, files, and a layer
in diffview. Slides use render-markdown, so fenced code is highlighted by
treesitter (given the languages' parsers) and `mermaid` blocks render as box
drawings (given render-markdown-mermaid and `bm`). On a slide, `j`/`k`/`gg`/`G`
move as usual, `l`/`h` page (with a count), `L`/`H` jump to the last and first
slide, `gf` opens the `path:line` under the cursor, and `g?` lists the rest.

Personal or team review process goes in a layer of its own, e.g.
`~/.config/wt/kits/review/` with an `AGENTS.md` and extra skills.

## Subagents in tabs

`wt kit subagents` makes an agent ask, before it hands substantial work to
subagents, whether they should be its harness's built-in ephemeral ones or
full agent sessions in their own Rex tabs, which you can switch to and steer.
For tabs, its `subagent-tabs` skill opens each one with `rex window new`, in
the current workspace or, for parallel edits, a fresh one from `wt new`. The
brief and the result pass through `.wt/agents/<name>/`.

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
The review kit also needs `gh`, `jq`, and `nvim`, with `rex` for the panel.

```sh
ln -s "$PWD/bin/wt" ~/bin/wt
echo "source $PWD/shell/wt.sh" >> ~/.zshrc
```

The shell function is what lets `wt` change your directory.

## Skills

`skills/` holds skills for any harness, outside kits. `nvim-panel` steers
agents to show you code, docs and diffs in one reused nvim pane beside them
(Rex or tmux), with your own config and unified.nvim for diffs, instead of
HTML pages or fresh panes. Install it for every session with:

```sh
ln -s "$PWD/skills/nvim-panel" ~/.agents/skills/nvim-panel
ln -s ../../.agents/skills/nvim-panel ~/.claude/skills/nvim-panel
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
