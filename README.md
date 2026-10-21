# wt

Workspaces of git worktrees, and a fuzzy finder to jump into them. Nothing
else: no tmux, no agents, no panes.

```
wt new fix-oauth Runlayer mcps-by-runlayer   # or just `wt new` to pick interactively
wt                                           # search; enter cds into the workspace, tab into one repo
```

A workspace is `~/worktrees/.sessions/<name>/repos/<repo>`: one worktree per
repo, on a new branch `<name>` cut from that repo's default branch (no
upstream, so a first push sets its own). Files listed in a repo's `.wt/sync`
(`copy <path>` or `symlink <path>`) are brought over from the main checkout,
for `.env` files and the like.

Search also finds workspaces made by the old wt, whose `repos/` entries are
symlinks, and loose worktrees at `~/worktrees/<repo>/<branch>`.

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

```sh
ln -s "$PWD/bin/wt" ~/bin/wt
echo "source $PWD/shell/wt.sh" >> ~/.zshrc
```

The shell function is what lets `wt` change your directory.

## Configuration

| Variable    | Default                   | Meaning                                    |
|-------------|---------------------------|--------------------------------------------|
| `WT_HOME`   | `~/worktrees/.sessions`   | Where workspaces live                      |
| `WT_REPOS`  | `~/workspace`             | Where to find repos (`:`-separated)        |
| `WT_LEGACY` | `~/worktrees`             | Where to look for loose legacy worktrees   |
| `WT_FZF_VIM` | `1`                      | `0` for plain fzf keys                     |

## The old wt

The tmux and agent manager this used to be is preserved on the
`legacy/tmux-wt` branch and the `legacy-tmux-wt` tag.
