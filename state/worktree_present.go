package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// presentDeck lazily acquires an explicitly targeted view for its manager. It
// never reuses a pinned view or a pane an attached human is currently using.
func presentDeck(s *Store, w Worktree, args []string, actor string) error {
	if len(args) != 1 {
		return errors.New("usage: wt-state worktree present ROOT TARGET < deck.json")
	}
	root, err := w.targetPath(args[0])
	if err != nil {
		return err
	}
	if _, err = canonicalDir(root); err != nil {
		return err
	}
	target := w.ID
	for _, c := range w.Checkouts {
		if args[0] == c.ID || args[0] == c.Alias {
			target = c.ID
		}
	}
	var deck struct {
		Version    int               `json:"version"`
		Scenes     []json.RawMessage `json:"scenes"`
		StartIndex int               `json:"startIndex"`
	}
	bytes, err := io.ReadAll(io.LimitReader(os.Stdin, 1024*1024+1))
	if err != nil {
		return err
	}
	if len(bytes) > 1024*1024 {
		return errors.New("deck exceeds 1MB")
	}
	if err = json.Unmarshal(bytes, &deck); err != nil {
		return err
	}
	if deck.Version != 1 || len(deck.Scenes) == 0 {
		return errors.New("deck requires version 1 and scenes")
	}
	if err = validateDeckPaths(root, deck.Scenes); err != nil {
		return err
	}
	manager := actor
	if manager == "" {
		manager = "human"
	}
	v := View{}
	for _, candidate := range w.Views {
		if candidate.Kind == "presentation" && candidate.Target == target && candidate.Manager == manager && !candidate.Pinned && !humanUsingPane(livePane(w, candidate.ID)) {
			v = candidate
			break
		}
	}
	state := ViewState{Version: 1, Restart: "never", Deck: bytes, Slide: deck.StartIndex}
	if state.Slide < 1 {
		state.Slide = 1
	}
	created := v.ID == ""
	anchor := ""
	if created {
		anchor, err = s.placementAnchor(w, viewPlacement{Mode: "split"}, actor)
		if err != nil {
			return err
		}
	}
	if v.ID == "" {
		if len(w.Views) >= 32 {
			return errors.New("worktree limit: 32 views")
		}
		v = View{ID: newID(), RootID: w.ID, Kind: "presentation", Target: target, Manager: manager, State: state}
		if err = s.insertView(v); err != nil {
			return err
		}
	} else {
		v.State = state
		if _, err = s.db.Exec(`UPDATE views SET state=? WHERE id=?`, jsonText(state), v.ID); err != nil {
			return err
		}
	}
	w, err = s.Worktree(w.ID)
	if err != nil {
		return err
	}
	if created {
		err = s.createSplitView(w, v, viewPlacement{Mode: "split"}, anchor)
	} else {
		err = s.restore(w)
	}
	if err != nil {
		return err
	}
	w, err = s.Worktree(w.ID)
	if err != nil {
		return err
	}
	v, err = w.view(v.ID)
	if err != nil {
		return err
	}
	data := struct {
		Kind  string    `json:"kind"`
		Root  string    `json:"root"`
		State ViewState `json:"state"`
	}{"presentation", root, state}
	if err = os.WriteFile(filepath.Join(runtimeDir(), v.ID+".json"), []byte(jsonText(data)), 0600); err != nil {
		return err
	}
	for i := 0; i < 40; i++ {
		out, e := exec.Command("nvim", "--server", viewSocket(v.ID), "--remote-expr", `luaeval("require('wt_view').reload()")`).CombinedOutput()
		if e == nil {
			var checkpoint struct {
				Seq   int       `json:"seq"`
				State ViewState `json:"state"`
			}
			if e = json.Unmarshal(out, &checkpoint); e != nil {
				return e
			}
			if checkpoint.State.Version != 1 || len(checkpoint.State.Deck) == 0 {
				return errors.New("renderer did not publish deck")
			}
			tx, e := s.db.Begin()
			if e != nil {
				return e
			}
			defer tx.Rollback()
			res, e := tx.Exec(`UPDATE views SET state=?,checkpoint_seq=? WHERE id=? AND runtime=? AND checkpoint_seq<=?`, jsonText(checkpoint.State), checkpoint.Seq, v.ID, v.Runtime, checkpoint.Seq)
			if e != nil {
				return e
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				return errors.New("stale presentation publication")
			}
			if _, e = tx.Exec(`INSERT INTO presentation_selection(root_id,manager,view_id) VALUES(?,?,?) ON CONFLICT(root_id,manager) DO UPDATE SET view_id=excluded.view_id`, w.ID, manager, v.ID); e != nil {
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			printJSON(map[string]any{"ok": true, "view_id": v.ID, "target": target, "pane": livePane(w, v.ID)})
			return nil
		}
		err = fmt.Errorf("presentation view unavailable: %s", out)
		time.Sleep(100 * time.Millisecond)
	}
	return err
}
func validateDeckPaths(root string, scenes []json.RawMessage) error {
	for _, scene := range scenes {
		var s struct {
			Artifact struct {
				Kind  string `json:"kind"`
				Path  string `json:"path"`
				Root  string `json:"root"`
				Focus []struct {
					Path string `json:"path"`
				} `json:"focus"`
			} `json:"artifact"`
		}
		if err := json.Unmarshal(scene, &s); err != nil {
			return err
		}
		switch s.Artifact.Kind {
		case "file", "diff", "markdown", "tree":
		default:
			return errors.New("unsupported presentation artifact")
		}
		if s.Artifact.Path != "" {
			if _, err := containedPath(root, s.Artifact.Path); err != nil {
				return err
			}
		}
		tree := root
		if s.Artifact.Root != "" {
			var err error
			tree, err = containedPath(root, s.Artifact.Root)
			if err != nil {
				return err
			}
		}
		for _, focus := range s.Artifact.Focus {
			if _, err := containedPath(tree, focus.Path); err != nil {
				return err
			}
		}
	}
	return nil
}
