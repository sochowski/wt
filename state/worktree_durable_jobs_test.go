package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func durableJobFixture(t *testing.T) (*Store, Worktree, DurableJobRequest) {
	t.Helper()
	s := worktreeTestStore(t)
	w := testRoot(t, s, "durable-job")
	cwd, err := canonicalDir(w.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", cwd).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	t.Setenv("WT_STATUS_DIR", t.TempDir())
	store := filepath.Join(t.TempDir(), "parent.sqlite")
	if err = os.WriteFile(store, []byte("fixture store"), 0600); err != nil {
		t.Fatal(err)
	}
	d := &DurableSnapshot{Store: store, UUID: newID(), Conversation: 1, Definition: durableDefinition, Dependencies: durableDependencies, Cwd: cwd, WriterLock: durableWriterLock(cwd), Initialized: true}
	adapter := PiSnapshot{Version: 1, Backend: "durable", Durable: d}
	if _, err = s.db.Exec(`UPDATE agent_sessions SET cwd=?,runtime='parent-runtime',adapter=? WHERE id=?`, cwd, jsonText(adapter), w.Agents[0].ID); err != nil {
		t.Fatal(err)
	}
	w, err = s.Worktree(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, w, DurableJobRequest{Version: 1, Operation: "operation", Task: "Implement bounded fixture", Cwd: cwd, Criteria: []string{"Independent evidence"}}
}
func setDurableHost(t *testing.T, s *Store, j DurableJob, role string) (string, string) {
	t.Helper()
	child, tools, keyColumn := j.Child, j.Contract.WriterTools, "writer_key"
	if role == "reviewer" {
		child, tools, keyColumn = j.Reviewer, j.Contract.ReviewerTools, "reviewer_key"
	}
	w, err := s.Worktree(j.Root)
	if err != nil {
		t.Fatal(err)
	}
	a, err := w.agent(child)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Adapter.Durable.Tools, tools) {
		t.Fatal("role ceiling not persisted")
	}
	d := a.Adapter.Durable
	if err = os.WriteFile(d.Store, []byte("fixture child store"), 0600); err != nil {
		t.Fatal(err)
	}
	d.Initialized = true
	if _, err = s.db.Exec(`UPDATE agent_sessions SET adapter=?,runtime='host-runtime',stopped=0 WHERE id=?`, jsonText(a.Adapter), child); err != nil {
		t.Fatal(err)
	}
	capability := newID() + newID()
	if _, err = s.db.Exec(`UPDATE durable_jobs SET `+keyColumn+`=? WHERE id=?`, delegationDigest([]byte(capability)), j.ID); err != nil {
		t.Fatal(err)
	}
	return child, capability
}
func TestDurableJobV1ImmutableAdmissionBudgetsCeilingsAndNoNativeIDs(t *testing.T) {
	s, w, r := durableJobFixture(t)
	parent := w.Agents[0].ID
	j, err := s.reserveDurableJob(w.ID, parent, "parent-runtime", r)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.reserveDurableJob(w.ID, parent, "parent-runtime", r)
	if err != nil || !reflect.DeepEqual(j, again) {
		t.Fatalf("retry changed admission: %+v %v", again, err)
	}
	got, _ := s.Worktree(w.ID)
	if got.WakeBudget != 62 || len(got.Agents) != 3 {
		t.Fatalf("duplicate reservation/budget: %+v", got)
	}
	for _, a := range got.Agents {
		if a.NativeID != "" || a.Adapter.File != "" {
			t.Fatal("fabricated native binding")
		}
	}
	reviewer, _ := got.agent(j.Reviewer)
	if !reviewer.Adapter.Durable.ReadOnly || containsTool(reviewer.Adapter.Durable.Tools, "bash") || containsTool(reviewer.Adapter.Durable.Tools, "write") {
		t.Fatal("reviewer escalated")
	}
	if j.Contract.ParentUUID != got.Agents[0].Adapter.Durable.UUID || j.Contract.RoleVersion != "wt-builtins-v1" || j.ResultID == j.ReviewID || j.Digest != delegationDigest([]byte(jsonText(j.Contract))) {
		t.Fatal("missing immutable identity/role digest")
	}
	changed := r
	changed.Task = "different"
	if _, err = s.reserveDurableJob(w.ID, parent, "parent-runtime", changed); err == nil {
		t.Fatal("same operation changed immutable payload")
	}
	if _, err = s.reserveDurableJob(w.ID, parent, "stale", r); err == nil {
		t.Fatal("stale parent retried")
	}
	if _, err = s.reserveDurableJob(w.ID, j.Child, "host-runtime", r); err == nil {
		t.Fatal("nested writer admitted")
	}
	s.db.Exec(`UPDATE roots SET wake_enabled=0 WHERE id=?`, w.ID)
	if _, err = s.reserveDurableJob(w.ID, parent, "parent-runtime", r); err != nil {
		t.Fatal("reconcile spent another disabled permit")
	}
	r.Operation = "new"
	if _, err = s.reserveDurableJob(w.ID, parent, "parent-runtime", r); err == nil {
		t.Fatal("disabled wake admitted new writer")
	}
}
func TestDurableJobWriterLeaseParentCeilingAndRetainedRootLimit(t *testing.T) {
	s, w, r := durableJobFixture(t)
	parent := w.Agents[0].ID
	lease, err := lockFile(durableWriterLock(r.Cwd))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.reserveDurableJob(w.ID, parent, "parent-runtime", r); err == nil {
		t.Fatal("occupied canonical writer lease admitted")
	}
	lease.Close()
	a := w.Agents[0]
	a.Adapter.Durable.ReadOnly = true
	s.db.Exec(`UPDATE agent_sessions SET adapter=? WHERE id=?`, jsonText(a.Adapter), parent)
	if _, err = s.reserveDurableJob(w.ID, parent, "parent-runtime", r); err == nil {
		t.Fatal("read-only parent escalated to writer")
	}
	a.Adapter.Durable.ReadOnly = false
	s.db.Exec(`UPDATE agent_sessions SET adapter=? WHERE id=?`, jsonText(a.Adapter), parent)
	for i := 0; i < 3; i++ {
		r.Operation = string(rune('a' + i))
		if _, err = s.reserveDurableJob(w.ID, parent, "parent-runtime", r); err != nil {
			t.Fatal(err)
		}
	}
	r.Operation = "fourth"
	if _, err = s.reserveDurableJob(w.ID, parent, "parent-runtime", r); err == nil {
		t.Fatal("root retained-agent budget exceeded")
	}
	got, _ := s.Worktree(w.ID)
	if len(got.Agents) != 7 || got.WakeBudget != 58 {
		t.Fatalf("failed capacity reservation spent budget: %+v", got)
	}
}
func TestDurableJobHostCapabilityMandatoryIndependentReviewAndImmutableResults(t *testing.T) {
	s, w, r := durableJobFixture(t)
	j, err := s.reserveDurableJob(w.ID, w.Agents[0].ID, "parent-runtime", r)
	if err != nil {
		t.Fatal(err)
	}
	child, key := setDurableHost(t, s, j, "writer")
	if _, err = s.durableJobHost(w.ID, child, "host-runtime", ""); err == nil {
		t.Fatal("bash runtime token alone forged host")
	}
	if _, err = s.durableJobHost(w.ID, child, "stale", key); err == nil {
		t.Fatal("stale host")
	}
	o := DurableJobObservation{ID: j.ResultID, Success: true, Evidence: json.RawMessage(`{"host_observed":"fixture"}`)}
	if err = s.finishDurableJob(w.ID, child, "host-runtime", "wrong", o); err == nil {
		t.Fatal("forged host result")
	}
	if err = s.finishDurableJob(w.ID, child, "host-runtime", key, o); err != nil {
		t.Fatal(err)
	}
	current, _ := s.durableJob(j.ID)
	if current.State != "review" || len(current.Review) > 0 {
		t.Fatal("writer JSON became success without independent review")
	}
	if err = s.finishDurableJob(w.ID, child, "host-runtime", key, o); err != nil {
		t.Fatal("receipt recovery not idempotent", err)
	}
	o.Evidence = json.RawMessage(`{"replacement":true}`)
	if err = s.finishDurableJob(w.ID, child, "host-runtime", key, o); err == nil {
		t.Fatal("writer result replaced")
	}
	reviewer, key := setDurableHost(t, s, j, "reviewer")
	s.db.Exec(`UPDATE agent_sessions SET stopped=1 WHERE id=?`, j.Parent) // Independent admitted review outlives parent stop.
	review := DurableJobObservation{ID: j.ReviewID, Success: false, Evidence: json.RawMessage(`{"uncertain":true}`)}
	if err = s.finishDurableJob(w.ID, reviewer, "host-runtime", key, review); err != nil {
		t.Fatal(err)
	}
	current, _ = s.durableJob(j.ID)
	if current.State != "failed" {
		t.Fatal("uncertain review became success")
	}
	aRoot, _ := s.Worktree(w.ID)
	a, _ := aRoot.agent(reviewer)
	if err = s.durableJobRunnable(a); err == nil {
		t.Fatal("terminal reviewer relaunched")
	}
}

func TestDurableTaskOrdinaryCLICeiling(t *testing.T) {
	s, w, r := durableJobFixture(t)
	j, err := s.reserveDurableJob(w.ID, w.Agents[0].ID, "parent-runtime", r)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"writer", "reviewer"} {
		child, _ := setDurableHost(t, s, j, role)
		t.Setenv("WT_AGENT_ID", child)
		t.Setenv("WT_ROOT_ID", w.ID)
		t.Setenv("WT_RUNTIME_ID", "host-runtime")
		for _, args := range [][]string{
			{"agents", "create", w.ID, "nested", "--backend", "native"}, {"agents", "create", w.ID, "nested", "--backend", "durable"},
			{"agents", "stop", w.ID, child}, {"agents", "reparent", w.ID, child, "-"}, {"present", w.ID, "root"},
			{"view", "create", w.ID, "editor", "root"}, {"checkout", "attach", w.ID, "other", w.Cwd},
			{"message", "send", w.ID, child, w.Agents[0].ID, "wake"}, {"restore", w.ID}, {"new", "nested"},
			{"_delegation", "reserve"}, {"_durable-jobs", "reserve"}, {"_durable-human-stop"},
		} {
			if err := worktreeCommand(s, args); err == nil {
				t.Fatalf("%s CLI ceiling admitted %v", role, args)
			}
		}
		if err := worktreeCommand(s, []string{"agents", "show", w.ID, child}); err != nil {
			t.Fatal(err)
		}
		live, _ := s.Worktree(w.ID)
		if _, err := s.addAgent(live, "bypass", child, child, w.Cwd); err == nil {
			t.Fatal("lower admission bypass")
		}
	}
	got, _ := s.Worktree(w.ID)
	if len(got.Agents) != 3 {
		t.Fatal("rejected command mutated peers")
	}
}

