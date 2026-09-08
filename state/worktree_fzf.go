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
	binds = append(binds, "~:ignore", "j:down", "k:up", "g:first", "G:last", "q:abort", "ctrl-d:half-page-down")
	enter := "unbind~" + toggle + "~+unbind(~)+rebind(esc)+change-prompt(INSERT > )"
	binds = append(binds, "i:"+enter, "/:"+enter)
	if multi {
		binds = append(binds, "space:toggle")
	}
	binds = append(binds, "esc:rebind~"+toggle+"~+rebind(~)+unbind(esc)+change-prompt(NORMAL > )")
	start := "unbind(esc)+change-prompt(NORMAL > )"
	if input {
		start = enter
	}
	binds = append(binds, "start:"+start)
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
