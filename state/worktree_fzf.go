package main

import (
	"fmt"
	"os"
	"strings"
)

// One modal contract for Bash and Go. This is list navigation/filter input,
// not a Vim editor. Unbinding printable keys restores fzf's ordinary insertion.
func fzfVimArgs(input, multi bool) []string {
	if os.Getenv("WT_FZF_VIM") != "1" && os.Getenv("WT_FZF_VIM") != "true" {
		return nil
	}
	keys := []string{}
	for c := 'a'; c <= 'z'; c++ {
		keys = append(keys, string(c))
	}
	for c := 'A'; c <= 'Z'; c++ {
		keys = append(keys, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		keys = append(keys, string(c))
	}
	keys = append(keys, "space", "/", "-", "_", ".", ":", ";", "'", "\"", "?", "!", "@", "#", "$", "%", "^", "&", "*", "(", ")", "[", "]", "{", "}", "<", ">", "=", "`", "\\", ",", "+", "|")
	toggle := strings.Join(keys, ",")
	binds := []string{}
	for _, k := range keys {
		binds = append(binds, k+":ignore")
	}
	binds = append(binds, "~:ignore", "h:abort", "j:down", "k:up", "l:accept", "g:first", "G:last", "q:abort", "ctrl-d:half-page-down")
	normal := "rebind~" + toggle + "~+rebind(~)+change-prompt(NORMAL > )"
	enter := "unbind~" + toggle + "~+unbind(~)+change-prompt(INSERT > )"
	binds = append(binds, "i:"+enter, "/:"+enter)
	if multi {
		binds = append(binds, "space:toggle")
	}
	// fzf does not restore its default Esc action after unbind(esc). Dispatch
	// explicitly by mode: INSERT triggers the internal NORMAL transition, while
	// NORMAL aborts. The indirection keeps the transform free of key-list quoting.
	binds = append(binds, "f12:"+normal)
	start := "change-prompt(NORMAL > )"
	if input {
		start = enter
	}
	binds = append(binds, "start:"+start)
	// The colon-delimited transform command must remain last: unlike ordinary
	// actions, it consumes the rest of the binding argument as shell source.
	binds = append(binds, `esc:transform:test "$FZF_PROMPT" = "INSERT > " && printf '%s\n' 'trigger(f12)' || printf '%s\n' abort`)
	return []string{"--bind=" + strings.Join(binds, ",")}
}
func fzfBindingsCommand(args []string) error {
	input, multi := false, false
	for _, arg := range args {
		switch arg {
		case "input":
			input = true
		case "multi":
			multi = true
		default:
			return fmt.Errorf("unknown fzf binding mode %s", arg)
		}
	}
	for _, arg := range fzfVimArgs(input, multi) {
		fmt.Println(arg)
	}
	return nil
}
