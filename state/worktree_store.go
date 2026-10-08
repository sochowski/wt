package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// AgentSession is a conversation instance, not an agent profile (agents.go).
type AgentSession struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	RootID   string     `json:"root_id"`
	Parent   string     `json:"parent_agent_id"`
	Creator  string     `json:"creator_agent_id"`
	Profile  string     `json:"profile"`
	Cwd      string     `json:"cwd"`
	Status   string     `json:"status"`
	NativeID string     `json:"native_id"`
	Runtime  string     `json:"runtime"`
	Stopped  bool       `json:"stopped"`
	Original bool       `json:"original"`
	Adapter  PiSnapshot `json:"adapter"`
}

// PiSnapshot discriminates exact durable stores from the legacy native adapter.
// Empty Backend remains native for existing records; no migration is performed.
type PiSnapshot struct {
	Backend   string           `json:"backend,omitempty"`
	Durable   *DurableSnapshot `json:"durable,omitempty"`
	Version   int              `json:"version"`
	File      string           `json:"file"`
	Persisted bool             `json:"persisted"`
	Leaf      string           `json:"leaf"`
	Provider  string           `json:"provider"`
	Model     string           `json:"model"`
	Thinking  string           `json:"thinking"`
}
type Checkout struct {
	ID        string `json:"id"`
	RootID    string `json:"root_id"`
	Alias     string `json:"alias"`
	Path      string `json:"path"`
	Ownership string `json:"ownership"`
}
type DiffPosition struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Topline int    `json:"topline"`
	Leftcol int    `json:"leftcol"`
}
type ViewState struct {
	Diff    *DiffPosition   `json:"diff,omitempty"`
	Version int             `json:"version"`
	Files   []string        `json:"files,omitempty"`
	Base    string          `json:"base,omitempty"`
	Deck    json.RawMessage `json:"deck,omitempty"`
	Slide   int             `json:"slide,omitempty"`
	Command string          `json:"command,omitempty"`
	Restart string          `json:"restart"`
	Shell   *ShellState     `json:"shell,omitempty"`
	Editor  *EditorState    `json:"editor,omitempty"`
}
type ShellState struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Cwd  string `json:"cwd"`
	Log  string `json:"log"`
}
type EditorState struct {
	Tabs         []EditorLayout `json:"tabs"`
	ActiveTab    int            `json:"active_tab"`
	ActiveWindow int            `json:"active_window"`
}
type EditorLayout struct {
	Kind     string         `json:"kind"`
	Children []EditorLayout `json:"children,omitempty"`
	File     string         `json:"file,omitempty"`
	Cursor   []int          `json:"cursor,omitempty"`
	Topline  int            `json:"topline,omitempty"`
	Leftcol  int            `json:"leftcol,omitempty"`
	Width    int            `json:"width,omitempty"`
	Height   int            `json:"height,omitempty"`
}
type View struct {
	Runtime string    `json:"runtime"`
	ID      string    `json:"id"`
	RootID  string    `json:"root_id"`
	Kind    string    `json:"kind"`
	Target  string    `json:"target"`
	Manager string    `json:"manager"`
	Pinned  bool      `json:"pinned"`
	Parked  bool      `json:"parked"`
	Pane    string    `json:"pane"`
	Problem string    `json:"problem"`
	State   ViewState `json:"state"`
}
type WindowSnapshot struct {
	Master        string   `json:"master,omitempty"`
	MasterPercent int      `json:"master_percent,omitempty"`
	Name          string   `json:"name"`
	Layout        string   `json:"layout"`
	Views         []string `json:"views"`
	Active        string   `json:"active"`
}
type LayoutSnapshot struct {
	Version      int              `json:"version"`
	Windows      []WindowSnapshot `json:"windows"`
	ActiveWindow int              `json:"active_window"`
}
type Worktree struct {
	Workspace   *WorkspaceInfo `json:"workspace,omitempty"`
	WakeEnabled bool           `json:"wake_enabled"`
	WakeBudget  int            `json:"wake_budget"`
	AgentFirst  bool           `json:"agent_first"`
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Cwd         string         `json:"cwd"`
	Socket      string         `json:"socket"`
	Layout      LayoutSnapshot `json:"layout"`
	Agents      []AgentSession `json:"agents"`
	Checkouts   []Checkout     `json:"checkouts"`
	Views       []View         `json:"views"`
}
type InboxMessage struct {
	SenderKind string `json:"sender_kind"`
	Request    bool   `json:"request"`
	ID         string `json:"id"`
	RootID     string `json:"root_id"`
	Sender     string `json:"sender"`
	Recipient  string `json:"recipient"`
	Body       string `json:"body"`
	State      string `json:"state"`
	Runtime    string `json:"runtime"`
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$`)

func (s *Store) migrateWorktrees() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > 7 {
		return fmt.Errorf("unsupported database schema %d", version)
	}
	if version == 7 {
		return tx.Commit()
	}
	if version == 0 {
		_, err = tx.Exec(`
 CREATE TABLE roots(id TEXT PRIMARY KEY, name TEXT UNIQUE NOT NULL REFERENCES sessions(name) ON DELETE CASCADE, wake_enabled INTEGER NOT NULL DEFAULT 1, wake_budget INTEGER NOT NULL DEFAULT 64, agent_first INTEGER NOT NULL DEFAULT 0, socket TEXT NOT NULL DEFAULT '', layout TEXT NOT NULL DEFAULT '{"version":1}');
 CREATE TABLE agent_sessions(id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id) ON DELETE CASCADE, name TEXT NOT NULL DEFAULT 'main', parent TEXT NOT NULL DEFAULT '', creator TEXT NOT NULL DEFAULT '', profile TEXT NOT NULL DEFAULT 'pi', cwd TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'idle', native_id TEXT NOT NULL DEFAULT '', runtime TEXT NOT NULL DEFAULT '', stopped INTEGER NOT NULL DEFAULT 0, original INTEGER NOT NULL DEFAULT 0, adapter TEXT NOT NULL DEFAULT '{"version":1}', UNIQUE(root_id,id), UNIQUE(root_id,name));
 CREATE UNIQUE INDEX original_agent ON agent_sessions(root_id) WHERE original=1;
 CREATE UNIQUE INDEX transcript_writer ON agent_sessions(json_extract(adapter,'$.file')) WHERE json_extract(adapter,'$.file')<>'';
 CREATE TABLE checkouts(id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id) ON DELETE CASCADE, alias TEXT NOT NULL, path TEXT NOT NULL, ownership TEXT NOT NULL DEFAULT 'borrowed' CHECK(ownership='borrowed'), UNIQUE(root_id,alias));
 CREATE TABLE views(id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id) ON DELETE CASCADE, kind TEXT NOT NULL, target TEXT NOT NULL, manager TEXT NOT NULL DEFAULT 'human', pinned INTEGER NOT NULL DEFAULT 0, parked INTEGER NOT NULL DEFAULT 0, runtime TEXT NOT NULL DEFAULT '', checkpoint_seq INTEGER NOT NULL DEFAULT 0, pane TEXT NOT NULL DEFAULT '', problem TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT '{"version":1,"restart":"never"}');
 CREATE TABLE inbox(id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id) ON DELETE CASCADE, sender TEXT, sender_kind TEXT NOT NULL DEFAULT 'agent' CHECK(sender_kind IN ('human','agent')), request INTEGER NOT NULL DEFAULT 1, recipient TEXT NOT NULL, body TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed','delivered','uncertain')), runtime TEXT NOT NULL DEFAULT '', FOREIGN KEY(root_id,sender) REFERENCES agent_sessions(root_id,id), FOREIGN KEY(root_id,recipient) REFERENCES agent_sessions(root_id,id));
 INSERT INTO roots(id,name) SELECT lower(hex(randomblob(16))),name FROM sessions;
 INSERT INTO agent_sessions(id,root_id,profile,cwd,status,native_id,original) SELECT lower(hex(randomblob(16))),r.id,CASE WHEN s.agent='' THEN 'pi' ELSE s.agent END,s.workspace_path,s.status,s.agent_session_id,1 FROM sessions s JOIN roots r ON r.name=s.name;
 INSERT INTO checkouts(id,root_id,alias,path) SELECT lower(hex(randomblob(16))),r.id,'original',s.wt_path FROM sessions s JOIN roots r ON r.name=s.name WHERE s.wt_path<>'';
 INSERT INTO views(id,root_id,kind,target) SELECT lower(hex(randomblob(16))),root_id,'agent',id FROM agent_sessions;
 CREATE TRIGGER legacy_root_insert AFTER INSERT ON sessions BEGIN
 INSERT INTO roots(id,name) VALUES(lower(hex(randomblob(16))),NEW.name);
 INSERT INTO agent_sessions(id,root_id,profile,cwd,status,native_id,original) SELECT lower(hex(randomblob(16))),id,CASE WHEN NEW.agent='' THEN 'pi' ELSE NEW.agent END,NEW.workspace_path,NEW.status,NEW.agent_session_id,1 FROM roots WHERE name=NEW.name;
 INSERT INTO views(id,root_id,kind,target) SELECT lower(hex(randomblob(16))),a.root_id,'agent',a.id FROM agent_sessions a JOIN roots r ON r.id=a.root_id WHERE r.name=NEW.name;
 INSERT INTO checkouts(id,root_id,alias,path) SELECT lower(hex(randomblob(16))),id,'original',NEW.wt_path FROM roots WHERE name=NEW.name AND NEW.wt_path<>'';
 END;
 CREATE TRIGGER legacy_agent_update AFTER UPDATE ON sessions BEGIN
 UPDATE agent_sessions SET profile=CASE WHEN NEW.agent='' THEN profile ELSE NEW.agent END,cwd=NEW.workspace_path,status=NEW.status,native_id=NEW.agent_session_id WHERE root_id=(SELECT id FROM roots WHERE name=NEW.name) AND original=1;
 INSERT INTO checkouts(id,root_id,alias,path) SELECT lower(hex(randomblob(16))),id,'original',NEW.wt_path FROM roots WHERE name=NEW.name AND NEW.wt_path<>'' ON CONFLICT(root_id,alias) DO UPDATE SET path=excluded.path;
 END;
 CREATE TRIGGER original_agent_update AFTER UPDATE OF status,native_id ON agent_sessions WHEN NEW.original=1 BEGIN
 UPDATE sessions SET status=NEW.status,agent_session_id=NEW.native_id WHERE name=(SELECT name FROM roots WHERE id=NEW.root_id) AND (status<>NEW.status OR agent_session_id<>NEW.native_id);
 END;
 PRAGMA user_version=1;`)
		if err != nil {
			return err
		}
	}
	if version < 2 {
		_, err = tx.Exec(`
 CREATE TABLE forgotten_checkouts(path TEXT PRIMARY KEY);
 CREATE TABLE presentation_selection(root_id TEXT NOT NULL REFERENCES roots(id) ON DELETE CASCADE, manager TEXT NOT NULL, view_id TEXT NOT NULL REFERENCES views(id) ON DELETE CASCADE, PRIMARY KEY(root_id,manager));
 CREATE TRIGGER remember_checkout AFTER INSERT ON checkouts BEGIN DELETE FROM forgotten_checkouts WHERE path=NEW.path; END;
 DROP TRIGGER original_agent_update;
 CREATE TRIGGER original_agent_update AFTER UPDATE OF status,native_id ON agent_sessions WHEN NEW.original=1 AND (OLD.status<>NEW.status OR OLD.native_id<>NEW.native_id) BEGIN
 UPDATE sessions SET updated_at=unixepoch(),status_changed_at=CASE WHEN status<>NEW.status THEN unixepoch() ELSE status_changed_at END,status=NEW.status,agent_session_id=NEW.native_id WHERE name=(SELECT name FROM roots WHERE id=NEW.root_id) AND (status<>NEW.status OR agent_session_id<>NEW.native_id);
 END;
 CREATE INDEX inbox_recipient ON inbox(root_id,recipient);
 PRAGMA user_version=2;`)
		if err != nil {
			return err
		}
	}
	if version < 3 {
		_, err = tx.Exec(`
 CREATE TABLE checkout_setups(id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id) ON DELETE CASCADE, plan TEXT NOT NULL);
 CREATE INDEX checkout_setups_root ON checkout_setups(root_id);
 CREATE TABLE root_labels(root_id TEXT PRIMARY KEY REFERENCES roots(id) ON DELETE CASCADE, label TEXT NOT NULL);
 PRAGMA user_version=3;`)
		if err != nil {
			return err
		}
	}
	if version < 4 {
		_, err = tx.Exec(`CREATE TABLE workspace_containers(path TEXT PRIMARY KEY, identity TEXT NOT NULL); CREATE TABLE workspace_homes(root_id TEXT PRIMARY KEY REFERENCES roots(id) ON DELETE CASCADE, record TEXT NOT NULL); PRAGMA user_version=4;`)
		if err != nil {
			return err
		}
	}
	if version < 5 {
		if err = migrateDelegationJobs(tx); err != nil {
			return err
		}
	}
	if version < 6 {
		if err = migrateDurableJobs(tx); err != nil {
			return err
		}
	}
	if err = migrateNativeRecovery(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Worktree(name string) (Worktree, error) {
	w := Worktree{Agents: []AgentSession{}, Checkouts: []Checkout{}, Views: []View{}}
	// Names and IDs remain compatible lookup forms, but must never select an
	// arbitrary root when one root's name is another root's opaque identity.
	var matches int
	var layout string
	err := s.db.QueryRow(`SELECT r.wake_enabled,r.wake_budget,r.agent_first,r.id,r.name,s.workspace_path,r.socket,r.layout,count(*) OVER () FROM roots r JOIN sessions s ON s.name=r.name WHERE r.name=? OR r.id=?`, name, name).Scan(&w.WakeEnabled, &w.WakeBudget, &w.AgentFirst, &w.ID, &w.Name, &w.Cwd, &w.Socket, &layout, &matches)
	if err != nil {
		return w, err
	}
	if matches > 1 {
		return Worktree{}, fmt.Errorf("ambiguous root reference %q matches a name and another root ID; inspect wt-state list and explicitly repair conflicting metadata (no automatic rename)", name)
	}
	if err = json.Unmarshal([]byte(layout), &w.Layout); err != nil {
		return w, err
	}
	rows, err := s.db.Query(`SELECT id,name,root_id,parent,creator,profile,cwd,status,native_id,runtime,stopped,original,adapter FROM agent_sessions WHERE root_id=? ORDER BY original DESC,rowid`, w.ID)
	if err != nil {
		return w, err
	}
	for rows.Next() {
		var a AgentSession
		var adapter string
		err = rows.Scan(&a.ID, &a.Name, &a.RootID, &a.Parent, &a.Creator, &a.Profile, &a.Cwd, &a.Status, &a.NativeID, &a.Runtime, &a.Stopped, &a.Original, &adapter)
		if err == nil {
			err = json.Unmarshal([]byte(adapter), &a.Adapter)
		}
		if err != nil {
			rows.Close()
			return w, err
		}
		w.Agents = append(w.Agents, a)
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT id,root_id,alias,path,ownership FROM checkouts WHERE root_id=? ORDER BY rowid`, w.ID)
	if err != nil {
		return w, err
	}
	for rows.Next() {
		var c Checkout
		if err = rows.Scan(&c.ID, &c.RootID, &c.Alias, &c.Path, &c.Ownership); err != nil {
			rows.Close()
			return w, err
		}
		w.Checkouts = append(w.Checkouts, c)
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT runtime,id,root_id,kind,target,manager,pinned,parked,pane,problem,state FROM views WHERE root_id=? ORDER BY rowid`, w.ID)
	if err != nil {
		return w, err
	}
	for rows.Next() {
		var v View
		var state string
		err = rows.Scan(&v.Runtime, &v.ID, &v.RootID, &v.Kind, &v.Target, &v.Manager, &v.Pinned, &v.Parked, &v.Pane, &v.Problem, &state)
		if err == nil {
			err = json.Unmarshal([]byte(state), &v.State)
		}
		if err != nil {
			rows.Close()
			return w, err
		}
		w.Views = append(w.Views, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return w, err
	}
	r, owned, err := s.workspaceRecord(w.ID)
	if owned {
		w.Workspace = &WorkspaceInfo{Home: r.Home, Scratch: filepath.Join(r.Home, "scratch")}
	}
	return w, err
}
func (w Worktree) agent(id string) (AgentSession, error) {
	for _, a := range w.Agents {
		if a.ID == id || a.Name == id {
			return a, nil
		}
	}
	return AgentSession{}, errors.New("agent not in worktree")
}
func (w Worktree) view(id string) (View, error) {
	for _, v := range w.Views {
		if v.ID == id {
			return v, nil
		}
	}
	return View{}, errors.New("view not in worktree")
}
func (w Worktree) targetPath(target string) (string, error) {
	if target == w.ID || target == "root" {
		return w.Cwd, nil
	}
	for _, c := range w.Checkouts {
		if target == c.ID || target == c.Alias {
			return c.Path, nil
		}
	}
	return "", errors.New("target must be root or an attached checkout ID/alias")
}
func (s *Store) addAgent(w Worktree, name, parent, creator, cwd string) (string, error) {
	if creator != "" {
		a, err := w.agent(creator)
		if err != nil {
			return "", err
		}
		if a.Adapter.Durable != nil && a.Adapter.Durable.Job != "" {
			return "", errors.New("durable task actor cannot create peers or children")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow(`SELECT count(*) FROM agent_sessions WHERE root_id=?`, w.ID).Scan(&count); err != nil {
		return "", err
	}
	if count >= 8 {
		return "", errors.New("worktree limit: 8 agents (including stopped agents)")
	}
	if parent != "" {
		a, e := w.agent(parent)
		if e != nil {
			return "", e
		}
		parent = a.ID
	}
	if creator != "" {
		if _, err = w.agent(creator); err != nil {
			return "", err
		}
	}
	if !safeName.MatchString(name) || name == "human" {
		return "", errors.New("invalid agent name")
	}
	id := newID()
	_, err = tx.Exec(`INSERT INTO agent_sessions(id,name,root_id,parent,creator,cwd) VALUES(?,?,?,?,?,?)`, id, name, w.ID, parent, creator, cwd)
	if err != nil {
		return "", err
	}
	manager := "human"
	if creator != "" {
		manager = creator
	}
	_, err = tx.Exec(`INSERT INTO views(id,root_id,kind,target,manager) VALUES(?,?,'agent',?,?)`, newID(), w.ID, id, manager)
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}
func (s *Store) reparent(w Worktree, id, parent string) error {
	a, err := w.agent(id)
	if err != nil {
		return err
	}
	id = a.ID
	if parent != "" {
		a, err = w.agent(parent)
		if err != nil {
			return err
		}
		parent = a.ID
	}
	for p := parent; p != ""; {
		if p == id {
			return errors.New("parent cycle")
		}
		a, err := w.agent(p)
		if err != nil {
			return err
		}
		p = a.Parent
	}
	_, err = s.db.Exec(`UPDATE agent_sessions SET parent=? WHERE root_id=? AND id=?`, parent, w.ID, id)
	return err
}
func (s *Store) updateAgent(root, id, runtime, status, native string, adapter *PiSnapshot) error {
	if root == "" || id == "" || runtime == "" {
		return errors.New("root, agent and runtime identities required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldNative string
	var oldAdapter string
	if err = tx.QueryRow(`SELECT native_id,adapter FROM agent_sessions WHERE root_id=? AND id=? AND runtime=? AND stopped=0`, root, id, runtime).Scan(&oldNative, &oldAdapter); err != nil {
		return errors.New("stale or unknown agent runtime")
	}
	if native == "" {
		native = oldNative
	}
	if err = checkDelegationNativeUpdate(tx, root, id, native, adapter); err != nil {
		return err
	}
	var previous PiSnapshot
	if err = json.Unmarshal([]byte(oldAdapter), &previous); err != nil {
		return err
	}
	if previous.Backend == "durable" && (adapter != nil || native != oldNative) {
		return errors.New("durable identity cannot be changed through the native checkpoint adapter")
	}
	if adapter != nil {
		if adapter.Backend != "" && adapter.Backend != "native" {
			return errors.New("native checkpoint cannot select another backend")
		}
		if adapter.Version != 1 {
			return errors.New("unsupported Pi adapter version")
		}
		oldAdapter = jsonText(adapter)
	}
	_, err = tx.Exec(`UPDATE agent_sessions SET status=?,native_id=?,adapter=? WHERE root_id=? AND id=?`, status, native, oldAdapter, root, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) send(w Worktree, id, sender, recipient, body string, request ...bool) (InboxMessage, error) {
	kind := "agent"
	var senderDB any
	if sender == "human" {
		kind = "human"
	} else {
		a, err := w.agent(sender)
		if err != nil {
			return InboxMessage{}, err
		}
		sender = a.ID
		senderDB = sender
	}
	a, err := w.agent(recipient)
	if err != nil {
		return InboxMessage{}, err
	}
	recipient = a.ID
	wake := true
	if len(request) > 0 {
		wake = request[0]
	}
	m := InboxMessage{ID: id, RootID: w.ID, Sender: sender, SenderKind: kind, Request: wake, Recipient: recipient, Body: body, State: "pending"}
	if !safeName.MatchString(id) || len(body) == 0 || len(body) > 16384 {
		return m, errors.New("message requires safe dedup ID and 1..16384 bytes")
	}

	if _, err := w.agent(recipient); err != nil {
		return m, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	// Human CLI may explicitly attribute a message; authenticated agents must
	// remain live through the write, not merely through command parsing.
	if os.Getenv("WT_AGENT_ID") != "" {
		if sender != os.Getenv("WT_AGENT_ID") || w.ID != os.Getenv("WT_ROOT_ID") {
			return m, errors.New("sender identity mismatch")
		}
		if err = liveActorTx(tx, w.ID, sender, os.Getenv("WT_RUNTIME_ID")); err != nil {
			return m, err
		}
	}
	_, err = tx.Exec(`INSERT INTO inbox(id,root_id,sender,sender_kind,request,recipient,body) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, id, w.ID, senderDB, kind, wake, recipient, body)
	if err != nil {
		return m, err
	}
	var got InboxMessage
	err = tx.QueryRow(`SELECT sender_kind,request,id,root_id,coalesce(sender,'human'),recipient,body,state,runtime FROM inbox WHERE id=?`, id).Scan(&got.SenderKind, &got.Request, &got.ID, &got.RootID, &got.Sender, &got.Recipient, &got.Body, &got.State, &got.Runtime)
	if err != nil {
		return m, err
	}
	if got.RootID != w.ID || got.Recipient != recipient || got.Body != body || got.Sender != sender || got.Request != wake {
		return m, errors.New("dedup ID reused for different message")
	}
	return got, tx.Commit()
}
func (s *Store) messages(root, recipient string) ([]InboxMessage, error) {
	out := []InboxMessage{}
	rows, err := s.db.Query(`SELECT sender_kind,request,id,root_id,coalesce(sender,'human'),recipient,body,state,runtime FROM inbox WHERE root_id=? AND recipient=? ORDER BY rowid`, root, recipient)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m InboxMessage
		if err = rows.Scan(&m.SenderKind, &m.Request, &m.ID, &m.RootID, &m.Sender, &m.Recipient, &m.Body, &m.State, &m.Runtime); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) receipt(root, agent, runtime, id, action string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow(`SELECT count(*) FROM agent_sessions WHERE root_id=? AND id=? AND runtime=? AND runtime<>'' AND stopped=0`, root, agent, runtime).Scan(&n); err != nil {
		return err
	}
	if n != 1 {
		return errors.New("stale recipient runtime")
	}
	var res sql.Result
	switch action {
	case "claim":
		var request bool
		if err = tx.QueryRow(`SELECT request FROM inbox WHERE root_id=? AND recipient=? AND id=? AND state='pending'`, root, agent, id).Scan(&request); err != nil {
			return errors.New("message not pending")
		}
		if request {
			budget, err := tx.Exec(`UPDATE roots SET wake_budget=wake_budget-1 WHERE id=? AND wake_enabled=1 AND wake_budget>0`, root)
			if err != nil {
				return err
			}
			n, _ := budget.RowsAffected()
			if n != 1 {
				return errors.New("automatic wake disabled or budget exhausted")
			}
		}
		res, err = tx.Exec(`UPDATE inbox SET state='claimed',runtime=? WHERE root_id=? AND recipient=? AND id=? AND state='pending'`, runtime, root, agent, id)
	case "ack":
		res, err = tx.Exec(`UPDATE inbox SET state='delivered' WHERE root_id=? AND recipient=? AND id=? AND state IN ('pending','claimed','uncertain')`, root, agent, id)
	default:
		return errors.New("unknown receipt action")
	}
	if err != nil {
		return err
	}
	n64, _ := res.RowsAffected()
	if n64 != 1 {
		return errors.New("message not eligible for receipt")
	}
	return tx.Commit()
}
func canonicalDir(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", errors.New("not a directory")
	}
	return p, nil
}
