package main

import (
	"encoding/json"
	"errors"
	"os/exec"
)

func clearPresentation(s *Store, w Worktree, args []string, actor string) error {
	if len(args) != 0 {
		return errors.New("usage: wt-state worktree present-clear ROOT")
	}
	manager := actor
	if manager == "" {
		manager = "human"
	}
	var id string
	if err := s.db.QueryRow(`SELECT view_id FROM presentation_selection WHERE root_id=? AND manager=?`, w.ID, manager).Scan(&id); err != nil {
		return errors.New("no resolvable presentation selection")
	}
	v, err := w.view(id)
	if err != nil || v.Kind != "presentation" || v.Manager != manager {
		return errors.New("no resolvable presentation selection")
	}
	p := livePane(w, id)
	if v.Pinned || humanUsingPane(p) {
		return errors.New("pinned/actively used presentation is protected")
	}
	if p != "" {
		out, e := exec.Command("nvim", "--server", viewSocket(id), "--remote-expr", `luaeval("require('wt_view').clear()")`).CombinedOutput()
		if e != nil {
			return errors.New("presentation clear failed: " + string(out))
		}
		var checkpoint struct {
			Seq   int       `json:"seq"`
			State ViewState `json:"state"`
		}
		if err = json.Unmarshal(out, &checkpoint); err != nil {
			return err
		}
		if checkpoint.State.Version != 1 || len(checkpoint.State.Deck) > 0 {
			return errors.New("renderer did not end deck")
		}
		res, e := s.db.Exec(`UPDATE views SET state=?,checkpoint_seq=? WHERE root_id=? AND id=? AND runtime=? AND checkpoint_seq<=?`, jsonText(checkpoint.State), checkpoint.Seq, w.ID, id, v.Runtime, checkpoint.Seq)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errors.New("stale presentation clear")
		}
		return nil
	}
	v.State.Deck = nil
	v.State.Slide = 0
	// Invalidate old checkpoint subprocesses when ending an offline renderer.
	_, err = s.db.Exec(`UPDATE views SET state=?,runtime='',checkpoint_seq=0 WHERE root_id=? AND id=?`, jsonText(v.State), w.ID, v.ID)
	return err
}
