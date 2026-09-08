package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func shellReadOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "ls", "list", "read", "wait":
		return true
	case "events":
		for _, a := range args[1:] {
			if a == "--consume" {
				return false
			}
		}
		return true
	}
	return false
}

// Run the existing shell CLI under the same projection lock/actor policy as
// views. The child skips registration; this writer registers its result before
// releasing the lock. Observations need no projection lock (wait may be long).
func (s *Store) shellControl(w Worktree, args []string, actor string) error {
	if actor == "" || len(args) == 0 {
		return errors.New("scoped shell requires agent and operation")
	}
	op := args[0]
	if op == "open" || op == "pick" {
		return errors.New("agents cannot change shell focus")
	}
	for _, arg := range args[1:] {
		if arg == "--switch" {
			return errors.New("agents cannot change shell focus")
		}
		if arg == "--" {
			break
		}
	}
	creating := op == "new" || op == "run"
	if op == "new" {
		detached := false
		for _, a := range args[1:] {
			if a == "--detach" || a == "--no-switch" {
				detached = true
			}
		}
		if !detached {
			return errors.New("agent shell new requires --detach")
		}
	}
	var v View
	targetPane := ""
	mutating := !shellReadOnly(args)
	if !creating && (mutating || op == "read" || op == "wait") {
		if len(args) < 2 {
			return errors.New("shell operation requires managed shell name")
		}
		for _, candidate := range w.Views {
			if candidate.Kind == "shell" && candidate.State.Shell != nil && candidate.State.Shell.Name == args[1] {
				v = candidate
			}
		}
		targetPane = livePane(w, v.ID)
		if v.ID == "" || targetPane == "" {
			return errors.New("managed shell is offline or unknown")
		}
		if mutating && (v.Manager != actor || v.Pinned || humanUsingPane(targetPane)) {
			return errors.New("human/pinned/peer-managed/actively used shell is protected")
		}
		if op == "rm" || op == "delete" {
			panes, err := tmux("display-message", "-p", "-t", targetPane, "#{window_panes}")
			if err != nil || panes != "1" {
				return errors.New("shell window contains other panes; separate it before removing")
			}
		}
	}
	switch op {
	case "new", "run", "ls", "list", "read", "wait", "send", "stop", "rm", "delete", "watch", "unwatch", "events":
	default:
		return errors.New("unsupported scoped shell operation")
	}
	if creating && len(w.Views) >= 32 {
		return errors.New("worktree limit: 32 views")
	}
	before, _ := tmux("list-panes", "-s", "-t", "="+w.Name, "-F", "#{pane_id}")
	existing := map[string]bool{}
	for _, p := range strings.Split(before, "\n") {
		existing[p] = true
	}
	cmd := exec.Command(filepath.Join(filepath.Dir(filepath.Dir(configFile("wt-view.lua"))), "bin", "wt"), append([]string{"shell", "--session", w.Name}, args...)...)
	env := []string{}
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "WT_AGENT_ID=") && !strings.HasPrefix(e, "WT_SHELL_SCOPED=") && !strings.HasPrefix(e, "WT_SHELL_TARGET_PANE=") {
			env = append(env, e)
		}
	}
	// Bash must act on the exact pane authorized above, never the window's
	// currently active pane (a human can change focus without our root lock).
	cmd.Env = append(env, "WT_AGENT_ID=", "WT_SHELL_SCOPED=1", "WT_SHELL_TARGET_PANE="+targetPane)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	if op == "rm" || op == "delete" {
		_, err := s.db.Exec(`DELETE FROM views WHERE root_id=? AND id=?`, w.ID, v.ID)
		if err != nil {
			return err
		}
		return s.snapshotLayout(w, false)
	}
	if creating {
		out, err := tmux("list-panes", "-s", "-t", "="+w.Name, "-F", "#{pane_id}\t#{@wt-view}\t#{@wt-managed-shell}")
		if err != nil {
			return err
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Split(line, "\t")
			if len(f) == 3 && !existing[f[0]] && f[1] == "" && f[2] == "1" {
				if err = s.registerShell(w, []string{f[0]}, actor); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
