package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// newStore opens a fresh DB in a temp dir with a controllable clock.
func newStore(t *testing.T) (*Store, *int64) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var clock int64 = 1000
	st.now = func() int64 { return clock }
	return st, &clock
}

func TestSetPreservesUnsetFields(t *testing.T) {
	st, _ := newStore(t)

	if _, err := st.Set("s", map[string]string{
		"status": "working", "repo": "myrepo", "branch": "feat", "agent": "claude",
	}); err != nil {
		t.Fatal(err)
	}
	// A status-only update must not wipe repo/branch/agent.
	got, err := st.Set("s", map[string]string{"status": "idle"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "idle" {
		t.Errorf("status = %q, want idle", got.Status)
	}
	if got.Repo != "myrepo" || got.Branch != "feat" || got.Agent != "claude" {
		t.Errorf("unset fields not preserved: %+v", got)
	}
}

func TestAgentSessionIDRoundTrips(t *testing.T) {
	st, _ := newStore(t)

	got, err := st.Set("s", map[string]string{"agent_session_id": "abc-123"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentSessionID != "abc-123" {
		t.Fatalf("agent_session_id = %q, want abc-123", got.AgentSessionID)
	}
	// A status update must preserve the captured id (revive reads it later).
	after, _ := st.Set("s", map[string]string{"status": "offline"})
	if after.AgentSessionID != "abc-123" {
		t.Errorf("agent_session_id not preserved: %q", after.AgentSessionID)
	}
}

func TestSessionScopeRoundTrips(t *testing.T) {
	st, _ := newStore(t)

	// Legacy worktree callers only set wt_path; the scope defaults should make
	// that checkout the session workspace without requiring a coordinated write.
	worktree, err := st.Set("repo-feature", map[string]string{"wt_path": "/worktrees/repo/feature"})
	if err != nil {
		t.Fatal(err)
	}
	if worktree.Kind != "worktree" || worktree.WorkspacePath != worktree.WtPath {
		t.Fatalf("worktree scope = kind %q workspace %q, want worktree/%q",
			worktree.Kind, worktree.WorkspacePath, worktree.WtPath)
	}

	workspace, err := st.Set("investigate-auth", map[string]string{
		"kind": "workspace", "workspace_path": "/workspaces/investigate-auth",
	})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Kind != "workspace" || workspace.WorkspacePath != "/workspaces/investigate-auth" || workspace.IsMaster {
		t.Fatalf("workspace scope wrong: %+v", workspace)
	}

	// Old and new callers can each describe a master while is_master remains
	// available for the existing list/count filters.
	legacyMaster, err := st.Set("legacy-master", map[string]string{"is_master": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if legacyMaster.Kind != "master" || !legacyMaster.IsMaster {
		t.Fatalf("legacy master scope wrong: %+v", legacyMaster)
	}
	newMaster, err := st.Set("new-master", map[string]string{"kind": "master"})
	if err != nil {
		t.Fatal(err)
	}
	if newMaster.Kind != "master" || !newMaster.IsMaster {
		t.Fatalf("new master scope wrong: %+v", newMaster)
	}

	if _, err := st.Set("bad", map[string]string{"kind": "repository"}); err == nil {
		t.Fatal("invalid session kind was accepted")
	}
}

func TestOpenBackfillsSessionScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE sessions (
  name TEXT PRIMARY KEY, status TEXT NOT NULL DEFAULT 'unknown',
  message TEXT NOT NULL DEFAULT '', repo TEXT NOT NULL DEFAULT '',
  branch TEXT NOT NULL DEFAULT '', wt_path TEXT NOT NULL DEFAULT '',
  pr TEXT NOT NULL DEFAULT '', agent TEXT NOT NULL DEFAULT '',
  opencode_config TEXT NOT NULL DEFAULT '', is_master INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0, status_changed_at INTEGER NOT NULL DEFAULT 0,
  pr_state TEXT NOT NULL DEFAULT '', pr_state_checked_at INTEGER NOT NULL DEFAULT 0,
  agent_session_id TEXT NOT NULL DEFAULT ''
);
INSERT INTO sessions (name, wt_path) VALUES ('repo-feature', '/worktrees/repo/feature');
INSERT INTO sessions (name, is_master) VALUES ('wt-master', 1);`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	worktree, ok, err := st.Get("repo-feature")
	if err != nil || !ok {
		t.Fatalf("get worktree = %v, %v", ok, err)
	}
	if worktree.Kind != "worktree" || worktree.WorkspacePath != worktree.WtPath {
		t.Errorf("backfilled worktree scope wrong: %+v", worktree)
	}
	master, ok, err := st.Get("wt-master")
	if err != nil || !ok {
		t.Fatalf("get master = %v, %v", ok, err)
	}
	if master.Kind != "master" || !master.IsMaster {
		t.Errorf("backfilled master scope wrong: %+v", master)
	}
}

func TestStatusChangedAtOnlyOnChange(t *testing.T) {
	st, clock := newStore(t)

	*clock = 100
	first, _ := st.Set("s", map[string]string{"status": "working"})
	if first.StatusChangedAt != 100 || first.UpdatedAt != 100 {
		t.Fatalf("initial timestamps = %+v", first)
	}

	// Same status, new message: updated_at moves, status_changed_at frozen.
	*clock = 200
	same, _ := st.Set("s", map[string]string{"status": "working", "message": "x"})
	if same.UpdatedAt != 200 {
		t.Errorf("updated_at = %d, want 200", same.UpdatedAt)
	}
	if same.StatusChangedAt != 100 {
		t.Errorf("status_changed_at = %d, want 100 (unchanged)", same.StatusChangedAt)
	}

	// Status actually changes: status_changed_at advances.
	*clock = 300
	changed, _ := st.Set("s", map[string]string{"status": "idle"})
	if changed.StatusChangedAt != 300 {
		t.Errorf("status_changed_at = %d, want 300", changed.StatusChangedAt)
	}

	// Metadata-only update (no status flag at all): status_changed_at frozen.
	*clock = 400
	meta, _ := st.Set("s", map[string]string{"repo": "r"})
	if meta.StatusChangedAt != 300 || meta.UpdatedAt != 400 {
		t.Errorf("metadata-only update timestamps = %+v", meta)
	}
}

func TestPRStateStampsCheckedAt(t *testing.T) {
	st, clock := newStore(t)

	// Setting pr_state stamps pr_state_checked_at to now.
	*clock = 500
	got, _ := st.Set("s", map[string]string{"pr_state": "open"})
	if got.PRState != "open" || got.PRStateCheckedAt != 500 {
		t.Fatalf("pr_state=%q checked_at=%d, want open/500", got.PRState, got.PRStateCheckedAt)
	}

	// Re-confirming the same state still advances checked_at (pushes TTL out).
	*clock = 900
	same, _ := st.Set("s", map[string]string{"pr_state": "open"})
	if same.PRStateCheckedAt != 900 {
		t.Errorf("checked_at = %d, want 900 on re-confirm", same.PRStateCheckedAt)
	}

	// An unrelated update must not touch pr_state or its timestamp.
	*clock = 1000
	other, _ := st.Set("s", map[string]string{"status": "idle"})
	if other.PRState != "open" || other.PRStateCheckedAt != 900 {
		t.Errorf("pr_state fields not preserved: %q %d", other.PRState, other.PRStateCheckedAt)
	}
}

func TestCountsExcludesMaster(t *testing.T) {
	st, _ := newStore(t)
	st.Set("a", map[string]string{"status": "working"})
	st.Set("b", map[string]string{"status": "working"})
	st.Set("c", map[string]string{"status": "idle"})
	st.Set("wt-master", map[string]string{"status": "working", "is_master": "1"})

	counts, err := st.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["working"] != 2 {
		t.Errorf("working = %d, want 2 (master excluded)", counts["working"])
	}
	if counts["idle"] != 1 {
		t.Errorf("idle = %d, want 1", counts["idle"])
	}
	if counts["error"] != 0 || counts["input"] != 0 {
		t.Errorf("absent statuses should be 0, got %+v", counts)
	}
}

func TestDelete(t *testing.T) {
	st, _ := newStore(t)
	st.Set("s", map[string]string{"status": "idle"})

	deleted, err := st.Delete("s")
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v", deleted, err)
	}
	if _, ok, _ := st.Get("s"); ok {
		t.Error("row still present after delete")
	}
	// Deleting a missing row is not an error.
	again, err := st.Delete("s")
	if err != nil || again {
		t.Errorf("second Delete = %v, %v; want false, nil", again, err)
	}
}

func TestMigrateUnescapesLegacy(t *testing.T) {
	dir := t.TempDir()
	// Mirror bin/wt status_escape: spaces -> "\ ", backslash -> "\\".
	// "opencode finished" => "opencode\ finished".
	content := "status=idle\n" +
		"message=opencode\\ finished\n" +
		"repo=wt\n" +
		"branch=aidan/install-wt-menu-conf\n" +
		"wt_path=/home/aidan/src/wt\n" +
		"agent=opencode\n"
	if err := os.WriteFile(filepath.Join(dir, "wt-main.status"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	st, _ := newStore(t)
	n, err := st.Migrate(dir)
	if err != nil || n != 1 {
		t.Fatalf("Migrate = %d, %v; want 1, nil", n, err)
	}
	got, ok, _ := st.Get("wt-main")
	if !ok {
		t.Fatal("migrated row missing")
	}
	if got.Message != "opencode finished" {
		t.Errorf("message = %q, want %q (unescaped)", got.Message, "opencode finished")
	}
	if got.Branch != "aidan/install-wt-menu-conf" || got.Status != "idle" {
		t.Errorf("migrated fields wrong: %+v", got)
	}

	// Idempotent: a second migrate upserts without error or duplication.
	if _, err := st.Migrate(dir); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	all, _ := st.List("all", "recency")
	if len(all) != 1 {
		t.Errorf("expected 1 row after re-migrate, got %d", len(all))
	}
}

func TestListRecencyOrder(t *testing.T) {
	st, clock := newStore(t)

	// Write three sessions at increasing times, out of name order.
	*clock = 100
	st.Set("bravo", map[string]string{"status": "idle"})
	*clock = 300
	st.Set("alpha", map[string]string{"status": "idle"})
	*clock = 200
	st.Set("charlie", map[string]string{"status": "idle"})

	// recency: most-recently-updated first, regardless of name.
	rec, _ := st.List("all", "recency")
	gotRec := []string{rec[0].Name, rec[1].Name, rec[2].Name}
	wantRec := []string{"alpha", "charlie", "bravo"}
	for i := range wantRec {
		if gotRec[i] != wantRec[i] {
			t.Fatalf("recency order = %v, want %v", gotRec, wantRec)
		}
	}

	// name: alphabetical.
	byName, _ := st.List("all", "name")
	if byName[0].Name != "alpha" || byName[1].Name != "bravo" || byName[2].Name != "charlie" {
		t.Fatalf("name order = %v", []string{byName[0].Name, byName[1].Name, byName[2].Name})
	}

	// Tie-break: equal updated_at falls back to name ASC.
	*clock = 500
	st.Set("zulu", map[string]string{"status": "idle"})
	st.Set("delta", map[string]string{"status": "idle"})
	tie, _ := st.List("all", "recency")
	if tie[0].Name != "delta" || tie[1].Name != "zulu" {
		t.Fatalf("tie-break order = %v, want delta before zulu", []string{tie[0].Name, tie[1].Name})
	}
}

func TestUnescapeOrder(t *testing.T) {
	// Literal backslash-then-space "a\b c" escapes to "a\\b\ c" and must
	// round-trip back exactly.
	if got := statusUnescape(`a\\b\ c`); got != `a\b c` {
		t.Errorf("statusUnescape = %q, want %q", got, `a\b c`)
	}
}
