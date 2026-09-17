package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Runtime projection lives apart from the store: these commands alone touch tmux.
func tmux(args ...string) (string, error) {
	// tmux's '=' exact-name syntax is not consistent across target-session,
	// target-window and user-option commands. Resolve to the stable runtime $id.
	for i, arg := range args {
		if i == 0 || !strings.HasPrefix(arg, "=") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(arg, "="), ":", 2)
		out, _ := exec.Command("tmux", "list-sessions", "-F", "#{session_id}\t#{session_name}").Output()
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.SplitN(line, "\t", 2)
			if len(f) == 2 && f[1] == parts[0] {
				args[i] = f[0]
				if len(parts) == 2 {
					args[i] += ":" + parts[1]
				}
				break
			}
		}
	}
	b, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %s", strings.Join(args, " "), strings.TrimSpace(string(b)))
	}
	return strings.TrimSpace(string(b)), nil
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func shellArgs(args ...string) string {
	out := []string{}
	for _, s := range args {
		out = append(out, shellQuote(s))
	}
	return strings.Join(out, " ")
}
func lockFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("runtime already locked: %s: %w", path, err)
	}
	return f, nil
}
func (s *Store) rootLock(w Worktree) (*os.File, error) {
	p, _ := filepath.Abs(dbPath())
	return lockFile(p + "." + w.ID + ".lock")
}
func nodeLockPath(id string) string {
	p, _ := filepath.Abs(dbPath())
	return p + ".agent-" + id + ".lock"
}
func runtimeDir() string { return filepath.Join(stateDir(), "runtime") }
func viewSocket(id string) string { // Short enough for Unix socket path limits.
	h := sha256.Sum256([]byte(runtimeDir() + id))
	return filepath.Join(runtimeDir(), hex.EncodeToString(h[:8])+".sock")
}
func runtimeBinary() string {
	p, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return p
}
func configFile(name string) string {
	p := os.Getenv("WT_SOURCE_CONFIG")
	if p == "" {
		bin, _ := filepath.EvalSymlinks(runtimeBinary())
		p = filepath.Join(filepath.Dir(filepath.Dir(bin)), "config")
	}
	return filepath.Join(p, name)
}

func (s *Store) liveRoot(w Worktree) (bool, error) {
	// Never create a second projection on a different live server/socket.
	if w.Socket != "" {
		if b, err := exec.Command("tmux", "-S", w.Socket, "show-option", "-qv", "-t", w.Name, "@wt-root").Output(); err == nil && strings.TrimSpace(string(b)) == w.ID {
			socket, _ := tmux("display-message", "-p", "#{socket_path}")
			if socket != w.Socket {
				return false, errors.New("worktree is live on another tmux socket")
			}
		}
	}
	if _, err := tmux("has-session", "-t", "="+w.Name); err != nil {
		return false, nil
	}
	id, _ := tmux("show-option", "-qv", "-t", "="+w.Name, "@wt-root")
	if id != w.ID {
		return false, errors.New("tmux session name is occupied; refusing to replace/adopt unrelated live panes")
	}
	return true, nil
}
func livePane(w Worktree, id string) string {
	out, _ := tmux("list-panes", "-s", "-t", "="+w.Name, "-F", "#{pane_id} #{@wt-view} #{@wt-root}")
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[1] == id && f[2] == w.ID {
			return f[0]
		}
	}
	return ""
}
func (s *Store) bindView(w Worktree, v View, pane string) error {
	for k, value := range map[string]string{"@wt-root": w.ID, "@wt-view": v.ID, "@wt-pane-role": v.Kind, "@wt-manager": v.Manager} {
		if _, err := tmux("set-option", "-p", "-t", pane, k, value); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`UPDATE views SET pane=? WHERE root_id=? AND id=?`, pane, w.ID, v.ID)
	return err
}
func (s *Store) placeholder(v View, problem string) error {
	_, err := s.db.Exec(`UPDATE views SET problem=? WHERE id=?`, problem, v.ID)
	return err
}
func stoppedCommand(problem string) string {
	return "printf '%s\\n' " + shellQuote("wt: "+problem) + "; exec bash --noprofile --norc -i"
}

