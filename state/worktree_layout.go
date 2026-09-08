package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// Creation and repositioning share one placement contract. Empty split options
// mean join the caller window stack; no focus-changing operation is used.
type viewPlacement struct {
	Mode, Anchor, Direction string
	Size                    int
}

func placementFlags(fs *flag.FlagSet, defaultMode string) *viewPlacement {
	p := &viewPlacement{}
	fs.StringVar(&p.Mode, "placement", defaultMode, "window or split")
	fs.StringVar(&p.Anchor, "anchor", "", "caller, focused, view ID or root-local pane ID")
	fs.StringVar(&p.Direction, "direction", "", "stack (right is a compatibility alias)")
	fs.IntVar(&p.Size, "size", 0, "unsupported: use wt view master-width ROOT VIEW PERCENT")
	return p
}
func parsePlacement(fs *flag.FlagSet, p *viewPlacement, args []string) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments after view options")
	}
	explicitZero := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "size" && p.Size == 0 {
			explicitZero = true
		}
	})
	if explicitZero {
		return errors.New("split size is unsupported by mandatory master-stack; use wt view master-width ROOT VIEW PERCENT")
	}
	return p.validate()
}
func (p viewPlacement) validate() error {
	if p.Mode != "split" && p.Mode != "window" {
		return errors.New("placement must be window or split")
	}
	if p.Size != 0 {
		return errors.New("split size is unsupported by mandatory master-stack; use wt view master-width ROOT VIEW PERCENT")
	}
	if p.Mode == "window" && (p.Anchor != "" || p.Direction != "") {
		return errors.New("anchor and direction require --placement split")
	}
	switch p.Direction {
	case "", "stack", "right":
	default:
		return errors.New("mandatory master-stack: use --placement split [--direction stack] to join the anchor window, or --placement window; use wt view master-width for sizing")
	}

	return nil
}
func (p viewPlacement) splitArgs() []string {
	return []string{"-d", "-h", "-l", "10%"}
}

// Resolve once before mutation. Never guess between distinct attached clients,
// or let a pane/session spelling target a different root.
func (s *Store) placementAnchor(w Worktree, p viewPlacement, actor string) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	if p.Mode == "window" {
		return "", nil
	}
	live, err := s.liveRoot(w)
	if err != nil {
		return "", err
	}
	if !live {
		return "", errors.New("split placement requires a live root; restore it first")
	}
	anchor := p.Anchor
	if anchor == "" || anchor == "caller" || anchor == "self" {
		if actor == "" && os.Getenv("TMUX_PANE") != "" {
			anchor = os.Getenv("TMUX_PANE")
		} else {
			id := actor
			if id == "" {
				for _, a := range w.Agents {
					if a.Original {
						id = a.ID
						break
					}
				}
			}
			anchor = ""
			for _, v := range w.Views {
				if v.Kind == "agent" && v.Target == id {
					anchor = v.ID
					break
				}
			}
		}
	} else if anchor == "focused" {
		clients, err := tmux("list-clients", "-F", "#{session_name}\t#{pane_id}")
		if err != nil {
			return "", err
		}
		anchor = ""
		for _, line := range strings.Split(clients, "\n") {
			f := strings.Split(line, "\t")
			if len(f) != 2 || f[0] != w.Name {
				continue
			}
			if anchor != "" && anchor != f[1] {
				return "", errors.New("focused anchor is ambiguous across clients; use an explicit view or pane ID")
			}
			anchor = f[1]
		}
		if anchor == "" {
			return "", errors.New("no attached client in this root; use caller or an explicit anchor")
		}
	}
	pane := ""
	for _, v := range w.Views {
		live := livePane(w, v.ID)
		if anchor == v.ID || (live != "" && anchor == live) {
			pane = live
			break
		}
	}
	if pane == "" && strings.HasPrefix(anchor, "%") {
		panes, err := tmux("list-panes", "-s", "-t", "="+w.Name, "-F", "#{pane_id}\t#{@wt-manager}\t#{@wt-view}\tend")
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(panes, "\n") {
			f := strings.Split(line, "\t")
			if len(f) == 4 && f[0] == anchor {
				// Unknown tagged resources are not ours to reinterpret as human panes.
				if f[2] != "" || (f[1] != "" && f[1] != "human") {
					return "", errors.New("anchor has an unknown managed identity")
				}
				pane = anchor
			}
		}
	}
	if pane == "" {
		return "", errors.New("anchor must be a live view or pane in this root")
	}
	// Anchoring only chooses a window; reflow transfers no ownership.
	return pane, nil
}

