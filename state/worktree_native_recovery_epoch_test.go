package main

import (
	"encoding/json"
	"testing"
)

func coldEpochFixture(t *testing.T) (delegationFixture, NativeRecoveryRequest, DelegationTurn) {
	t.Helper()
	f, r := nativeRecoveryFixture(t)
	if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.nativeRecoveryAdmissionWithProbe(r, absentNativeFixturePID, true); err != nil {
		t.Fatal(err)
	}
	next := nextDelegationTurn(f)
	next.RunID = r.Operation
	next.RequestID = r.Operation
	turn, err := f.s.queueDelegationTurnWithColdRecovery(r.Owner, r.Runtime, r.Job, next, &r)
	if err != nil {
		t.Fatal(err)
	}
	return f, r, turn
}
func TestNativeRecoveryNewEpochRetainsLogicalIdentityAndOneSDKOpen(t *testing.T) {
	f, r, turn := coldEpochFixture(t)
	before, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := f.s.startNativeRecoveryEpochWithProbe(r, turn.ID, absentNativeFixturePID)
	if err != nil || epoch.HostRuntime == "" || epoch.HostRuntime == epoch.ChildRuntime {
		t.Fatalf("no new physical epoch: %+v %v", epoch, err)
	}
	after, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := after.agent(f.job.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Runtime != epoch.HostRuntime || child.NativeID != epoch.NativeID || len(after.Agents) != len(before.Agents) || len(after.Views) != len(before.Views) {
		t.Fatal("epoch replaced logical child/native/slot/view")
	}
	if _, err = f.s.startNativeRecoveryEpochWithProbe(r, turn.ID, absentNativeFixturePID); err == nil {
		t.Fatal("physical epoch replay")
	}
	if _, err = f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, epoch.ChildRuntime); err == nil {
		t.Fatal("stale physical owner opened SDK")
	}
	opened, err := f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, epoch.HostRuntime)
	if err != nil || !opened.SDKOpened {
		t.Fatal("no authoritative SDK opening permit", err)
	}
	if _, err = f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, epoch.HostRuntime); err == nil {
		t.Fatal("SDK opening replay")
	}
	snapshot := child.Adapter
	bad := snapshot
	bad.Leaf = "another-leaf"
	if err = f.s.bindNativeRecoverySDK(r, f.job.ChildID, epoch.HostRuntime, epoch.NativeID, bad); err == nil {
		t.Fatal("unobserved SDK leaf accepted")
	}
	if err = f.s.bindNativeRecoverySDK(r, f.job.ChildID, epoch.HostRuntime, "replacement-native", snapshot); err == nil {
		t.Fatal("SDK replacement identity accepted")
	}
	if err = f.s.bindNativeRecoverySDK(r, f.job.ChildID, epoch.HostRuntime, epoch.NativeID, snapshot); err != nil {
		t.Fatal(err)
	}
	var raw, state string
	if err = f.s.db.QueryRow(`SELECT lease,state FROM native_recovery_leases WHERE id=?`, epoch.ID).Scan(&raw, &state); err != nil {
		t.Fatal(err)
	}
	var saved NativeRecoveryLease
	if err = json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatal(err)
	}
	if state != "completed" || saved.NewTurnID != turn.ID || saved.HostRuntime != epoch.HostRuntime {
		t.Fatal("new epoch provenance not retained")
	}
	old, _, _, err := readDelegationTurnForFixture(f.s, f.job.ID, f.turn.ID)
	if err != nil || old.State != "completed" {
		t.Fatal("old completed turn replayed/rewritten", err)
	}
}
func readDelegationTurnForFixture(s *Store, job, turn string) (DelegationTurn, DelegationTurnRequest, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return DelegationTurn{}, DelegationTurnRequest{}, "", err
	}
	defer tx.Rollback()
	return readDelegationTurnTx(tx, job, turn)
}
func TestNativeRecoveryQueueCannotReusePermitOrChangeOperation(t *testing.T) {
	f, r, turn := coldEpochFixture(t)
	next := nextDelegationTurn(f)
	next.RunID = r.Operation
	next.RequestID = r.Operation
	if _, err := f.s.queueDelegationTurnWithColdRecovery(r.Owner, r.Runtime, r.Job, next, &r); err == nil {
		t.Fatal("cold queue permit reused")
	}
	if _, err := f.s.queueDelegationTurn(r.Owner, r.Runtime, r.Job, next); err == nil {
		t.Fatal("warm queue bypassed claimed cold lease")
	}
	wrong := r
	wrong.Operation = "not-the-admitted-operation"
	if _, err := f.s.startNativeRecoveryEpochWithProbe(wrong, turn.ID, absentNativeFixturePID); err == nil {
		t.Fatal("cross-operation physical epoch")
	}
}
