package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Session is one tracked wt session. JSON tags match the legacy .status field
// names so bash consumers can parse rows with jq using the same keys.
type Session struct {
	Name            string `json:"name"`
	Status          string `json:"status"`
	Message         string `json:"message"`
	Repo            string `json:"repo"`
	Branch          string `json:"branch"`
	WtPath          string `json:"wt_path"`
	PR              string `json:"pr"`
	Agent           string `json:"agent"`
	OpencodeConfig  string `json:"opencode_config"`
	IsMaster        bool   `json:"is_master"`
	Kind            string `json:"kind"`
	WorkspacePath   string `json:"workspace_path"`
	UpdatedAt       int64  `json:"updated_at"`
	StatusChangedAt int64  `json:"status_changed_at"`
	// PRState caches the GitHub PR state for this session's branch
	// (merged/open/closed/draft/none/""), refreshed by `wt` on a TTL so the
	// list surfaces don't hit `gh` on every render. PRStateCheckedAt is the
	// unix time of the last refresh; it auto-stamps whenever pr_state is set.
	PRState          string `json:"pr_state"`
	PRStateCheckedAt int64  `json:"pr_state_checked_at"`
	// AgentSessionID is the agent CLI's own native session/conversation id,
	// captured by wt-hook on session start. It lets `wt` resume the exact prior
	// conversation on revive (e.g. `claude --resume <id>`) rather than relying
	// on the agent's cwd-latest fallback. Empty when uncaptured/unsupported.
	AgentSessionID string `json:"agent_session_id"`
}

// column names that `set` and `get --field` accept, in a stable order.
var columns = []string{
	"status", "message", "repo", "branch", "wt_path",
	"pr", "agent", "opencode_config", "is_master", "kind", "workspace_path",
	"updated_at", "status_changed_at",
	"pr_state", "pr_state_checked_at",
	"agent_session_id",
}

type Store struct {
	db  *sql.DB
	now func() int64 // injectable for tests
}

// Open opens (creating if needed) the SQLite database at path with WAL and
// immediate write-locking so concurrent hook processes can't deadlock or lose
// updates during read-modify-write.
func Open(path string) (*Store, error) {
	dsn := "file:" + url.PathEscape(path) + "?" + strings.Join([]string{
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(ON)",
		"_txlock=immediate",
	}, "&")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: WAL + busy_timeout serialize across processes; a single
	// in-process conn avoids self-contention and keeps txlock semantics simple.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, now: func() int64 { return time.Now().Unix() }}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) init() error {
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS sessions (
  name              TEXT PRIMARY KEY,
  status            TEXT    NOT NULL DEFAULT 'unknown',
  message           TEXT    NOT NULL DEFAULT '',
  repo              TEXT    NOT NULL DEFAULT '',
  branch            TEXT    NOT NULL DEFAULT '',
  wt_path           TEXT    NOT NULL DEFAULT '',
  pr                TEXT    NOT NULL DEFAULT '',
  agent             TEXT    NOT NULL DEFAULT '',
  opencode_config   TEXT    NOT NULL DEFAULT '',
  is_master         INTEGER NOT NULL DEFAULT 0,
  kind              TEXT    NOT NULL DEFAULT 'worktree',
  workspace_path    TEXT    NOT NULL DEFAULT '',
  updated_at        INTEGER NOT NULL DEFAULT 0,
  status_changed_at INTEGER NOT NULL DEFAULT 0,
  pr_state            TEXT    NOT NULL DEFAULT '',
  pr_state_checked_at INTEGER NOT NULL DEFAULT 0,
  agent_session_id    TEXT    NOT NULL DEFAULT ''
);`); err != nil {
		return err
	}

	// Columns added after the initial schema: CREATE TABLE IF NOT EXISTS won't
	// alter an existing DB, and SQLite has no "ADD COLUMN IF NOT EXISTS", so
	// run each ALTER and ignore only the duplicate-column error.
	for _, stmt := range []string{
		`ALTER TABLE sessions ADD COLUMN pr_state TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN pr_state_checked_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'worktree'`,
		`ALTER TABLE sessions ADD COLUMN workspace_path TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil &&
			!strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	// Backfill the new scope fields without requiring callers to rewrite every
	// existing row. A normal worktree's workspace is its checkout today; master
	// remains the one legacy row whose path was not previously persisted.
	if _, err := s.db.Exec(`
UPDATE sessions
SET kind = CASE WHEN is_master = 1 THEN 'master' ELSE 'worktree' END
WHERE kind = '' OR (is_master = 1 AND kind = 'worktree');
UPDATE sessions
SET workspace_path = wt_path
WHERE workspace_path = '' AND wt_path <> '';`); err != nil {
		return err
	}
	return nil
}

const selectCols = `name, status, message, repo, branch, wt_path, pr, agent,
	opencode_config, is_master, kind, workspace_path, updated_at, status_changed_at,
	pr_state, pr_state_checked_at, agent_session_id`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var s Session
	err := row.Scan(&s.Name, &s.Status, &s.Message, &s.Repo, &s.Branch,
		&s.WtPath, &s.PR, &s.Agent, &s.OpencodeConfig, &s.IsMaster,
		&s.Kind, &s.WorkspacePath, &s.UpdatedAt, &s.StatusChangedAt,
		&s.PRState, &s.PRStateCheckedAt, &s.AgentSessionID)
	return s, err
}

// Get returns the session by name. ok is false if no such row exists.
func (s *Store) Get(name string) (Session, bool, error) {
	row := s.db.QueryRow(`SELECT `+selectCols+` FROM sessions WHERE name = ?`, name)
	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	return sess, true, nil
}