// strictPiArgs never invokes the legacy exact -> cwd-latest -> fresh ladder.
func strictPiArgs(a AgentSession) ([]string, error) {
	if a.Profile != "pi" {
		return nil, fmt.Errorf("exact restore unsupported for profile %s; use its legacy launcher manually", a.Profile)
	}
	if _, err := canonicalDir(a.Cwd); err != nil {
		return nil, fmt.Errorf("missing agent cwd: %w", err)
	}
	args := []string{"pi"}
	if a.Adapter.Version != 1 {
		return nil, errors.New("unsupported Pi snapshot version")
	}
	if a.NativeID != "" || a.Adapter.File != "" {
		if !a.Adapter.Persisted || a.Adapter.File == "" {
			return nil, errors.New("blank/unpersisted native session: no durable conversation to resume")
		}
		file, err := os.Open(a.Adapter.File)
		if err != nil {
			return nil, fmt.Errorf("missing transcript: %w", err)
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 65536), 32*1024*1024)
		var header struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Version int    `json:"version"`
		}
		if !scanner.Scan() {
			return nil, errors.New("empty transcript")
		}
		if err = json.Unmarshal(scanner.Bytes(), &header); err != nil || header.Type != "session" || header.ID != a.NativeID || header.Version != 3 {
			return nil, errors.New("transcript header does not match exact Pi v3 identity")
		}
		leaf := ""
		for scanner.Scan() {
			var e struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal(scanner.Bytes(), &e); err != nil {
				return nil, errors.New("invalid transcript entry")
			}
			leaf = e.ID
		}
		if err = scanner.Err(); err != nil {
			return nil, err
		}
		if leaf != a.Adapter.Leaf {
			return nil, errors.New("selected leaf differs from durable transcript tail; checkpoint in Pi before recovery")
		}
		args = append(args, "--session", a.Adapter.File)
	}
	if a.Adapter.Provider != "" {
		args = append(args, "--provider", a.Adapter.Provider)
	}
	if a.Adapter.Model != "" {
		args = append(args, "--model", a.Adapter.Model)
	}
	if a.Adapter.Thinking != "" {
		args = append(args, "--thinking", a.Adapter.Thinking)
	}
	return args, nil
}
func (s *Store) viewCommand(w Worktree, v View) (string, string, error) {
	cwd, err := w.targetPath(v.Target)
	if v.Kind == "agent" {
		a, e := w.agent(v.Target)
		if e != nil {
			return "", "", e
		}
		// A stopped agent is never relaunched, delegated or not. Finished
		// delegated children (e.g. completed workers/reviewers) therefore restore
		// as quiet placeholders instead of tripping the host fence on every revive.
		if a.Stopped {
			return stoppedCommand("agent stopped"), w.Cwd, nil
		}
		delegated, e := s.isDelegatedChild(w.ID, a.ID)
		if e != nil {
			return "", "", e
		}
		if delegated {
			return "", "", errors.New("delegated conversation requires its admitted interactive host; ordinary Pi launch is forbidden (the owning pi-subagents run must relaunch it, or stop the child)")
		}
		if a.Runtime != "" && a.NativeID == "" {
			return "", "", errors.New("previous launch has no captured native identity; refusing a fresh replacement")
		}
		if _, e = strictPiArgs(a); e != nil {
			return "", "", e
		}
		lock, e := lockFile(nodeLockPath(a.ID))
		if e != nil {
			return "", "", e
		}
		lock.Close()
		token := newID()
		_, e = s.db.Exec(`UPDATE agent_sessions SET runtime=?,status='idle' WHERE root_id=? AND id=?`, token, w.ID, a.ID)
		if e != nil {
			return "", "", e
		}
		_, e = s.db.Exec(`UPDATE inbox SET state='uncertain' WHERE root_id=? AND recipient=? AND state='claimed'`, w.ID, a.ID)
		if e != nil {
			return "", "", e
		}
		return shellArgs("env", "WT_DB="+dbPath(), "WT_STATUS_DIR="+stateDir(), "WT_SOURCE_CONFIG="+filepath.Dir(configFile("wt-view.lua")), runtimeBinary(), "worktree", "run-agent", w.ID, a.ID, token), a.Cwd, nil
	}
	if err != nil {
		return "", "", err
	}
	if _, err = canonicalDir(cwd); err != nil {
		return "", "", fmt.Errorf("missing borrowed target: %w", err)
	}
	if v.State.Version != 1 {
		return "", "", errors.New("unsupported view adapter")
	}
	switch v.Kind {
	case "shell": // Saved commands are data, never implicitly executed on restore.
		if v.State.Shell != nil && v.State.Shell.Cwd != "" {
			cwd, err = canonicalDir(v.State.Shell.Cwd)
			if err != nil {
				return "", "", err
			}
		}
		return stoppedCommand("shell restored idle; saved command was NOT restarted"), cwd, nil
	case "editor", "diff", "presentation":
		if _, err = exec.LookPath("nvim"); err != nil {
			return "", "", err
		}
		if err = validateDiffPosition(cwd, v.State); err != nil {
			return "", "", err
		}
		for _, p := range v.State.Files {
			if _, err = containedPath(cwd, p); err != nil {
				return "", "", err
			}
		}
		if err = os.MkdirAll(runtimeDir(), 0700); err != nil {
			return "", "", err
		}
		data := struct {
			Kind  string    `json:"kind"`
			Root  string    `json:"root"`
			State ViewState `json:"state"`
		}{v.Kind, cwd, v.State}
		path := filepath.Join(runtimeDir(), v.ID+".json")
		if err = os.WriteFile(path, []byte(jsonText(data)), 0600); err != nil {
			return "", "", err
		}
		token := newID()
		if _, err = s.db.Exec(`UPDATE views SET runtime=?,checkpoint_seq=0 WHERE id=?`, token, v.ID); err != nil {
			return "", "", err
		}
		return shellArgs("env", "WT_VIEW_ROOT_ID="+w.ID, "WT_VIEW_ID="+v.ID, "WT_VIEW_RUNTIME="+token, "WT_STATE="+runtimeBinary(), "WT_DB="+dbPath(), "WT_VIEW_STATE="+path, "WT_VIEW_ROOT="+cwd, "WT_PRESENT_MODULE="+configFile("wt-present.lua"), "nvim", "--listen", viewSocket(v.ID), "-c", "lua dofile("+luaString(configFile("wt-view.lua"))+")"), cwd, nil
	}
	return "", "", errors.New("unsupported view kind")
}
func luaString(s string) string { return jsonText(s) }
func containedPath(root, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	r, err := canonicalDir(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(r, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("file escapes explicit view target")
	}
	return p, nil
}

func (s *Store) restore(w Worktree) (err error) {
	if err := s.refreshWorkspace(w); err != nil {
		return err
	}
	live, err := s.liveRoot(w)
	if err != nil {
		return err
	}
	fresh := !live
	created := false
	defer func() {
		if err == nil || !created {
			return
		}
		// A failed projection must not look like a live, switchable root. Keep
		// every durable record, but remove the partial tmux projection and its
		// stale pane bindings so the next open can retry from a clean slate.
		tmux("kill-session", "-t", "="+w.Name)
		s.db.Exec(`UPDATE views SET pane='' WHERE root_id=?`, w.ID)
	}()
	if live {
		// Opening code trusts only a completed projection. Clear the marker
		// before reconciliation so any later error remains visibly retryable.
		if _, err = tmux("set-option", "-t", "="+w.Name, "@wt-restore-complete", "0"); err != nil {
			return err
		}
	}
	if fresh {
		if w.Socket != "" {
			if _, err = s.db.Exec(`UPDATE roots SET wake_enabled=0 WHERE id=?`, w.ID); err != nil {
				return err
			}
			if _, err = s.db.Exec(`UPDATE agent_sessions SET runtime=lower(hex(randomblob(16))),status='idle' WHERE root_id=? AND runtime<>'' AND stopped=0`, w.ID); err != nil {
				return err
			}
			if _, err = s.db.Exec(`UPDATE inbox SET state='uncertain' WHERE root_id=? AND state='claimed'`, w.ID); err != nil {
				return err
			}
		}
		cwd := w.Cwd
		if _, e := canonicalDir(cwd); e != nil {
			cwd = os.TempDir()
		}
		if _, err = tmux("new-session", "-d", "-s", w.Name, "-n", "main", "-c", cwd, "bash --noprofile --norc -i"); err != nil {
			return err
		}
		created = true
		if _, err = tmux("set-option", "-t", "="+w.Name, "@wt-root", w.ID); err != nil {
			return err
		}
		tmux("set-option", "-t", "="+w.Name, "@wt-session", w.Name)
		tmux("set-option", "-t", "="+w.Name, "@wt-root-cwd", w.Cwd)
		for key, value := range map[string]string{"WT_DB": dbPath(), "WT_STATUS_DIR": stateDir(), "WT_STATE": runtimeBinary(), "WT_SESSION": w.Name, "WT_ROOT_ID": "", "WT_AGENT_ID": "", "WT_RUNTIME_ID": ""} {
			if _, err = tmux("set-environment", "-t", "="+w.Name, key, value); err != nil {
				return err
			}
		}
		tmux("set-option", "-t", "="+w.Name, "remain-on-exit", "on")
		socket, _ := tmux("display-message", "-p", "-t", "="+w.Name, "#{socket_path}")
		if _, err = s.db.Exec(`UPDATE roots SET socket=? WHERE id=?`, socket, w.ID); err != nil {
			return err
		}
	}
	// Rebuild the checkpoint's ordinary windows, then place later-created views
	// in their own windows. Layout application only happens on a fresh server.
	placed := map[string]bool{}
	if fresh && len(w.Layout.Windows) > 0 {
		for _, win := range w.Layout.Windows {
			pane := ""
			window := ""
			for _, id := range win.Views {
				v, e := w.view(id)
				if e != nil {
					continue
				}
				if window == "" {
					if len(placed) == 0 {
						pane, err = tmux("display-message", "-p", "-t", "="+w.Name, "#{pane_id}")
						tmux("rename-window", "-t", pane, win.Name)
					} else {
						pane, err = tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "="+w.Name, "-n", win.Name, "bash --noprofile --norc -i")
					}
					window = pane
				} else {
					pane, err = tmux("split-window", "-d", "-h", "-l", "10%", "-P", "-F", "#{pane_id}", "-t", window, "bash --noprofile --norc -i")
				}
				if err != nil {
					return err
				}
				if err = s.launchView(w, v, pane); err != nil {
					return err
				}
				placed[id] = true
				if w.AgentFirst {
					if err = s.reflowWindow(w, window); err != nil {
						return err
					}
				}
			}
			if window != "" {
				if w.AgentFirst {
					if err = setWindowOption(window, "@wt-master-view", win.Master); err != nil {
						return err
					}
					if err = setWindowOption(window, "@wt-stack", strings.Join(win.Views, " ")); err != nil {
						return err
					}
					if err = setWindowOption(window, "@wt-master-percent", fmt.Sprint(win.MasterPercent)); err != nil {
						return err
					}
				} else if _, e := tmux("select-layout", "-t", window, win.Layout); e != nil {
					fmt.Fprintln(os.Stderr, "layout unavailable:", e)
				}
				if p := livePane(w, win.Active); p != "" {
					tmux("select-pane", "-t", p)
				}
			}
		}
	}
	for _, v := range w.Views {
		if placed[v.ID] {
			continue
		}
		if p := livePane(w, v.ID); p != "" {
			if err = s.bindView(w, v, p); err != nil {
				return err
			}
			dead, _ := tmux("display-message", "-p", "-t", p, "#{pane_dead}")
			if dead == "1" && v.Kind == "agent" {
				if _, err = s.db.Exec(`UPDATE roots SET wake_enabled=0 WHERE id=?`, w.ID); err != nil {
					return err
				}
				if err = s.launchView(w, v, p); err != nil {
					return err
				}
			}
			continue
		}
		pane := ""
		if fresh && len(placed) == 0 {
			pane, err = tmux("display-message", "-p", "-t", "="+w.Name, "#{pane_id}")
		} else {
			name := v.Kind + "-" + v.ID[:6]
			if v.Kind == "agent" {
				a, _ := w.agent(v.Target)
				name = a.Name
			}
			pane, err = tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "="+w.Name, "-n", name, "bash --noprofile --norc -i")
		}
		if err != nil {
			return err
		}
		if err = s.launchView(w, v, pane); err != nil {
			return err
		}
		placed[v.ID] = true
	}
	if fresh {
		active := ""
		if w.Layout.ActiveWindow < len(w.Layout.Windows) {
			active = w.Layout.Windows[w.Layout.ActiveWindow].Active
		} else if len(w.Views) > 0 {
			active = w.Views[0].ID
		}
		if p := livePane(w, active); p != "" {
			tmux("select-window", "-t", p)
		}
	}
	if err = s.installStackHook(w); err != nil {
		return err
	}
	if err = s.snapshot(w); err != nil {
		return err
	}
	_, err = tmux("set-option", "-t", "="+w.Name, "@wt-restore-complete", "1")
	return err
}
func (s *Store) launchView(w Worktree, v View, pane string) error {
	cmd, cwd, err := s.viewCommand(w, v)
	problem := ""
	if err != nil {
		problem = err.Error()
		fmt.Fprintf(os.Stderr, "unresolved %s: %s\n", v.ID, problem)
		cmd = stoppedCommand(problem)
		cwd = os.TempDir()
	}
	if err = s.placeholder(v, problem); err != nil {
		return err
	}
	if err = s.bindView(w, v, pane); err != nil {
		return err
	}
	_, err = tmux("respawn-pane", "-k", "-t", pane, "-c", cwd, cmd)
	if err == nil && v.Kind == "shell" && v.State.Shell != nil {
		err = s.bindShell(v, pane)
	}
	return err
}
func (s *Store) snapshot(w Worktree) error { return s.snapshotLayout(w, true) }
func (s *Store) snapshotLayout(w Worktree, adapters bool) error {
	live, err := s.liveRoot(w)
	if err != nil {
		return err
	}
	if !live {
		return errors.New("cannot checkpoint an offline worktree")
	}
	// Explicit checkpoints also activate policy on existing live projections,
	// without restore's side effect of relaunching missing/stopped views.
	if adapters {
		if err = s.installStackHook(w); err != nil {
			return err
		}
	}
	if err = s.reflow(w); err != nil {
		return err
	}
	// Reflow may have adopted a manual pane.
	w, err = s.Worktree(w.ID)
	if err != nil {
		return err
	}
	layout := LayoutSnapshot{Version: 1, Windows: []WindowSnapshot{}}
	windows, err := tmux("list-windows", "-t", "="+w.Name, "-F", "#{window_id}\t#{window_name}\t#{window_layout}\t#{window_active}\t#{window_index}")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(windows, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			return errors.New("unsupported window name/layout")
		}
		win := WindowSnapshot{Name: f[1], Layout: f[2], Views: []string{}}
		if w.AgentFirst {
			win.Master = windowOption(f[0], "@wt-master-view")
			fmt.Sscan(windowOption(f[0], "@wt-master-percent"), &win.MasterPercent)
		}
		if f[3] == "1" {
			layout.ActiveWindow = len(layout.Windows)
		}
		// tmux() trims trailing whitespace. Frame the record so an exited
		// last pane's empty cwd (or a cwd ending in whitespace) survives intact.
		panes, err := tmux("list-panes", "-t", f[0], "-F", "#{pane_id}\t#{@wt-view}\t#{pane_active}\t#{pane_current_path}\twt-pane-end")
		if err != nil {
			return err
		}
		for _, p := range strings.Split(panes, "\n") {
			fields := strings.Split(p, "\t")
			if len(fields) != 5 || fields[4] != "wt-pane-end" {
				return errors.New("unsupported pane metadata")
			}
			id := fields[1]
			v, e := w.view(id)
			if e != nil { // Manual panes are checkpointed as stopped human shells, never commands.
				id = newID()
				v = View{ID: id, RootID: w.ID, Kind: "shell", Target: w.ID, Manager: "human", State: ViewState{Version: 1, Restart: "never"}}
				if e = s.insertView(v); e != nil {
					return e
				}
				if e = s.bindView(w, v, fields[0]); e != nil {
					return e
				}
			}
			if v.Kind == "shell" {
				if e = s.captureShell(w, &v, fields[0], fields[3]); e != nil {
					return e
				}
			}
			if adapters && (v.Kind == "editor" || v.Kind == "diff" || v.Kind == "presentation") {
				out, e := exec.Command("nvim", "--server", viewSocket(v.ID), "--remote-expr", `luaeval("require('wt_view').snapshot()")`).Output()
				if e != nil {
					fmt.Fprintln(os.Stderr, "view checkpoint unavailable:", v.ID)
				} else {
					var state ViewState
					if e = json.Unmarshal(out, &state); e != nil {
						return e
					}
					root, e := w.targetPath(v.Target)
					if e != nil {
						return e
					}
					if e = validateDiffPosition(root, state); e != nil {
						return e
					}
					if v.Kind == "diff" && state.Base != v.State.Base {
						return errors.New("diff checkpoint changed base")
					}
					if state.Version != 1 {
						return errors.New("invalid view checkpoint")
					}
					if _, e = s.db.Exec(`UPDATE views SET state=? WHERE id=?`, jsonText(state), v.ID); e != nil {
						return e
					}
				}
			}
			win.Views = append(win.Views, id)
			if fields[2] == "1" {
				win.Active = id
			}
		}
		layout.Windows = append(layout.Windows, win)
	}
	_, err = s.db.Exec(`UPDATE roots SET layout=? WHERE id=?`, jsonText(layout), w.ID)
	return err
}
func (s *Store) insertView(v View) error {
	_, err := s.db.Exec(`INSERT INTO views(id,root_id,kind,target,manager,pinned,state) VALUES(?,?,?,?,?,?,?)`, v.ID, v.RootID, v.Kind, v.Target, v.Manager, v.Pinned, jsonText(v.State))
	return err
}

