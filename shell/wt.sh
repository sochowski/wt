# Source from ~/.zshrc or ~/.bashrc. `wt search` and `wt new` print a
# directory; this cds into it. Bare `wt` is `wt search`.
wt() {
  case "${1:-search}" in
    search|new)
      local dir
      dir=$(command wt "$@") && [ -d "$dir" ] && cd "$dir"
      ;;
    *) command wt "$@" ;;
  esac
}