func TestDurableTaskTopLevelStateAndRegistryAdmission(t *testing.T) {
	s, w, r := durableJobFixture(t)
	j, err := s.reserveDurableJob(w.ID, w.Agents[0].ID, "parent-runtime", r)
	if err != nil {
		t.Fatal(err)
	}
	child, _ := setDurableHost(t, s, j, "writer")
	t.Setenv("WT_AGENT_ID", child)
	t.Setenv("WT_ROOT_ID", w.ID)
	t.Setenv("WT_RUNTIME_ID", "host-runtime")
	for _, cmd := range []string{"set", "delete", "migrate"} {
		if err = s.authorizeDurableStateCommand(cmd, []string{w.Name}); err == nil {
			t.Fatal(cmd)
		}
	}
	for _, args := range [][]string{{"install-hooks"}, {"pi", "session-setup"}, {"pi", "launch-plan"}} {
		cmd := "agents"
		if args[0] == "pi" {
			cmd = "agent"
		}
		if err = s.authorizeDurableStateCommand(cmd, args); err == nil {
			t.Fatal(args)
		}
	}
	for _, cmd := range []string{"get", "list", "counts", "fzf-bindings"} {
		if err = s.authorizeDurableStateCommand(cmd, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.authorizeDurableStateCommand("agents", []string{"list"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_RUNTIME_ID", "stale")
	if err = s.authorizeDurableStateCommand("set", nil); err == nil {
		t.Fatal("stale became human")
	}
	t.Setenv("WT_RUNTIME_ID", "host-runtime")
	t.Setenv("WT_ROOT_ID", "missing")
	if err = s.authorizeDurableStateCommand("agents", []string{"install-hooks"}); err == nil {
		t.Fatal("missing admission became human")
	}
	// Live nonjob/native callers preserve their ordinary routes.
	native := testRoot(t, s, "native-state-admission")
	s.db.Exec(`UPDATE agent_sessions SET runtime='native-runtime' WHERE id=?`, native.Agents[0].ID)
	t.Setenv("WT_ROOT_ID", native.ID)
	t.Setenv("WT_AGENT_ID", native.Agents[0].ID)
	t.Setenv("WT_RUNTIME_ID", "native-runtime")
	if err = s.authorizeDurableStateCommand("agents", []string{"install-hooks"}); err != nil {
		t.Fatal(err)
	}
	if err = s.authorizeDurableStateCommand("set", nil); err != nil {
		t.Fatal(err)
	}
}
