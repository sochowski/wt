package main

import (
	"errors"
	"os"
	"path/filepath"
)

func validateActor(w Worktree, actor string) error {
	if actor == "" {
		return nil
	}
	a, err := w.agent(actor)
	if err != nil || os.Getenv("WT_ROOT_ID") != w.ID || a.Runtime == "" || a.Runtime != os.Getenv("WT_RUNTIME_ID") || a.Stopped {
		return errors.New("stale or cross-root actor identity")
	}
	return nil
}

// Detach is deliberately conservative: referenced views or agent working
// directories must be changed/closed first. No filesystem operations occur.
func (s *Store) detachCheckout(w Worktree, alias string) error {
	var c Checkout
	for _, candidate := range w.Checkouts {
		if candidate.ID == alias || candidate.Alias == alias {
			c = candidate
		}
	}
	if c.ID == "" {
		return errors.New("checkout not attached")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow(`SELECT count(*) FROM views WHERE root_id=? AND target=?`, w.ID, c.ID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errors.New("checkout is referenced by a view")
	}
	for _, a := range w.Agents {
		rel, e := filepath.Rel(c.Path, a.Cwd)
		if e == nil && (rel == "." || (rel != ".." && len(rel) > 0 && !filepath.IsAbs(rel) && rel[:min(3, len(rel))] != "../")) {
			return errors.New("checkout is used by an agent cwd")
		}
	}
	// Clear the legacy attachment too, or its compatibility trigger would reattach it.
	if c.Alias == "original" {
		if _, err = tx.Exec(`UPDATE sessions SET wt_path='' WHERE name=?`, w.Name); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM checkouts WHERE root_id=? AND id=?`, w.ID, c.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) resumeView(w Worktree, v View) error {
	p := livePane(w, v.ID)
	dead := ""
	if p != "" {
		dead, _ = tmux("display-message", "-p", "-t", p, "#{pane_dead}")
	}
	stopped := false
	if v.Kind == "agent" {
		a, err := w.agent(v.Target)
		if err != nil {
			return err
		}
		stopped = a.Stopped
		if !stopped && v.Problem == "" && p != "" && dead != "1" {
			return nil
		}
		if a.Stopped && a.NativeID == "" {
			return errors.New("stopped conversation has no captured native identity; cannot resume exactly")
		}
		if _, err = strictPiArgs(a); err != nil {
			return err
		}
	}
	if !stopped && v.Problem == "" && p != "" && dead != "1" {
		return nil
	}
	// A human may be using the placeholder shell for repair. Never kill that shell.
	if humanUsingPane(p) {
		return errors.New("view actively used by a human; leave it before resuming")
	}
	if _, err := s.db.Exec(`UPDATE roots SET wake_enabled=0 WHERE id=?`, w.ID); err != nil {
		return err
	}
	if v.Kind == "agent" {
		if _, err := s.db.Exec(`UPDATE agent_sessions SET stopped=0 WHERE root_id=? AND id=?`, w.ID, v.Target); err != nil {
			return err
		}
	}
	w, err := s.Worktree(w.ID)
	if err != nil {
		return err
	}
	if p == "" {
		return s.restore(w)
	}
	return s.launchView(w, v, p)
}