// Set upserts a session. Only the columns present in fields are changed; all
// others are preserved. updated_at is always bumped; status_changed_at is
// bumped only when "status" is provided and differs from the current value.
// The full resulting row is returned.
func (s *Store) Set(name string, fields map[string]string) (Session, error) {
	tx, err := s.db.Begin() // IMMEDIATE via _txlock: takes write lock now
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()

	cur, err := scanSession(tx.QueryRow(`SELECT `+selectCols+` FROM sessions WHERE name = ?`, name))
	existed := true
	if err == sql.ErrNoRows {
		existed = false
		cur = Session{Name: name, Status: "unknown", Kind: "worktree"}
	} else if err != nil {
		return Session{}, err
	}

	now := s.now()
	statusChanged := false
	for k, v := range fields {
		switch k {
		case "status":
			if !existed || v != cur.Status {
				statusChanged = true
			}
			cur.Status = v
		case "message":
			cur.Message = v
		case "repo":
			cur.Repo = v
		case "branch":
			cur.Branch = v
		case "wt_path":
			cur.WtPath = v
		case "pr":
			cur.PR = v
		case "agent":
			cur.Agent = v
		case "opencode_config":
			cur.OpencodeConfig = v
		case "is_master":
			cur.IsMaster = v == "1" || strings.EqualFold(v, "true")
		case "kind":
			switch v {
			case "worktree", "workspace", "master":
				cur.Kind = v
			default:
				return Session{}, fmt.Errorf("invalid session kind %q", v)
			}
		case "workspace_path":
			cur.WorkspacePath = v
		case "pr_state":
			// pr_state_checked_at auto-stamps on every write, even when the
			// state is unchanged, so a TTL refresh that re-confirms the same
			// value still pushes the next check out.
			cur.PRState = v
			cur.PRStateCheckedAt = now
		case "agent_session_id":
			cur.AgentSessionID = v
		default:
			return Session{}, fmt.Errorf("unknown field %q", k)
		}
	}
	// Keep the legacy master bit and the new kind coherent while both APIs are
	// supported. Callers that only know is_master continue to produce a master;
	// callers using kind=master continue to work with old list/count filters.
	_, kindProvided := fields["kind"]
	if _, masterProvided := fields["is_master"]; masterProvided && !kindProvided {
		if cur.IsMaster {
			cur.Kind = "master"
		} else if cur.Kind == "master" {
			cur.Kind = "worktree"
		}
	}
	if kindProvided {
		cur.IsMaster = cur.Kind == "master"
	}
	if cur.Kind == "" {
		cur.Kind = "worktree"
	}
	// A worktree session's checkout is also its workspace in the legacy model.
	// Explicit workspace_path always wins, including an intentionally empty one.
	if _, provided := fields["workspace_path"]; !provided && cur.WorkspacePath == "" && cur.WtPath != "" {
		cur.WorkspacePath = cur.WtPath
	}

	cur.Name = name
	cur.UpdatedAt = now
	if statusChanged || cur.StatusChangedAt == 0 {
		cur.StatusChangedAt = now
	}

	_, err = tx.Exec(`
INSERT INTO sessions (name, status, message, repo, branch, wt_path, pr, agent,
	opencode_config, is_master, kind, workspace_path, updated_at, status_changed_at,
	pr_state, pr_state_checked_at, agent_session_id)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET
	status=excluded.status, message=excluded.message, repo=excluded.repo,
	branch=excluded.branch, wt_path=excluded.wt_path, pr=excluded.pr,
	agent=excluded.agent, opencode_config=excluded.opencode_config,
	is_master=excluded.is_master, kind=excluded.kind,
	workspace_path=excluded.workspace_path, updated_at=excluded.updated_at,
	status_changed_at=excluded.status_changed_at,
	pr_state=excluded.pr_state, pr_state_checked_at=excluded.pr_state_checked_at,
	agent_session_id=excluded.agent_session_id`,
		cur.Name, cur.Status, cur.Message, cur.Repo, cur.Branch, cur.WtPath,
		cur.PR, cur.Agent, cur.OpencodeConfig, b2i(cur.IsMaster),
		cur.Kind, cur.WorkspacePath, cur.UpdatedAt, cur.StatusChangedAt,
		cur.PRState, cur.PRStateCheckedAt, cur.AgentSessionID)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return cur, nil
}

// List returns all sessions. master controls inclusion of the master row:
// "all" (default), "only", or "exclude". sort controls ordering: "recency"
// (default) puts the most-recently-active session first (updated_at DESC), with
// name as a stable tie-break; "name" sorts alphabetically. This is the single
// ranking used by every wt surface (wt ls, wt pick, the switcher), so they all
// agree on order.
func (s *Store) List(master, sort string) ([]Session, error) {
	q := `SELECT ` + selectCols + ` FROM sessions`
	switch master {
	case "only":
		q += ` WHERE is_master = 1`
	case "exclude":
		q += ` WHERE is_master = 0`
	}
	switch sort {
	case "name":
		q += ` ORDER BY name`
	default: // "recency"
		q += ` ORDER BY updated_at DESC, name ASC`
	}

	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// Counts tallies non-master sessions by status. Keys are status strings; only
// the four tracked statuses are guaranteed present (zeroed if absent).
func (s *Store) Counts() (map[string]int, error) {
	counts := map[string]int{"working": 0, "idle": 0, "input": 0, "error": 0}
	rows, err := s.db.Query(
		`SELECT status, COUNT(*) FROM sessions WHERE is_master = 0 GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// Delete removes a session. Returns whether a row was deleted.
func (s *Store) Delete(name string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE name = ?`, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
