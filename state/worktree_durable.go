package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Source default applies only at modern creation admission, never restore or
// stored native/legacy adapters (including legacy non-Pi roots).
func defaultPiBackend(w Worktree) string {
	if w.AgentFirst {
		return "durable"
	}
	return "native"
}

const durableDefinition = "wt-durable-v1"
const durableDependencies = "pi-durable=1.0.3;pi-ai=1.0.3;chord=1.0.3;pi-tui=0.87.1"

type DurableSnapshot struct {
	Store        string   `json:"store"`
	UUID         string   `json:"uuid"`
	Conversation int      `json:"conversation"`
	Definition   string   `json:"definition"`
	Dependencies string   `json:"dependencies"`
	Cwd          string   `json:"cwd"`
	Initialized  bool     `json:"initialized"`
	WriterLock   string   `json:"writer_lock"`
	ReadOnly     bool     `json:"read_only"`
	Job          string   `json:"job,omitempty"`
	Role         string   `json:"role,omitempty"`
	Tools        []string `json:"tools,omitempty"`
}

type DurableLaunch struct {
	Root     string          `json:"root"`
	Agent    string          `json:"agent"`
	Runtime  string          `json:"runtime"`
	Identity DurableSnapshot `json:"identity"`
}

func durableWriterLock(cwd string) string {
	root := cwd
	if output, err := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel").Output(); err == nil {
		if canonical, e := canonicalDir(strings.TrimSpace(string(output))); e == nil {
			root = canonical
		}
	}
	return filepath.Join(filepath.Dir(root), "."+filepath.Base(root)+".wt-durable-writer.lock")
}

func durableEntrypoint() string {
	return filepath.Clean(filepath.Join(filepath.Dir(configFile("wt-view.lua")), "..", "runtime", "pi-durable", "main.mjs"))
}

func validateDurable(a AgentSession) error {
	d := a.Adapter.Durable
	if a.Profile != "pi" || a.Adapter.Version != 1 || a.Adapter.Backend != "durable" || d == nil || !d.Initialized || d.UUID == "" || d.Conversation != 1 || d.Definition != durableDefinition || d.Dependencies != durableDependencies || a.NativeID != "" || a.Adapter.File != "" {
		return errors.New("incompatible or incomplete durable identity")
	}
	cwd, err := canonicalDir(a.Cwd)
	if err != nil || cwd != a.Cwd || d.Cwd != cwd || d.WriterLock != durableWriterLock(cwd) {
		return errors.New("durable assigned cwd changed or missing")
	}
	store, err := filepath.EvalSymlinks(d.Store)
	if err != nil {
		return fmt.Errorf("missing durable store: %w", err)
	}
	if !filepath.IsAbs(d.Store) || store != d.Store {
		return errors.New("durable store must retain its exact canonical path")
	}
	info, err := os.Stat(store)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("invalid durable store")
	}
	return nil
}

// Explicit bootstrap is only for a never-launched native placeholder or an
// interrupted durable bootstrap intent. Relaunch never creates a missing store.
func (s *Store) initializeDurable(w Worktree, id string, readOnly ...bool) error {
	a, err := w.agent(id)
	if err != nil {
		return err
	}
	delegated, err := s.isDelegatedChild(w.ID, a.ID)
	if err != nil {
		return err
	}
	if delegated || a.Profile != "pi" || a.Runtime != "" || a.NativeID != "" || a.Adapter.File != "" {
		return errors.New("durable bootstrap requires a fresh nondelegated Pi placeholder")
	}
	node, err := lockFile(nodeLockPath(a.ID))
	if err != nil {
		return err
	}
	defer node.Close()
	d := a.Adapter.Durable
	if a.Adapter.Backend == "durable" {
		if d == nil || d.Initialized {
			return errors.New("durable store is already initialized or incompatible")
		}
	} else {
		if a.Adapter.Backend != "" && a.Adapter.Backend != "native" {
			return errors.New("unsupported backend")
		}
		cwd, e := canonicalDir(a.Cwd)
		if e != nil {
			return e
		}
		directory := filepath.Join(stateDir(), "durable", w.ID, a.ID)
		if err = os.MkdirAll(directory, 0700); err != nil {
			return err
		}
		directory, err = canonicalDir(directory)
		if err != nil {
			return err
		}
		d = &DurableSnapshot{Store: filepath.Join(directory, "session.sqlite"), UUID: newID(), Conversation: 1, Definition: durableDefinition, Dependencies: durableDependencies, Cwd: cwd, WriterLock: durableWriterLock(cwd), ReadOnly: len(readOnly) > 0 && readOnly[0]}
		a.Adapter = PiSnapshot{Version: 1, Backend: "durable", Durable: d}
		// Persist intent before any store creation. A failed init remains stopped and
		// cannot accidentally fall back to a fresh ordinary Pi conversation.
		if _, err = s.db.Exec(`UPDATE agent_sessions SET adapter=?,cwd=?,stopped=1 WHERE root_id=? AND id=? AND runtime=''`, jsonText(a.Adapter), cwd, w.ID, a.ID); err != nil {
			return err
		}
	}
	storeLock, err := lockFile(d.Store + ".wt-lock")
	if err != nil {
		return err
	}
	defer storeLock.Close()
	command := exec.Command("node", durableEntrypoint(), "bootstrap")
	command.ExtraFiles = []*os.File{node, storeLock}
	command.Env = append(os.Environ(), "WT_DB="+dbPath(), "WT_STATUS_DIR="+stateDir())
	command.Stdin = strings.NewReader(jsonText(DurableLaunch{Root: w.ID, Agent: a.ID, Identity: *d}))
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err = command.Run(); err != nil {
		return err
	}
	d.Initialized = true
	_, err = s.db.Exec(`UPDATE agent_sessions SET adapter=?,stopped=0 WHERE root_id=? AND id=? AND runtime=''`, jsonText(a.Adapter), w.ID, a.ID)
	return err
}