func (s *Store) runAgent(w Worktree, id, token string) error {
	a, err := w.agent(id)
	if err != nil {
		return err
	}
	if a.Runtime != token || token == "" || a.Stopped {
		return errors.New("stale agent launch")
	}
	delegated, err := s.isDelegatedChild(w.ID, a.ID)
	if err != nil {
		return err
	}
	if delegated {
		return errors.New("delegated conversation requires its admitted interactive host; ordinary Pi launch is forbidden")
	}
	lock, err := lockFile(nodeLockPath(id))
	if err != nil {
		return err
	}
	defer lock.Close()
	args, err := strictPiArgs(a)
	if err != nil {
		return err
	}
	// Agent-first launches bypass the legacy session-setup path. Seed the
	// selected cwd just before launch so Pi and other MCP-aware extensions see
	// WT's default profile, while preserving any project-owned .mcp.json.
	ensureMCPProfile(a.Cwd, wtConfigDir(), "default")
	var transcriptLock *os.File
	if a.Adapter.File != "" { // A lock beside the canonical transcript also fences other wt databases.
		file, e := filepath.EvalSymlinks(a.Adapter.File)
		if e != nil {
			return e
		}
		transcriptLock, err = lockFile(file + ".wt-lock")
		if err != nil {
			return err
		}
		defer transcriptLock.Close()
	}
	args = append(args, "--session-dir", filepath.Join(stateDir(), "conversations", a.ID))
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = a.Cwd
	cmd.Env = append(os.Environ(), "WT_ROOT_ID="+w.ID, "WT_AGENT_ID="+a.ID, "WT_RUNTIME_ID="+token, "WT_SESSION="+w.Name, "WT_STATE="+runtimeBinary(), "PI_SUBAGENT_REQUIRED_NATIVE_PROVIDER=wt-interactive-v1")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-signals:
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()
	err = cmd.Wait()
	_ = s.updateAgent(w.ID, a.ID, token, "idle", "", nil)
	return err
}

var safeBase = regexp.MustCompile(`^[A-Za-z0-9_./~^{}@-]+$`)

func humanUsingPane(pane string) bool {
	if pane == "" {
		return false
	}
	out, _ := tmux("list-clients", "-F", "#{pane_id}")
	for _, p := range strings.Split(out, "\n") {
		if p == pane {
			return true
		}
	}
	return false
}
