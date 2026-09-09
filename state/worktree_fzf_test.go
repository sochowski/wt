package main

import (
	"strings"
	"testing"
)

func TestFzfVimArgsDisabled(t *testing.T) {
	for _, value := range []string{"", "0", "false"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("WT_FZF_VIM", value)
			if got := fzfVimArgs(false, false); got != nil {
				t.Fatalf("disabled Vim mode returned bindings: %v", got)
			}
		})
	}
}

func TestFzfVimNormalAndInsertBindings(t *testing.T) {
	t.Setenv("WT_FZF_VIM", "1")
	args := fzfVimArgs(false, false)
	if len(args) != 1 || !strings.HasPrefix(args[0], "--bind=") {
		t.Fatalf("unexpected generated arguments: %v", args)
	}
	binds := args[0]
	for _, want := range []string{
		"h:abort", "j:down", "k:up", "l:accept", "q:abort",
		"i:unbind~", "/:unbind~", "change-prompt(INSERT > )",
		"f12:rebind~", `esc:transform:test "$FZF_PROMPT" = "INSERT > "`,
		`printf '%s\n' 'trigger(f12)'`, `|| printf '%s\n' abort`,
		"change-prompt(NORMAL > )",
		"start:change-prompt(NORMAL > )",
	} {
		if !strings.Contains(binds, want) {
			t.Errorf("generated bindings missing %q: %s", want, binds)
		}
	}
	if strings.Contains(binds, "space:toggle") {
		t.Fatalf("single-select bindings unexpectedly toggle marks: %s", binds)
	}
	if strings.Contains(binds, "unbind(esc)") {
		t.Fatalf("NORMAL Esc must remain explicitly dispatchable: %s", binds)
	}
	if !strings.HasSuffix(binds, `|| printf '%s\n' abort`) {
		t.Fatalf("colon-delimited Esc transform must remain the final binding: %s", binds)
	}
}

func TestFzfVimInputAndMultiBindings(t *testing.T) {
	t.Setenv("WT_FZF_VIM", "true")
	input := strings.Join(fzfVimArgs(true, false), " ")
	if !strings.Contains(input, "start:unbind~") || !strings.Contains(input, "change-prompt(INSERT > )") {
		t.Fatalf("text input did not start in INSERT: %s", input)
	}
	multi := strings.Join(fzfVimArgs(false, true), " ")
	if !strings.Contains(multi, "space:toggle") {
		t.Fatalf("multi-select bindings missing NORMAL Space: %s", multi)
	}
}

func TestSetupRepoPickerVimAcceptPreservesReviewCount(t *testing.T) {
	nonVim := strings.Join(setupRepoPickerActionBinds(false), "\n")
	vim := strings.Join(setupRepoPickerActionBinds(true), "\n")
	if strings.Contains(nonVim, "--bind=l:") {
		t.Fatalf("non-Vim picker unexpectedly binds l: %s", nonVim)
	}
	want := "--bind=l:transform:printf 'print(review:%s)+accept' \"$FZF_SELECT_COUNT\""
	if !strings.Contains(vim, want) {
		t.Fatalf("Vim l does not use Enter's count-preserving review transform: %s", vim)
	}
}
