package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func worktreeTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wt.db")
	t.Setenv("WT_DB", path)
	for _, key := range []string{"WT_AGENT_ID", "WT_ROOT_ID", "WT_RUNTIME_ID"} {
		t.Setenv(key, "")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func testRoot(t *testing.T, s *Store, name string) Worktree {
	t.Helper()
	_, err := s.Set(name, map[string]string{"agent": "pi", "workspace_path": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Worktree(name)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func TestWorktreeMigrationLegacyAndRepeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// Original pre-agent-first schema, including fields required by the old store.
	_, err = db.Exec(`CREATE TABLE sessions(name TEXT PRIMARY KEY,status TEXT NOT NULL DEFAULT 'unknown',message TEXT NOT NULL DEFAULT '',repo TEXT NOT NULL DEFAULT '',branch TEXT NOT NULL DEFAULT '',wt_path TEXT NOT NULL DEFAULT '',pr TEXT NOT NULL DEFAULT '',agent TEXT NOT NULL DEFAULT '',opencode_config TEXT NOT NULL DEFAULT '',is_master INTEGER NOT NULL DEFAULT 0,updated_at INTEGER NOT NULL DEFAULT 0,status_changed_at INTEGER NOT NULL DEFAULT 0,agent_session_id TEXT NOT NULL DEFAULT ''); INSERT INTO sessions(name,status,message,repo,branch,wt_path,pr,agent,opencode_config,agent_session_id) VALUES('old','working','keep','repo','feature','/borrowed','77','pi','/config','native-exact');`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old, _, _ := s.Get("old")
	w, err := s.Worktree("old")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Agents) != 1 || w.Agents[0].NativeID != "native-exact" || w.Agents[0].Cwd != "/borrowed" || len(w.Checkouts) != 1 || w.Checkouts[0].Ownership != "borrowed" || len(w.Views) != 1 {
		t.Fatalf("bad migration: %+v", w)
	}
	if old.PR != "77" || old.Message != "keep" || old.OpencodeConfig != "/config" || old.Branch != "feature" {
		t.Fatalf("legacy metadata lost: %+v", old)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	again, err := s.Worktree("old")
	if err != nil || !reflect.DeepEqual(w, again) {
		t.Fatalf("migration not stable: %v %+v", err, again)
	}
}
func TestWorktreePerAgentFencingLegacyTranslationAndScope(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "one")
	other := testRoot(t, s, "two")
	id, err := s.addAgent(w, "reviewer", w.Agents[0].ID, "", w.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	if _, err = s.db.Exec(`UPDATE agent_sessions SET runtime='current' WHERE root_id=?`, w.ID); err != nil {
		t.Fatal(err)
	}
	adapter := PiSnapshot{Version: 1, File: "/exact/native.jsonl", Persisted: true, Leaf: "leaf"}
	if err = s.updateAgent(w.ID, id, "current", "working", "peer-native", &adapter); err != nil {
		t.Fatal(err)
	}
	if err = s.updateAgent(w.ID, id, "stale", "error", "wrong", nil); err == nil {
		t.Fatal("stale runtime accepted")
	}
	if err = s.updateAgent(other.ID, id, "current", "error", "wrong", nil); err == nil {
		t.Fatal("cross-root runtime accepted")
	}
	if _, err = s.Set(w.Name, map[string]string{"status": "input", "agent_session_id": "original-native"}); err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	peer, _ := w.agent(id)
	if peer.Status != "working" || peer.NativeID != "peer-native" || w.Agents[0].NativeID != "original-native" {
		t.Fatalf("legacy clobbered peer: %+v", w.Agents)
	}
	if err = s.reparent(w, w.Agents[0].ID, id); err == nil {
		t.Fatal("cycle accepted")
	}
	if err = s.reparent(w, id, other.Agents[0].ID); err == nil {
		t.Fatal("cross-root parent accepted")
	}
	if err = s.reparent(w, id, ""); err != nil {
		t.Fatal(err)
	}
	for i := len(w.Agents); i < 8; i++ {
		w, _ = s.Worktree(w.ID)
		if _, err = s.addAgent(w, string(rune('a'+i)), "", "", w.Cwd); err != nil {
			t.Fatal(err)
		}
	}
	w, _ = s.Worktree(w.ID)
	if _, err = s.addAgent(w, "overflow", "", "", w.Cwd); err == nil {
		t.Fatal("spawn limit not enforced")
	}
}
func TestWorktreeDurableInboxDedupAndUncertainty(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "inbox")
	id, err := s.addAgent(w, "peer", "", "", w.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	m, err := s.send(w, "dedup", w.Agents[0].ID, id, "hello")
	if err != nil {
		t.Fatal(err)
	}
	m2, err := s.send(w, "dedup", w.Agents[0].ID, id, "hello")
	if err != nil || m != m2 {
		t.Fatal("dedup failed")
	}
	if _, err = s.send(w, "dedup", w.Agents[0].ID, id, "different"); err == nil {
		t.Fatal("dedup collision accepted")
	}
	s.db.Exec(`UPDATE agent_sessions SET runtime='a' WHERE id=?`, id)
	if err = s.receipt(w.ID, id, "a", "dedup", "claim"); err != nil {
		t.Fatal(err)
	}
	if err = s.receipt(w.ID, id, "a", "dedup", "claim"); err == nil {
		t.Fatal("duplicate claim")
	}
	s.db.Exec(`UPDATE agent_sessions SET runtime='b' WHERE id=?`, id)
	s.db.Exec(`UPDATE inbox SET state='uncertain' WHERE state='claimed'`)
	if err = s.receipt(w.ID, id, "a", "dedup", "ack"); err == nil {
		t.Fatal("stale receipt accepted")
	}
	if err = s.receipt(w.ID, id, "b", "dedup", "claim"); err == nil {
		t.Fatal("uncertain automatically redelivered")
	}
	if err = s.receipt(w.ID, id, "b", "dedup", "ack"); err != nil {
		t.Fatal(err)
	}
	rows, _ := s.messages(w.ID, id)
	if len(rows) != 1 || rows[0].State != "delivered" {
		t.Fatal(rows)
	}
}
func TestStrictPiArgsMissingBlankLeafAndNoFallback(t *testing.T) {
	cwd := t.TempDir()
	a := AgentSession{Profile: "pi", Cwd: cwd, Adapter: PiSnapshot{Version: 1}}
	args, err := strictPiArgs(a)
	if err != nil || !reflect.DeepEqual(args, []string{"pi"}) {
		t.Fatal(args, err)
	}
	a.NativeID = "native"
	if _, err = strictPiArgs(a); err == nil {
		t.Fatal("unpersisted blank silently replaced")
	}
	a.Adapter = PiSnapshot{Version: 1, File: filepath.Join(cwd, "exact.jsonl"), Persisted: true, Leaf: "leaf"}
	if _, err = strictPiArgs(a); err == nil {
		t.Fatal("missing transcript fallback")
	}
	os.WriteFile(a.Adapter.File, []byte("{\"type\":\"session\",\"version\":3,\"id\":\"native\"}\n{\"id\":\"leaf\",\"type\":\"message\"}\n"), 0600)
	args, err = strictPiArgs(a)
	if err != nil || len(args) != 3 || args[1] != "--session" || args[2] != a.Adapter.File {
		t.Fatal(args, err)
	}
	a.Adapter.Leaf = ""
	if _, err = strictPiArgs(a); err == nil {
		t.Fatal("selected empty/root branch silently restored durable tail")
	}
	a.Adapter.Leaf = "memory-only"
	if _, err = strictPiArgs(a); err == nil {
		t.Fatal("memory-only branch accepted")
	}
	a.Profile = "claude"
	if _, err = strictPiArgs(a); err == nil {
		t.Fatal("unsupported restore fallback")
	}
}
func TestWorktreeBorrowedPathsAndLocks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("keep"), 0600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	if _, err := containedPath(root, "escape/secret"); err == nil {
		t.Fatal("symlink escape accepted")
	}
	lockPath := filepath.Join(root, "lock")
	lock, err := lockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lockFile(lockPath); err == nil {
		t.Fatal("concurrent writer accepted")
	}
	lock.Close()
	s := worktreeTestStore(t)
	w := testRoot(t, s, "borrowed")
	s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?)`, newID(), w.ID, "external", outside)
	s.Delete(w.Name)
	if b, err := os.ReadFile(filepath.Join(outside, "secret")); err != nil || strings.TrimSpace(string(b)) != "keep" {
		t.Fatal("borrowed resource removed")
	}
}

func TestWorktreeWakeBudgetHumanAttributionAndStoppedRecipient(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "budget")
	id, err := s.addAgent(w, "reviewer", "main", "", w.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	m, err := s.send(w, "human-task", "human", "reviewer", "do work")
	if err != nil || m.SenderKind != "human" || m.Recipient != id {
		t.Fatal(m, err)
	}
	s.db.Exec(`UPDATE agent_sessions SET runtime='r' WHERE id=?`, id)
	s.db.Exec(`UPDATE roots SET wake_budget=1 WHERE id=?`, w.ID)
	if err = s.receipt(w.ID, id, "r", m.ID, "claim"); err != nil {
		t.Fatal(err)
	}
	if err = s.receipt(w.ID, id, "r", m.ID, "claim"); err == nil {
		t.Fatal("duplicate claim")
	}
	w, _ = s.Worktree(w.ID)
	if w.WakeBudget != 0 {
		t.Fatal("budget did not decrement exactly once")
	}
	s.send(w, "exhausted", "main", "reviewer", "more work")
	if err = s.receipt(w.ID, id, "r", "exhausted", "claim"); err == nil {
		t.Fatal("exhausted budget accepted")
	}
	s.send(w, "notification", "main", "reviewer", "information", false)
	if err = s.receipt(w.ID, id, "r", "notification", "claim"); err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE agent_sessions SET stopped=1 WHERE id=?`, id)
	s.send(w, "stopped", "human", "reviewer", "wait")
	if err = s.receipt(w.ID, id, "r", "stopped", "claim"); err == nil {
		t.Fatal("stopped recipient claimed work")
	}
	w, _ = s.Worktree(w.ID)
	if w.WakeBudget != 0 {
		t.Fatal("notification consumed budget")
	}
}