// The caller holds the root lock and has revalidated the runtime identity.
func (s *Store) placeView(w Worktree, v View, p viewPlacement, actor string) error {
	if err := p.validate(); err != nil {
		return err
	}
	if v.Kind == "agent" || v.Kind == "shell" {
		return errors.New("placement supports editor, diff and presentation views; agents and named shells keep their own windows")
	}
	if actor != "" && (v.Manager != actor || v.Pinned) {
		return errors.New("human/pinned/peer-managed view is protected")
	}
	source := livePane(w, v.ID)
	if source == "" {
		return errors.New("view offline; restore worktree first")
	}
	if actor != "" && humanUsingPane(source) {
		return errors.New("actively used human pane is protected")
	}
	target, err := s.placementAnchor(w, p, actor)
	if err != nil {
		return err
	}
	if p.Mode == "split" {
		sourceWindow, e := tmux("display-message", "-p", "-t", source, "#{window_id}")
		if e != nil {
			return e
		}
		targetWindow, e := tmux("display-message", "-p", "-t", target, "#{window_id}")
		if e != nil {
			return e
		}
		if sourceWindow == targetWindow {
			if _, e = s.db.Exec(`UPDATE views SET parked=0 WHERE id=?`, v.ID); e != nil {
				return e
			}
			return s.snapshot(w)
		}
		target, err = stackInsertionTarget(w, target)
		if err != nil {
			return err
		}
	}
	if p.Mode == "window" {
		count, err := tmux("display-message", "-p", "-t", source, "#{window_panes}")
		if err != nil {
			return err
		}
		if count != "1" {
			if _, err = tmux("break-pane", "-d", "-s", source, "-n", v.Kind+"-"+v.ID[:6]); err != nil {
				return err
			}
		}
	} else {
		args := append([]string{"join-pane"}, p.splitArgs()...)
		args = append(args, "-s", source, "-t", target)
		if _, err := tmux(args...); err != nil {
			return fmt.Errorf("place view: %w", err)
		}
	}
	if _, err := s.db.Exec(`UPDATE views SET parked=0 WHERE id=?`, v.ID); err != nil {
		return err
	}
	return s.snapshot(w)
}

// Create directly in the target window: no temporary window, no focus switch.
func (s *Store) createSplitView(w Worktree, v View, p viewPlacement, anchor string) error {
	var err error
	anchor, err = stackInsertionTarget(w, anchor)
	if err != nil {
		_, cleanup := s.db.Exec(`DELETE FROM views WHERE id=?`, v.ID)
		return errors.Join(err, cleanup)
	}
	args := append([]string{"split-window"}, p.splitArgs()...)
	args = append(args, "-P", "-F", "#{pane_id}", "-t", anchor, stoppedCommand("initializing managed view"))
	pane, err := tmux(args...)
	if err != nil {
		_, cleanup := s.db.Exec(`DELETE FROM views WHERE id=?`, v.ID)
		return errors.Join(err, cleanup)
	}
	if err = s.launchView(w, v, pane); err != nil {
		// Retain the stable identity if cleanup fails, so recovery can reconcile it.
		if _, cleanup := tmux("kill-pane", "-t", pane); cleanup != nil {
			return errors.Join(err, cleanup)
		}
		_, cleanup := s.db.Exec(`DELETE FROM views WHERE id=?`, v.ID)
		return errors.Join(err, cleanup)
	}
	updated, err := s.Worktree(w.ID)
	if err != nil {
		return err
	}
	return s.snapshot(updated)
}
