package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const defaultMasterPercent = 60

// A layout is geometry only: no process is restarted and no ownership changes.
// tmux assigns layout leaves in pane-index order, so reorder with detached swaps
// first, retaining the active pane by identity rather than by its old position.
func stackLayout(width, height, percent int, panes []string) (string, error) {
	if len(panes) == 0 || width < 1 || height < 1 || (len(panes) > 1 && width < 3) {
		return "", errors.New("window too small for master-stack")
	}
	leaf := func(w, h, x, y int, p string) string {
		return fmt.Sprintf("%dx%d,%d,%d,%s", w, h, x, y, strings.TrimPrefix(p, "%"))
	}
	body := leaf(width, height, 0, 0, panes[0])
	if len(panes) > 1 {
		n := len(panes) - 1
		if height < n*2-1 {
			return "", errors.New("window too short for stack; enlarge it or park a support view in a background window")
		}
		mw := (width - 1) * percent / 100
		if mw < 1 {
			mw = 1
		}
		if mw > width-2 {
			mw = width - 2
		}
		sw := width - mw - 1
		stack := []string{}
		y := 0
		for i, p := range panes[1:] {
			h := (height - (n - 1)) / n
			if i < (height-(n-1))%n {
				h++
			}
			stack = append(stack, leaf(sw, h, mw+1, y, p))
			y += h + 1
		}
		right := stack[0]
		if n > 1 {
			right = fmt.Sprintf("%dx%d,%d,0[%s]", sw, height, mw+1, strings.Join(stack, ","))
		}
		body = fmt.Sprintf("%dx%d,0,0{%s,%s}", width, height, leaf(mw, height, 0, 0, panes[0]), right)
	}
	var sum uint16
	for _, b := range []byte(body) {
		sum = (sum >> 1) | (sum << 15)
		sum += uint16(b)
	}
	return fmt.Sprintf("%04x,%s", sum, body), nil
}

// Validate capacity before adding a pane. Failure leaves existing processes and
// geometry untouched, rather than discovering an impossible stack after launch.
func stackInsertionTarget(w Worktree, anchor string) (string, error) {
	if !w.AgentFirst {
		return anchor, nil
	}
	out, err := tmux("display-message", "-p", "-t", anchor, "#{window_width} #{window_height} #{window_panes}")
	if err != nil {
		return "", err
	}
	var width, height, count int
	if _, err = fmt.Sscan(out, &width, &height, &count); err != nil {
		return "", err
	}
	panes := make([]string, count+1)
	for i := range panes {
		panes[i] = fmt.Sprint(i)
	}
	if _, err = stackLayout(width, height, defaultMasterPercent, panes); err != nil {
		return "", err
	}
	// The anchor chooses a window, never a split direction. Use its large master
	// as temporary insertion space even when the anchor is a tiny stack pane.
	if master := livePane(w, windowOption(anchor, "@wt-master-view")); master != "" {
		return master, nil
	}
	return anchor, nil
}

func windowOption(window, key string) string {
	out, _ := tmux("show-option", "-wqv", "-t", window, key)
	return out
}
func setWindowOption(window, key, value string) error {
	_, err := tmux("set-option", "-w", "-t", window, key, value)
	return err
}

