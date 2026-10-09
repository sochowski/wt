package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func cmdWorktree(s *Store, args []string) {
	if err := worktreeCommand(s, args); err != nil {
		fatalf("worktree: %v", err)
	}
}
func needArgs(args []string, n int, usage string) error {
	if len(args) < n {
		return errors.New("usage: wt " + usage)
	}
	return nil
}
func worktreeCommand(s *Store, args []string) error {
	if len(args) == 0 {
		return errors.New("commands: new roots agents checkout view message snapshot restore")
	}
	if err := s.authorizeDurableTaskCommand(args); err != nil {
		return err
	}
	cmd, args := args[0], args[1:]
	if cmd == "_durable-human-stop" {
		if len(args) != 0 {
			return errors.New("human self-stop accepts only JSON")
		}
		return s.durableHumanStop(os.Stdin)
	}
	if cmd == "_run-native-host" {
		if len(args) != 4 {
			return errors.New("native host requires root, child, runtime and private launch path")
		}
		return s.runDelegationHost(args[0], args[1], args[2], args[3])
	}
	if cmd == "_durable-jobs" {
		if len(args) != 1 {
			return errors.New("durable jobs require one v1 operation and JSON on stdin")
		}
		result, err := durableJobsCommand(s, args[0], os.Stdin)
		if err == nil {
			printJSON(result)
		}
		return err
	}
	if cmd == "_delegation" {
		if len(args) != 1 {
			return errors.New("internal delegation requires one operation and JSON on stdin")
		}
		var result any
		var err error
		if args[0] == "launch" {
			result, err = s.launchDelegationHost(os.Stdin)
		} else {
			result, err = delegationCommand(s, args[0], os.Stdin)
		}
		if err == nil {
			printJSON(result)
		}
		return err
	}
	if cmd == "setup-menu" {
		return setupMenu(s, args)
	}
	if cmd == "setup" {
		return setupCommand(s, args)
	}
	if cmd == "forgotten" {
		rows, err := s.db.Query(`SELECT path FROM forgotten_checkouts`)
		if err != nil {
			return err
		}
		defer rows.Close()
		paths := []string{}
		for rows.Next() {
			var p string
			if err = rows.Scan(&p); err != nil {
				return err
			}
			paths = append(paths, p)
		}
		printJSON(paths)
		return rows.Err()
	}
	if cmd == "forget" {
		if len(args) != 2 || os.Getenv("WT_AGENT_ID") != "" {
			return errors.New("forget requires human, name and checkout path")
		}
		// Do not remove durable setup intent while its Git operation is in flight.
		w, lookupErr := s.Worktree(args[0])
		if lookupErr == nil {
			lock, err := s.rootLock(w)
			if err != nil {
				return err
			}
			defer lock.Close()
		} else if lookupErr != sql.ErrNoRows {
			return lookupErr
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if args[1] != "" && args[1] != "?" {
			if _, err = tx.Exec(`INSERT OR IGNORE INTO forgotten_checkouts(path) VALUES(?)`, args[1]); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(`DELETE FROM sessions WHERE name=?`, args[0]); err != nil {
			return err
		}
		return tx.Commit()
	}
	if cmd == "view-checkpoint" {
		if len(args) != 3 {
			return errors.New("view checkpoint requires root, view, runtime")
		}
		var input struct {
			Seq   int       `json:"seq"`
			State ViewState `json:"state"`
		}
		if err := json.NewDecoder(io.LimitReader(os.Stdin, 1024*1024)).Decode(&input); err != nil {
			return err
		}
		if input.State.Version != 1 || args[2] == "" {
			return errors.New("invalid view checkpoint")
		}
		w, err := s.Worktree(args[0])
		if err != nil {
			return err
		}
		v, err := w.view(args[1])
		if err != nil {
			return err
		}
		root, err := w.targetPath(v.Target)
		if err != nil {
			return err
		}
		if err = validateDiffPosition(root, input.State); err != nil {
			return err
		}
		if v.Kind == "diff" && input.State.Base != v.State.Base {
			return errors.New("diff checkpoint changed base")
		}
		for _, file := range input.State.Files {
			if _, err = containedPath(root, file); err != nil {
				return err
			}
		}
		res, err := s.db.Exec(`UPDATE views SET state=?,checkpoint_seq=? WHERE root_id=? AND id=? AND runtime=? AND checkpoint_seq<?`, jsonText(input.State), input.Seq, w.ID, v.ID, args[2], input.Seq)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return errors.New("stale view checkpoint")
		}
		return nil
	}
	if cmd == "hook" {
		if err := needArgs(args, 1, "internal hook STATUS [NATIVE_ID]"); err != nil {
			return err
		}
		native := ""
		if len(args) > 1 {
			native = args[1]
		}
		return s.updateAgent(os.Getenv("WT_ROOT_ID"), os.Getenv("WT_AGENT_ID"), os.Getenv("WT_RUNTIME_ID"), args[0], native, nil)
	}
	if cmd == "update" {
		var req struct {
			Status  string     `json:"status"`
			Native  string     `json:"native_id"`
			Adapter PiSnapshot `json:"adapter"`
		}
		if err := json.NewDecoder(io.LimitReader(os.Stdin, 65536)).Decode(&req); err != nil {
			return err
		}
		return s.updateAgent(os.Getenv("WT_ROOT_ID"), os.Getenv("WT_AGENT_ID"), os.Getenv("WT_RUNTIME_ID"), req.Status, req.Native, &req.Adapter)
	}
	if cmd == "projection" {
		return projectionCommand(s, args)
	}
	if cmd == "workspace" {
		if len(args) != 1 {
			return errors.New("workspace ROOT")
		}
		w, err := s.Worktree(args[0])
		if err != nil {
			return err
		}
		printJSON(struct {
			Cwd       string         `json:"cwd"`
			Workspace *WorkspaceInfo `json:"workspace"`
			Checkouts []Checkout     `json:"checkouts"`
		}{w.Cwd, w.Workspace, w.Checkouts})
		return nil
	}
	if cmd == "roots" {
		query := ""
		if len(args) > 0 {
			query = args[0]
		}
		out, err := s.rootProjections(query)
		if err == nil {
			printJSON(out)
		}
		return err
	}
	if cmd == "new" {
		name := ""
		if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
			name = args[0]
			if name == "" {
				return errors.New("empty session name")
			}
			args = args[1:]
		}
		fs := flag.NewFlagSet("new", flag.ContinueOnError)
		cwd := fs.String("cwd", "", "working directory (no checkout attachment)")
		open := fs.Bool("switch", false, "focus new agent")
		offline := fs.Bool("offline", false, "persist without launching tmux")
		backend := fs.String("backend", "durable", "Pi backend for modern new sessions: durable or explicit native")
		durableProfile := fs.String("durable-profile", "", "explicit new-store native-compat-v2 plugin profile; default v1 unchanged")
		readOnly := fs.Bool("read-only", false, "durable host read-only coding ceiling")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return errors.New("unexpected new arguments")
		}
		explicitCwd := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "cwd" {
				explicitCwd = true
			}
		})
		if explicitCwd && *cwd == "" {
			return errors.New("explicit --cwd must be an existing directory")
		}
		if (*backend != "native" && *backend != "durable") || (*readOnly && *backend != "durable") || (*durableProfile != "" && (*durableProfile != durablePluginProfile || *backend != "durable")) {
			return errors.New("unsupported Pi backend/permission/profile selection")
		}
		w, err := s.createNamedRoot(name, *cwd)
		if err != nil {
			return err
		}
		if *backend == "durable" {
			if err = s.initializeDurableProfile(w, w.Agents[0].ID, *durableProfile, *readOnly); err != nil {
				return err
			}
			w, err = s.Worktree(w.ID)
			if err != nil {
				return err
			}
		}
		if !*offline {
			l, err := s.rootLock(w)
			if err != nil {
				return err
			}
			defer l.Close()
			if err = s.restore(w); err != nil {
				return err
			}
		}
		w, err = s.Worktree(w.ID)
		if err != nil {
			return err
		}
		printJSON(w)
		if *open {
			return openView(w, w.Views[0])
		}
		return nil
	}
	if cmd == "is-agent-first" {
		if len(args) != 1 {
			return errors.New("name required")
		}
		w, err := s.Worktree(args[0])
		if err != nil {
			return err
		}
		if !w.AgentFirst {
			return errors.New("legacy root")
		}
		return nil
	}
	if cmd == "run-agent" {
		if len(args) != 3 {
			return errors.New("run-agent requires root node runtime")
		}
		w, err := s.Worktree(args[0])
		if err != nil {
			return err
		}
		return s.runAgent(w, args[1], args[2])
	}
	group := ""
	if cmd == "agents" || cmd == "checkout" || cmd == "view" || cmd == "message" {
		group = cmd
		if len(args) == 0 {
			return errors.New("missing group operation")
		}
		cmd, args = args[0], args[1:]
	}
	if err := needArgs(args, 1, "[group operation] ROOT ..."); err != nil {
		return err
	}
	w, err := s.Worktree(args[0])
	if err != nil {
		return err
	}
	args = args[1:]
	actor := os.Getenv("WT_AGENT_ID")
	if actor != "" {
		a, err := w.agent(actor)
		if err != nil || os.Getenv("WT_ROOT_ID") != w.ID || a.Runtime == "" || a.Runtime != os.Getenv("WT_RUNTIME_ID") || a.Stopped {
			return errors.New("stale or cross-root actor identity")
		}
	}
	if group == "" && cmd == "shell-control" && shellReadOnly(args) {
		return s.shellControl(w, args, actor)
	}
	// Inbox operations use SQLite transactions, not the projection lock: the
	// extension must be able to report readiness while restore is launching it.
	if group == "message" {
		return messageCommand(s, w, cmd, args, actor)
	}
	if (group == "agents" || group == "view" || group == "checkout") && cmd == "list" {
		switch group {
		case "agents":
			printJSON(w.Agents)
		case "view":
			printJSON(w.Views)
		case "checkout":
			printJSON(w.Checkouts)
		}
		return nil
	}
	if group == "agents" && (cmd == "show" || cmd == "read" || cmd == "open") {
		if len(args) != 1 {
			return errors.New("agent operation requires node ID")
		}
		a, err := w.agent(args[0])
		if err != nil {
			return err
		}
		if cmd == "show" {
			printJSON(a)
			return nil
		}
		for _, v := range w.Views {
			if v.Kind == "agent" && v.Target == a.ID {
				if cmd == "open" {
					if actor != "" {
						return errors.New("only human may change focus")
					}
					return openView(w, v)
				}
				p := livePane(w, v.ID)
				if p == "" {
					return errors.New("agent is offline")
				}
				out, e := tmux("capture-pane", "-p", "-S", "-200", "-t", p)
				fmt.Println(out)
				return e
			}
		}
		return errors.New("agent has no view")
	}
	if group == "view" && (cmd == "show" || cmd == "open") {
		if len(args) != 1 {
			return errors.New("view operation requires view ID")
		}
		v, err := w.view(args[0])
		if err != nil {
			return err
		}
		if cmd == "show" {
			printJSON(v)
			return nil
		}
		if actor != "" {
			return errors.New("only human may change focus")
		}
		return openView(w, v)
	}
	lock, err := s.rootLock(w)
	if err != nil {
		// Bounded waiting also lets external topology events reconcile after a
		// foreground writer. Dropping a busy hook could leave a free layout.
		attempts := 200
		for i := 0; i < attempts && err != nil; i++ {
			time.Sleep(25 * time.Millisecond)
			lock, err = s.rootLock(w)
		}
		if err != nil {
			return err
		}
	}
	defer lock.Close()
	// Reload and authenticate again after locking; a stop may have won the race.
	w, err = s.Worktree(w.ID)
	if err != nil {
		return err
	}
	if err = validateActor(w, actor); err != nil {
		return err
	}
	switch group {
	case "agents":
		switch cmd {
		case "durable-init":
			if actor != "" || len(args) != 1 {
				return errors.New("durable-init requires human and fresh agent ID")
			}
			return s.initializeDurable(w, args[0])
		case "create":
			if len(args) == 0 {
				return errors.New("usage: wt agents create ROOT NAME [--parent ID] [--cwd root|CHECKOUT] [--open]")
			}
			name := args[0]
			fs := flag.NewFlagSet("agents create", flag.ContinueOnError)
			parent := fs.String("parent", actor, "supervisor ID")
			target := fs.String("cwd", "root", "root or attached checkout")
			open := fs.Bool("open", false, "human focus")
			task := fs.String("task", "", "initial durable delegated request")
			backend := fs.String("backend", defaultPiBackend(w), "Pi backend: durable for modern roots, explicit native for legacy")
			durableProfile := fs.String("durable-profile", "", "explicit new-store native-compat-v2 plugin profile; default v1 unchanged")
			readOnly := fs.Bool("read-only", false, "durable host read-only coding ceiling")
			if err = fs.Parse(args[1:]); err != nil {
				return err
			}
			if (*backend != "native" && *backend != "durable") || (*readOnly && *backend != "durable") || (*durableProfile != "" && (*durableProfile != durablePluginProfile || *backend != "durable")) {
				return errors.New("unsupported Pi backend/permission/profile selection")
			}
			if actor != "" && *open {
				return errors.New("agent creation cannot steal focus")
			}
			cwd, err := w.targetPath(*target)
			if err != nil {
				return err
			}
			if _, err = canonicalDir(cwd); err != nil {
				return err
			}
			id, err := s.addAgent(w, name, *parent, actor, cwd)
			if err != nil {
				return err
			}
			w, err = s.Worktree(w.ID)
			if err != nil {
				return err
			}
			if *backend == "durable" {
				if err = s.initializeDurableProfile(w, id, *durableProfile, *readOnly); err != nil {
					return err
				}
				w, err = s.Worktree(w.ID)
				if err != nil {
					return err
				}
			}
			if *task != "" {
				sender := actor
				if sender == "" {
					sender = "human"
				}
				if _, err = s.send(w, newID(), sender, id, *task); err != nil {
					return err
				}
			}
			if err = s.restore(w); err != nil {
				return err
			}
			w, _ = s.Worktree(w.ID)
			a, _ := w.agent(id)
			printJSON(a)
			if *open {
				for _, v := range w.Views {
					if v.Target == id {
						return openView(w, v)
					}
				}
			}
			return nil
		case "reparent":
			if len(args) != 2 {
				return errors.New("usage: wt agents reparent ROOT NODE PARENT|- ")
			}
			a, e := w.agent(args[0])
			if e != nil {
				return e
			}
			if actor != "" && actor != a.ID {
				return errors.New("agents may change only their own supervision link")
			}
			parent := args[1]
			if parent == "-" {
				parent = ""
			}
			return s.reparent(w, args[0], parent)
		case "resume":
			if len(args) != 1 || actor != "" {
				return errors.New("usage: wt agents resume ROOT NODE (human-only)")
			}
			a, e := w.agent(args[0])
			if e != nil {
				return e
			}
			for _, v := range w.Views {
				if v.Kind == "agent" && v.Target == a.ID {
					return s.resumeView(w, v)
				}
			}
			return errors.New("agent has no view")
		case "stop":
			if len(args) != 1 {
				return errors.New("usage: wt agents stop ROOT NODE")
			}
			a, err := w.agent(args[0])
			if err != nil {
				return err
			}
			if actor != "" && actor != a.ID && a.Parent != actor {
				return errors.New("agent may stop only itself or an agent it currently supervises")
			}
			for _, v := range w.Views {
				if v.Kind == "agent" && v.Target == a.ID && actor != "" && (v.Manager == "human" || v.Pinned || humanUsingPane(livePane(w, v.ID))) {
					return errors.New("human-managed/pinned agent view is protected")
				}
			}
			if _, err = s.db.Exec(`UPDATE agent_sessions SET stopped=1,runtime='',status='idle' WHERE root_id=? AND id=?`, w.ID, a.ID); err != nil {
				return err
			}
			if _, err = s.db.Exec(`UPDATE inbox SET state='uncertain' WHERE root_id=? AND recipient=? AND state='claimed'`, w.ID, a.ID); err != nil {
				return err
			}
			for _, v := range w.Views {
				if v.Kind == "agent" && v.Target == a.ID {
					if p := livePane(w, v.ID); p != "" {
						_, err = tmux("respawn-pane", "-k", "-t", p, stoppedCommand("agent stopped; children unaffected"))
						if err != nil {
							return err
						}
					}
				}
			}
			return nil
		}
	case "checkout":
		if cmd == "detach" {
			if len(args) != 1 {
				return errors.New("usage: wt checkout detach ROOT ALIAS|ID")
			}
			if err = s.detachCheckout(w, args[0]); err != nil {
				return err
			}
			if err = s.refreshWorkspace(w); err != nil {
				return fmt.Errorf("checkout detached; workspace publication incomplete (map may be stale): %w", err)
			}
			return nil
		}
		if cmd == "attach" {
			if len(args) != 2 {
				return errors.New("usage: wt checkout attach ROOT ALIAS PATH")
			}
			if !safeName.MatchString(args[0]) || args[0] == "root" {
				return errors.New("invalid/reserved alias")
			}
			path, err := canonicalDir(args[1])
			if err != nil {
				return err
			}
			if _, err = exec.Command("git", "-C", path, "rev-parse", "--git-dir").Output(); err != nil {
				return errors.New("checkout must be a git directory")
			}
			c := Checkout{ID: newID(), RootID: w.ID, Alias: args[0], Path: path, Ownership: "borrowed"}
			_, err = s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?)`, c.ID, w.ID, c.Alias, c.Path)
			if err != nil {
				return err
			}
			if err = s.refreshWorkspace(w); err != nil {
				return fmt.Errorf("checkout attached; workspace publication incomplete (map may be stale): %w", err)
			}
			printJSON(c)
			return nil
		}
	case "view":
		if cmd == "promote" || cmd == "master-width" || cmd == "cycle" {
			return s.stackCommand(w, cmd, args, actor)
		}
		if cmd == "place" {
			if len(args) < 1 {
				return errors.New("usage: wt view place ROOT VIEW [--placement split|window] [--anchor caller|focused|VIEW|PANE] [--direction stack]")
			}
			v, e := w.view(args[0])
			if e != nil {
				return e
			}
			fs := flag.NewFlagSet("view place", flag.ContinueOnError)
			p := placementFlags(fs, "split")
			if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
				// Compatibility with the initial positional placement command.
				if len(args) > 3 {
					return errors.New("unexpected positional placement arguments")
				}
				if args[1] == "window" {
					p.Mode = "window"
				} else {
					p.Direction = args[1]
				}
				if len(args) == 3 && p.Mode == "split" {
					p.Anchor = args[2]
				}
			} else if e = parsePlacement(fs, p, args[1:]); e != nil {
				return e
			}
			return s.placeView(w, v, *p, actor)
		}
		if cmd == "create" {
			return createView(s, w, args, actor)
		}
		if cmd == "resume" {
			if len(args) != 1 || actor != "" {
				return errors.New("usage: wt view resume ROOT VIEW (human-only)")
			}
			v, e := w.view(args[0])
			if e != nil {
				return e
			}
			return s.resumeView(w, v)
		}
		if cmd == "close" || cmd == "park" || cmd == "pin" || cmd == "unpin" {
			if len(args) != 1 {
				return errors.New("view operation requires view ID")
			}
			v, err := w.view(args[0])
			if err != nil {
				return err
			}
			if actor != "" && (v.Manager != actor || v.Pinned) {
				return errors.New("human/pinned/peer-managed view is protected")
			}
			if cmd == "pin" || cmd == "unpin" {
				if actor != "" {
					return errors.New("pin/unpin is human-only")
				}
				_, err = s.db.Exec(`UPDATE views SET pinned=? WHERE id=?`, cmd == "pin", v.ID)
				return err
			}
			if actor != "" && humanUsingPane(livePane(w, v.ID)) {
				return errors.New("actively used human pane is protected")
			}
			if cmd == "park" || v.Kind == "agent" || v.Kind == "shell" {
				if p := livePane(w, v.ID); p != "" {
					count, _ := tmux("display-message", "-p", "-t", p, "#{window_panes}")
					if count != "1" {
						if _, err = tmux("break-pane", "-d", "-s", p, "-n", "parked-"+v.ID[:6]); err != nil {
							return err
						}
					}
				}
				_, err = s.db.Exec(`UPDATE views SET parked=1 WHERE id=?`, v.ID)
				if err != nil {
					return err
				}
				return s.snapshot(w)
			}
			if p := livePane(w, v.ID); p != "" {
				if _, err = tmux("kill-pane", "-t", p); err != nil {
					return err
				}
			}
			_, err = s.db.Exec(`DELETE FROM views WHERE id=?`, v.ID)
			if err != nil {
				return err
			}
			return s.snapshot(w)
		}
	case "":
		if cmd == "stack-hook" {
			if actor != "" {
				return errors.New("internal hook requires human context")
			}
			changed, e := s.stackNeedsReflow(w)
			if e != nil || !changed {
				return e
			}
			return s.snapshotLayout(w, false)
		}
		if cmd == "present" {
			return presentDeck(s, w, args, actor)
		}
		if cmd == "present-clear" {
			return clearPresentation(s, w, args, actor)
		}
		if cmd == "shell-control" {
			return s.shellControl(w, args, actor)
		}
		if cmd == "shell-register" {
			if actor != "" {
				return errors.New("agents register shells through scoped shell creation")
			}
			return s.registerShell(w, args, actor)
		}
		if cmd == "shell-forget" {
			if len(args) != 1 || actor != "" {
				return errors.New("shell-forget requires human and pane")
			}
			_, err = s.db.Exec(`DELETE FROM views WHERE root_id=? AND kind='shell' AND pane=?`, w.ID, args[0])
			return err
		}
		if actor != "" {
			return errors.New("snapshot/restore is human-only")
		}
		switch cmd {
		case "snapshot":
			return s.snapshot(w)
		case "restore":
			if err = s.restore(w); err != nil {
				return err
			}
			w, err = s.Worktree(w.ID)
			if err == nil {
				printJSON(w)
			}
			return err
		}
	}
	return fmt.Errorf("unknown operation %s %s", group, cmd)
}
func openView(w Worktree, v View) error {
	p := livePane(w, v.ID)
	if p == "" {
		return errors.New("view offline; restore worktree first")
	}
	if _, err := tmux("select-window", "-t", p); err != nil {
		return err
	}
	if _, err := tmux("select-pane", "-t", p); err != nil {
		return err
	}
	if os.Getenv("TMUX") != "" {
		_, err := tmux("switch-client", "-t", "="+w.Name)
		return err
	}
	cmd := exec.Command("tmux", "attach-session", "-t", "="+w.Name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
func messageCommand(s *Store, w Worktree, cmd string, args []string, actor string) error {
	switch cmd {
	case "send":
		if len(args) < 3 {
			return errors.New("usage: wt message send ROOT SENDER RECIPIENT BODY [--id DEDUP_ID]")
		}
		if actor != "" && args[0] != actor {
			return errors.New("sender must match calling agent")
		}
		fs := flag.NewFlagSet("message send", flag.ContinueOnError)
		id := fs.String("id", newID(), "dedup ID")
		notify := fs.Bool("notify", false, "informational: do not wake recipient")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		m, err := s.send(w, *id, args[0], args[1], args[2], !*notify)
		if err == nil {
			printJSON(m)
		}
		return err
	case "poll":
		if len(args) < 1 {
			return errors.New("usage: wt message poll ROOT RECIPIENT [--after CURSOR]")
		}
		a, err := w.agent(args[0])
		if err != nil {
			return err
		}
		fs := flag.NewFlagSet("message poll", flag.ContinueOnError)
		after := fs.Int64("after", 0, "cursor")
		if err = fs.Parse(args[1:]); err != nil {
			return err
		}
		page, err := s.inboxPage(w.ID, a.ID, *after)
		if err == nil {
			printJSON(page)
		}
		return err
	case "list":
		if len(args) != 1 {
			return errors.New("usage: wt message list ROOT RECIPIENT")
		}
		a, err := w.agent(args[0])
		if err != nil {
			return err
		}
		m, err := s.messages(w.ID, a.ID)
		if err == nil {
			printJSON(m)
		}
		return err
	case "durable-claim":
		if len(args) != 1 || actor == "" {
			return errors.New("durable receipt requires live recipient and message ID")
		}
		return s.claimDurableInbox(w.ID, actor, os.Getenv("WT_RUNTIME_ID"), args[0])
	case "claim", "ack":
		if len(args) != 1 || actor == "" {
			return errors.New("receipt requires live recipient identity and message ID")
		}
		return s.receipt(w.ID, actor, os.Getenv("WT_RUNTIME_ID"), args[0], cmd)
	case "wake":
		if actor != "" {
			return errors.New("wake is human-only")
		}
		fs := flag.NewFlagSet("message wake", flag.ContinueOnError)
		budget := fs.Int("budget", 64, "automatic request delivery budget (1..64)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *budget < 1 || *budget > 64 {
			return errors.New("budget must be 1..64")
		}
		_, err := s.db.Exec(`UPDATE roots SET wake_enabled=1,wake_budget=? WHERE id=?`, *budget, w.ID)
		return err
	case "recover":
		if actor == "" {
			return errors.New("recover requires live recipient")
		}
		return s.recoverInbox(w.ID, actor, os.Getenv("WT_RUNTIME_ID"))
	case "retry":
		if len(args) != 1 || actor != "" {
			return errors.New("uncertain retry is human-only: wt message retry ROOT MESSAGE_ID")
		}
		_, err := s.db.Exec(`UPDATE inbox SET state='pending',runtime='' WHERE root_id=? AND id=? AND state='uncertain'`, w.ID, args[0])
		return err
	}
	return errors.New("unknown message operation")
}

type fileFlags []string

func (f *fileFlags) String() string     { return strings.Join(*f, ",") }
func (f *fileFlags) Set(s string) error { *f = append(*f, s); return nil }
func createView(s *Store, w Worktree, args []string, actor string) error {
	if len(args) < 2 {
		return errors.New("usage: wt view create ROOT editor|diff|presentation|shell TARGET [--file PATH] [--base REF] [--deck JSON_FILE] [--command TEXT]")
	}
	fs := flag.NewFlagSet("view create", flag.ContinueOnError)
	var files fileFlags
	fs.Var(&files, "file", "target-relative file")
	base := fs.String("base", "HEAD", "diff base")
	deck := fs.String("deck", "", "deck JSON file")
	command := fs.String("command", "", "shell command to run ONCE, never replayed")
	pin := fs.Bool("pin", false, "human pin")
	defaultPlacement := "split"
	if args[0] == "shell" {
		defaultPlacement = "window"
	}
	placement := placementFlags(fs, defaultPlacement)
	if err := parsePlacement(fs, placement, args[2:]); err != nil {
		return err
	}
	if actor != "" && *pin {
		return errors.New("only humans pin views")
	}
	if len(w.Views) >= 32 {
		return errors.New("worktree limit: 32 views")
	}
	path, err := w.targetPath(args[1])
	if err != nil {
		return err
	}
	if _, err = canonicalDir(path); err != nil {
		return err
	}
	target := w.ID
	if args[1] != "root" && args[1] != w.ID {
		for _, c := range w.Checkouts {
			if c.Alias == args[1] || c.ID == args[1] {
				target = c.ID
			}
		}
	}
	state := ViewState{Version: 1, Restart: "never", Files: files}
	switch args[0] {
	case "editor":
		for _, f := range files {
			if _, err = containedPath(path, f); err != nil {
				return err
			}
		}
	case "diff":
		if !safeBase.MatchString(*base) || strings.HasPrefix(*base, "-") {
			return errors.New("invalid diff base")
		}
		out, err := exec.Command("git", "-C", path, "rev-parse", "--verify", *base+"^{commit}").Output()
		if err != nil {
			return errors.New("diff base must resolve to a commit")
		}
		state.Base = strings.TrimSpace(string(out))
	case "presentation":
		if *deck == "" {
			return errors.New("presentation requires --deck JSON_FILE")
		}
		b, err := os.ReadFile(*deck)
		if err != nil {
			return err
		}
		var d map[string]any
		if err = json.Unmarshal(b, &d); err != nil {
			return err
		}
		state.Deck = b
		state.Slide = 1
	case "shell":
		state.Command = *command
	default:
		return errors.New("unsupported view type (agents use wt agents create)")
	}
	manager := "human"
	if actor != "" {
		manager = actor
	}
	v := View{ID: newID(), RootID: w.ID, Kind: args[0], Target: target, Manager: manager, Pinned: *pin, State: state}
	if placement.Mode == "split" && v.Kind == "shell" {
		return errors.New("named shells keep their own windows; split placement supports editor, diff and presentation")
	}
	anchor, err := s.placementAnchor(w, *placement, actor)
	if err != nil {
		return err
	}
	if err = s.insertView(v); err != nil {
		return err
	}
	w, err = s.Worktree(w.ID)
	if err != nil {
		return err
	}
	if placement.Mode == "split" {
		err = s.createSplitView(w, v, *placement, anchor)
	} else {
		err = s.restore(w)
	}
	if err != nil {
		return err
	}
	if v.Kind == "shell" && state.Command != "" {
		p := livePane(w, v.ID)
		if p == "" {
			return errors.New("shell pane unavailable")
		}
		if _, err = tmux("respawn-pane", "-k", "-t", p, "-c", path, "bash --noprofile --norc -c "+shellQuote(state.Command)+"; "+stoppedCommand("command finished/interrupted; not auto-restarted")); err != nil {
			return err
		}
	}
	w, err = s.Worktree(w.ID)
	if err != nil {
		return err
	}
	v, err = w.view(v.ID)
	if err != nil {
		return err
	}
	printJSON(v)
	return nil
}
