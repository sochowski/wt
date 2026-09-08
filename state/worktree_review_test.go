package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInboxPageLargeHistoryAndOutstandingBacklog(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "pages")
	id := w.Agents[0].ID
	// Control bytes expand sixfold in JSON: exercise worst-case maxBuffer sizing.
	body := strings.Repeat("\x01", 16384)
	for i := 0; i < 160; i++ {
		if _, err := s.send(w, fmt.Sprintf("m-%d", i), "human", id, body); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE inbox SET state='delivered' WHERE rowid<=80`); err != nil {
		t.Fatal(err)
	}
	after := int64(0)
	count := 0
	for {
		page, err := s.inboxPage(w.ID, id, after)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(page)
		if len(b) >= 1024*1024 {
			t.Fatalf("page exceeds subprocess limit: %d", len(b))
		}
		if len(page.Messages) == 0 {
			break
		}
		if page.Next <= after || len(page.Messages) > 8 {
			t.Fatal("unbounded or non-progressing page")
		}
		for _, m := range page.Messages {
			if m.State == "delivered" {
				t.Fatal("history body leaked")
			}
		}
		count += len(page.Messages)
		after = page.Next
	}
	if count != 80 {
		t.Fatalf("backlog lost: %d", count)
	}
}
func TestStoppedActorMessagingTransactionFence(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "fence")
	id := w.Agents[0].ID
	s.db.Exec(`UPDATE agent_sessions SET runtime='live' WHERE id=?`, id)
	w, _ = s.Worktree(w.ID)
	t.Setenv("WT_ROOT_ID", w.ID)
	t.Setenv("WT_AGENT_ID", id)
	t.Setenv("WT_RUNTIME_ID", "live")
	if err := validateActor(w, id); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"send", "recover"} {
		s.db.Exec(`UPDATE agent_sessions SET runtime='live',stopped=0 WHERE id=?`, id)
		tx, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`UPDATE agent_sessions SET stopped=1,runtime='' WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
		started := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			close(started)
			if op == "send" {
				_, e := s.send(w, "stale-send", id, id, "no")
				done <- e
			} else {
				done <- s.recoverInbox(w.ID, id, "live")
			}
		}()
		<-started
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-done; err == nil {
			t.Fatalf("%s accepted stopped identity", op)
		}
	}
	fresh, _ := s.Worktree(w.ID)
	if err := validateActor(fresh, id); err == nil {
		t.Fatal("post-lock revalidation accepted stopped actor")
	}
	messages, _ := s.messages(w.ID, id)
	if len(messages) != 0 {
		t.Fatal("stale message committed")
	}
}
func TestOriginalLifecycleRecencyAndStatusChangeOrdering(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "old")
	testRoot(t, s, "other")
	s.db.Exec(`UPDATE agent_sessions SET runtime='live' WHERE root_id=?`, w.ID)
	for _, status := range []string{"working", "input", "idle"} {
		s.db.Exec(`UPDATE sessions SET updated_at=1,status_changed_at=1`)
		before := time.Now().Unix()
		if err := s.updateAgent(w.ID, w.Agents[0].ID, "live", status, "", nil); err != nil {
			t.Fatal(err)
		}
		row, _, _ := s.Get(w.Name)
		if row.UpdatedAt < before || row.StatusChangedAt < before || row.Status != status {
			t.Fatalf("stale timestamps: %+v", row)
		}
		rows, _ := s.List("all", "recency")
		if rows[0].Name != w.Name {
			t.Fatal("wrong activity order")
		}
	}
}
func TestDetachBorrowedAndReferencedCheckout(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "detach")
	dir := t.TempDir()
	file := filepath.Join(dir, "keep")
	os.WriteFile(file, []byte("keep"), 0600)
	c := Checkout{ID: newID(), RootID: w.ID, Alias: "repo", Path: dir}
	s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?)`, c.ID, w.ID, c.Alias, c.Path)
	v := View{ID: newID(), RootID: w.ID, Kind: "editor", Target: c.ID, Manager: "human", State: ViewState{Version: 1}}
	if err := s.insertView(v); err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	if err := s.detachCheckout(w, c.Alias); err == nil {
		t.Fatal("in-use detach accepted")
	}
	s.db.Exec(`DELETE FROM views WHERE id=?`, v.ID)
	w, _ = s.Worktree(w.ID)
	if err := s.detachCheckout(w, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("borrowed file removed")
	}
}
func TestPinUnpinRetainsManagerAndActorProtection(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "pin")
	id := w.Agents[0].ID
	s.db.Exec(`UPDATE agent_sessions SET runtime='live' WHERE id=?`, id)
	v := View{ID: newID(), RootID: w.ID, Kind: "editor", Target: w.ID, Manager: id, State: ViewState{Version: 1}}
	if err := s.insertView(v); err != nil {
		t.Fatal(err)
	}
	if err := worktreeCommand(s, []string{"view", "pin", w.Name, v.ID}); err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	got, _ := w.view(v.ID)
	if got.Manager != id || !got.Pinned {
		t.Fatal("pin stole management")
	}
	t.Setenv("WT_ROOT_ID", w.ID)
	t.Setenv("WT_AGENT_ID", id)
	t.Setenv("WT_RUNTIME_ID", "live")
	if err := worktreeCommand(s, []string{"view", "close", w.Name, v.ID}); err == nil {
		t.Fatal("pinned mutation accepted")
	}
	t.Setenv("WT_AGENT_ID", "")
	if err := worktreeCommand(s, []string{"view", "unpin", w.Name, v.ID}); err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	got, _ = w.view(v.ID)
	if got.Manager != id || got.Pinned {
		t.Fatal("unpin lost manager")
	}
}
func TestForgetTombstoneAndExplicitReattach(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "forget")
	dir := t.TempDir()
	if err := worktreeCommand(s, []string{"forget", w.Name, dir}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(w.Name); ok {
		t.Fatal("root not forgotten")
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM forgotten_checkouts WHERE path=?`, dir).Scan(&n)
	if n != 1 {
		t.Fatal("missing tombstone")
	}
	s.Close()
	var err error
	s, err = Open(os.Getenv("WT_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.db.QueryRow(`SELECT count(*) FROM forgotten_checkouts WHERE path=?`, dir).Scan(&n)
	if n != 1 {
		t.Fatal("tombstone lost on reopen")
	}
	w = testRoot(t, s, "again")
	s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?)`, newID(), w.ID, "repo", dir)
	s.db.QueryRow(`SELECT count(*) FROM forgotten_checkouts WHERE path=?`, dir).Scan(&n)
	if n != 0 {
		t.Fatal("explicit attachment not remembered")
	}
}