func (s *Store) reflowWindow(w Worktree, window string) error {
	out, err := tmux("list-panes", "-t", window, "-F", "#{pane_id}\t#{@wt-view}\t#{pane_active}\t#{@wt-root}\t#{@wt-manager}\tend")
	if err != nil {
		return err
	}
	panes := []string{}
	ids := []string{}
	byID := map[string]string{}
	active := ""
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 6 {
			return errors.New("invalid pane metadata")
		}
		id := f[1]
		if _, e := w.view(id); e != nil {
			if id != "" || (f[3] != "" && f[3] != w.ID) || (f[4] != "" && f[4] != "human") {
				return errors.New("unknown managed pane identity; refusing ownership transfer during reflow")
			}
			// Raw human splits are adopted as idle-on-restore shells, never relaunched.
			id = newID()
			v := View{ID: id, RootID: w.ID, Kind: "shell", Target: w.ID, Manager: "human", State: ViewState{Version: 1, Restart: "never"}}
			if e = s.insertView(v); e != nil {
				return e
			}
			if e = s.bindView(w, v, f[0]); e != nil {
				return e
			}
		}
		panes = append(panes, f[0])
		ids = append(ids, id)
		byID[id] = f[0]
		if f[2] == "1" {
			active = f[0]
		}
	}
	order := []string{}
	seen := map[string]bool{}
	add := func(id string) {
		if byID[id] != "" && !seen[id] {
			order = append(order, id)
			seen[id] = true
		}
	}
	add(windowOption(window, "@wt-master-view"))
	for _, id := range strings.Fields(windowOption(window, "@wt-stack")) {
		add(id)
	}
	for _, id := range ids {
		add(id)
	}
	if len(order) == 0 {
		return nil
	}
	percent, _ := strconv.Atoi(windowOption(window, "@wt-master-percent"))
	if percent < 20 || percent > 80 {
		percent = defaultMasterPercent
	}
	var width, height int
	desired := []string{}
	for _, id := range order {
		desired = append(desired, byID[id])
	}
	// A newly created detached tmux window can briefly report 1x1 before the
	// server applies its default/client dimensions. Avoid turning that transient
	// state into a failed restore and a manual second attempt.
	for attempt := 0; attempt < 20; attempt++ {
		dims, e := tmux("display-message", "-p", "-t", window, "#{window_width} #{window_height}")
		if e != nil {
			return e
		}
		width, height = 0, 0
		if _, e = fmt.Sscan(dims, &width, &height); e != nil {
			return e
		}
		minHeight := 1
		if len(desired) > 1 {
			minHeight = (len(desired)-1)*2 - 1
		}
		if width >= 3 || len(desired) == 1 {
			if height >= minHeight {
				break
			}
		}
		if attempt < 19 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	layout, err := stackLayout(width, height, percent, desired)
	if err != nil {
		return err
	}
	for i, p := range desired {
		if panes[i] == p {
			continue
		}
		j := i + 1
		for j < len(panes) && panes[j] != p {
			j++
		}
		if _, err = tmux("swap-pane", "-d", "-s", p, "-t", panes[i]); err != nil {
			return err
		}
		panes[i], panes[j] = panes[j], panes[i]
	}
	current, _ := tmux("display-message", "-p", "-t", window, "#{window_layout}")
	if current != layout {
		if _, err = tmux("select-layout", "-t", window, layout); err != nil {
			return err
		}
	}
	now, _ := tmux("display-message", "-p", "-t", window, "#{pane_id}")
	if active != "" && now != active {
		if _, err = tmux("select-pane", "-t", active); err != nil {
			return err
		}
	}
	for key, value := range map[string]string{"@wt-master-view": order[0], "@wt-stack": strings.Join(order, " "), "@wt-master-percent": strconv.Itoa(percent), "@wt-stack-managed": "1", "@wt-stack-layout": layout} {
		if windowOption(window, key) != value {
			if err = setWindowOption(window, key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) reflow(w Worktree) error {
	var reloadErr error
	w, reloadErr = s.Worktree(w.ID)
	if reloadErr != nil {
		return reloadErr
	}
	if !w.AgentFirst {
		return nil
	}
	live, err := s.liveRoot(w)
	if err != nil || !live {
		return err
	}
	windows, err := tmux("list-windows", "-t", "="+w.Name, "-F", "#{window_id}")
	if err != nil {
		return err
	}
	for _, window := range strings.Fields(windows) {
		if err = s.reflowWindow(w, window); err != nil {
			return err
		}
	}
	return nil
}

// Own layout events are cheap no-ops. Keep each event queued behind the root
// lock rather than dropping a busy hook (which can lose external mutations).
func (s *Store) stackNeedsReflow(w Worktree) (bool, error) {
	if !w.AgentFirst {
		return false, nil
	}
	live, err := s.liveRoot(w)
	if err != nil || !live {
		return false, err
	}
	out, err := tmux("list-windows", "-t", "="+w.Name, "-F", "#{window_layout}\t#{@wt-stack-layout}\tend")
	if err != nil {
		return false, err
	}
	lines := strings.Split(out, "\n")
	if len(lines) != len(w.Layout.Windows) {
		return true, nil
	}
	for _, line := range lines {
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[0] != f[1] {
			return true, nil
		}
	}
	return false, nil
}

// Session-local, asynchronous and idempotent: our own select-layout event is a
// no-op on the next pass. The root lock serializes hooks with durable commands.
func (s *Store) installStackHook(w Worktree) error {
	if !w.AgentFirst {
		return nil
	}
	cli := shellArgs("env", "WT_AGENT_ID=", "WT_ROOT_ID=", "WT_RUNTIME_ID=", "WT_DB="+dbPath(), "WT_STATUS_DIR="+stateDir(), runtimeBinary(), "worktree", "view")
	if _, err := tmux("set-option", "-t", "="+w.Name, "@wt-stack-cli", cli); err != nil {
		return err
	}
	cmd := shellArgs("env", "WT_AGENT_ID=", "WT_ROOT_ID=", "WT_RUNTIME_ID=", "WT_DB="+dbPath(), "WT_STATUS_DIR="+stateDir(), runtimeBinary(), "worktree", "stack-hook", w.ID)
	for _, event := range []string{"window-layout-changed", "after-new-window", "window-unlinked"} {
		if _, err := tmux("set-hook", "-t", "="+w.Name, event+"[19731]", "run-shell -b "+shellQuote(cmd)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) stackCommand(w Worktree, cmd string, args []string, actor string) error {
	if !w.AgentFirst {
		return errors.New("master-stack commands require an agent-first worktree")
	}
	if len(args) < 1 {
		return errors.New("usage: wt view promote ROOT VIEW | wt view master-width ROOT VIEW PERCENT|+5|-5 | wt view cycle ROOT VIEW next|previous")
	}
	v, err := w.view(args[0])
	if err != nil {
		return err
	}
	pane := livePane(w, v.ID)
	if pane == "" {
		return errors.New("view offline; restore first")
	}
	// Reflow is not a source move/stop: pinned and peer panes keep identity,
	// ownership and focus. Only cycle changes focus and is human-only.
	switch cmd {
	case "promote":
		if len(args) != 1 {
			return errors.New("promote requires one view")
		}
		if err = setWindowOption(pane, "@wt-master-view", v.ID); err != nil {
			return err
		}
	case "master-width":
		if len(args) != 2 {
			return errors.New("master-width requires view and percent (20..80) or signed delta")
		}
		n, e := strconv.Atoi(args[1])
		if e != nil {
			return e
		}
		if strings.HasPrefix(args[1], "+") || strings.HasPrefix(args[1], "-") {
			old, _ := strconv.Atoi(windowOption(pane, "@wt-master-percent"))
			if old == 0 {
				old = defaultMasterPercent
			}
			n += old
		}
		if n < 20 || n > 80 {
			return errors.New("master width must be 20..80 percent")
		}
		if err = setWindowOption(pane, "@wt-master-percent", strconv.Itoa(n)); err != nil {
			return err
		}
	case "cycle":
		if actor != "" {
			return errors.New("only human may change focus")
		}
		if len(args) != 2 || (args[1] != "next" && args[1] != "previous") {
			return errors.New("cycle requires view and next|previous")
		}
		if err = s.reflow(w); err != nil {
			return err
		}
		order := strings.Fields(windowOption(pane, "@wt-stack"))
		for i, id := range order {
			if id == v.ID {
				delta := 1
				if args[1] == "previous" {
					delta = len(order) - 1
				}
				_, err = tmux("select-pane", "-t", livePane(w, order[(i+delta)%len(order)]))
				if err != nil {
					return err
				}
				break
			}
		}
	}
	return s.snapshotLayout(w, false)
}