func durableLaunchArgs(a AgentSession) ([]string, error) {
	if err := validateDurable(a); err != nil {
		return nil, err
	}
	b, err := json.Marshal(a.Adapter.Durable)
	if err != nil {
		return nil, err
	}
	mode := "interactive"
	if a.Adapter.Durable.Job != "" {
		mode = "job"
	}
	return []string{"node", durableEntrypoint(), mode, string(b)}, nil
}

// The public CLI honors task ceilings even when invoked by admitted writer bash.
// This is admission protection, not a sandbox against stripped identity/raw DB IO.
func (s *Store) authorizeDurableTaskCommand(args []string) error {
	actor := os.Getenv("WT_AGENT_ID")
	if actor == "" {
		return nil
	}
	w, err := s.Worktree(os.Getenv("WT_ROOT_ID"))
	if err != nil {
		return err
	}
	if err = validateActor(w, actor); err != nil {
		return err
	}
	a, _ := w.agent(actor)
	if a.Adapter.Durable == nil || a.Adapter.Durable.Job == "" {
		return nil
	}
	allowed := false
	switch args[0] {
	case "workspace":
		allowed = len(args) == 2 && args[1] == w.ID && containsTool(a.Adapter.Durable.Tools, "wt_workspace")
	case "agents", "view", "checkout":
		allowed = len(args) >= 3 && args[2] == w.ID && (args[1] == "list" || args[1] == "show" || (args[0] == "agents" && args[1] == "read"))
	case "hook":
		allowed = len(args) == 2 && (args[1] == "idle" || args[1] == "working")
	case "_durable-jobs":
		allowed = len(args) == 2 && (args[1] == "host" || args[1] == "finish") // separately capability-authenticated
	}
	if !allowed {
		return errors.New("durable task ceiling prohibits this WT command")
	}
	return nil
}

// Only an explicit human TUI command calls this private transport. It cannot
// stop a peer and never overrides ordinary model/CLI focus or pin protections.
func (s *Store) durableHumanStop(input io.Reader) error {
	var r struct {
		Root       string `json:"root"`
		Agent      string `json:"agent"`
		Runtime    string `json:"runtime"`
		Capability string `json:"capability"`
	}
	dec := json.NewDecoder(io.LimitReader(input, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return err
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("one self-stop command required")
	}
	if r.Root != os.Getenv("WT_ROOT_ID") || r.Agent != os.Getenv("WT_AGENT_ID") || r.Runtime != os.Getenv("WT_RUNTIME_ID") {
		return errors.New("self-stop identity mismatch")
	}
	w, err := s.Worktree(r.Root)
	if err != nil {
		return err
	}
	lock, err := s.rootLock(w)
	for i := 0; i < 200 && err != nil; i++ {
		time.Sleep(25 * time.Millisecond)
		lock, err = s.rootLock(w)
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	w, err = s.Worktree(r.Root)
	if err != nil {
		return err
	}
	if err = validateActor(w, r.Agent); err != nil {
		return err
	}
	a, _ := w.agent(r.Agent)
	if a.Adapter.Durable == nil || a.Adapter.Durable.Job != "" {
		return errors.New("only interactive durable human self-stop is supported")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var key string
	if err = tx.QueryRow(`SELECT key FROM durable_controls WHERE agent_id=? AND runtime=?`, r.Agent, r.Runtime).Scan(&key); err != nil {
		return err
	}
	if r.Capability == "" || key != delegationDigest([]byte(r.Capability)) {
		return errors.New("private human control capability required")
	}
	if _, err = tx.Exec(`UPDATE agent_sessions SET stopped=1,runtime='',status='idle' WHERE root_id=? AND id=? AND runtime=?`, r.Root, r.Agent, r.Runtime); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE inbox SET state='uncertain' WHERE root_id=? AND recipient=? AND state='claimed'`, r.Root, r.Agent); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM durable_controls WHERE agent_id=?`, r.Agent); err != nil {
		return err
	}
	return tx.Commit() // Owner closes recoverably; no peer cascade or cancellation claim.
}

// Top-level supported CLI admission includes legacy and registry routes. Exact
// actor resolution must never degrade a missing/stale task into human authority.
func (s *Store) authorizeDurableStateCommand(cmd string, args []string) error {
	actor := os.Getenv("WT_AGENT_ID")
	if actor == "" {
		return nil
	}
	w, err := s.Worktree(os.Getenv("WT_ROOT_ID"))
	if err != nil {
		return err
	}
	if err = validateActor(w, actor); err != nil {
		return err
	}
	a, _ := w.agent(actor)
	if a.Adapter.Durable == nil || a.Adapter.Durable.Job == "" {
		return nil
	}
	switch cmd {
	case "worktree":
		if len(args) == 0 {
			return errors.New("missing worktree task operation")
		}
		return s.authorizeDurableTaskCommand(args)
	case "get", "list", "counts", "fzf-bindings":
		return nil
	case "agents":
		if len(args) > 0 && (args[0] == "list" || args[0] == "hooks-status") {
			return nil
		}
	case "agent":
		if len(args) == 1 || (len(args) > 1 && strings.HasPrefix(args[1], "-")) {
			return nil
		}
	}
	return errors.New("durable task ceiling prohibits this state/registry command")
}
