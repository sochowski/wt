package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) captureShell(w Worktree, v *View, pane, cwd string) error {
	if v.State.Shell == nil {
		v.State.Shell = &ShellState{}
	}
	sh := v.State.Shell
	sh.Cwd = cwd
	if name, _ := tmux("show-option", "-wv", "-t", pane, "@wt-shell-name"); name != "" {
		sh.Name = name
		sh.Kind, _ = tmux("show-option", "-wv", "-t", pane, "@wt-shell-kind")
		sh.Log, _ = tmux("show-option", "-pqv", "-t", pane, "@wt-shell-log")
		if sh.Log == "" { // Pre-adapter managed shells did not have a pane log option.
			if filepath.Base(name) != name || name == "." || name == ".." {
				return errors.New("invalid managed shell name")
			}
			base := os.Getenv("WT_SHELL_DIR")
			if base == "" {
				base = filepath.Join(stateDir(), "shells")
			}
			sh.Log = filepath.Join(base, w.Name, "logs", name+".log")
		}
		v.State.Command, _ = tmux("show-option", "-wv", "-t", pane, "@wt-shell-command")
	}
	_, err := s.db.Exec(`UPDATE views SET state=? WHERE id=? AND root_id=?`, jsonText(v.State), v.ID, w.ID)
	return err
}
func (s *Store) registerShell(w Worktree, args []string, actor string) error {
	if len(args) != 1 {
		return errors.New("shell-register requires pane")
	}
	pane := args[0]
	panes, err := tmux("list-panes", "-s", "-t", "="+w.Name, "-F", "#{pane_id}")
	if err != nil {
		return err
	}
	found := false
	for _, p := range strings.Split(panes, "\n") {
		if p == pane {
			found = true
		}
	}
	if !found {
		return errors.New("pane not in root")
	}
	id, _ := tmux("show-option", "-pqv", "-t", pane, "@wt-view")
	v, e := w.view(id)
	if e != nil {
		manager := "human"
		if actor != "" {
			manager = actor
		}
		v = View{ID: newID(), RootID: w.ID, Kind: "shell", Target: w.ID, Manager: manager, State: ViewState{Version: 1, Restart: "never"}}
		if err = s.insertView(v); err != nil {
			return err
		}
	}
	if actor != "" && (v.Manager != actor || v.Pinned || humanUsingPane(pane)) {
		return errors.New("protected shell")
	}
	if v.Kind != "shell" {
		return errors.New("not a shell view")
	}
	cwd, err := tmux("display-message", "-p", "-t", pane, "#{pane_current_path}")
	if err != nil {
		return err
	}
	if err = s.captureShell(w, &v, pane, cwd); err != nil {
		return err
	}
	if err = s.bindView(w, v, pane); err != nil {
		return err
	}
	return s.snapshotLayout(w, false)
}
func (s *Store) bindShell(v View, pane string) error {
	sh := v.State.Shell
	if sh.Name == "" {
		return nil
	} // A manual pane has no managed-shell identity.
	for k, value := range map[string]string{"@wt-managed-shell": "1", "@wt-shell-name": sh.Name, "@wt-shell-kind": "interactive", "@wt-shell-command": ""} {
		if _, err := tmux("set-option", "-w", "-t", pane, k, value); err != nil {
			return err
		}
	}
	for k, value := range map[string]string{"@wt-shell-name": sh.Name, "@wt-shell-log": sh.Log} {
		if _, err := tmux("set-option", "-p", "-t", pane, k, value); err != nil {
			return err
		}
	}
	if sh.Log != "" {
		if err := os.MkdirAll(filepath.Dir(sh.Log), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(sh.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		f.Close()
		_, err = tmux("pipe-pane", "-o", "-t", pane, "umask 077; cat >> "+shellQuote(sh.Log))
		return err
	}
	return nil
}
