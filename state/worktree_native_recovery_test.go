package main

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"
	"testing"
)

// Backend metadata fixtures only, NOT actual SDK/process recovery evidence.
func nativeRecoveryFixture(t *testing.T) (delegationFixture, NativeRecoveryRequest) {
	t.Helper()
	f := newDelegationFixture(t)
	f.reserve(t)
	f.bind(t)
	f.complete(t)
	if err := os.WriteFile(f.job.SessionFile, []byte("{\"type\":\"session\",\"id\":\"child-native\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	sidecar := map[string]any{"version": 1, "provider": f.a.Provider, "ownerSessionId": f.a.OwnerSessionID, "parentSessionId": f.a.ParentNativeID, "jobId": f.job.ID, "nativeId": f.job.NativeID, "sessionFile": f.job.SessionFile, "turnId": f.turn.ID, "runId": f.r.RunID, "configDigest": f.job.ContractDigest, "hostPid": os.Getpid()}
	data, _ := json.Marshal(sidecar)
	if err := os.WriteFile(f.job.SessionFile+".native-host.json", data, 0600); err != nil {
		t.Fatal(err)
	}
	return f, NativeRecoveryRequest{Version: 1, Owner: f.a.DelegationOwner, Runtime: "parent-runtime", Job: f.job.ID, PreviousTurn: f.turn.ID, Operation: "new-cold-operation"}
}
func absentNativeFixturePID(int) error { return syscall.ESRCH }
func TestNativeRecoveryExclusiveLeaseDoesNotLaunchResetOrRebind(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	before, _ := f.s.Worktree(f.root.ID)
	lease, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID)
	if err != nil || lease.Observed || lease.State != "prepared" || lease.NativeID != f.job.NativeID || lease.Leaf != "observed-leaf" {
		t.Fatalf("bad lease: %+v %v", lease, err)
	}
	again, err := f.s.prepareNativeRecoveryWithProbe(r, func(int) error { t.Fatal("observing a lease must not reprobe/publish"); return nil })
	if err != nil || !again.Observed || again.ID != lease.ID {
		t.Fatalf("bad observation: %+v %v", again, err)
	}
	other := r
	other.Operation = "competing-cold-operation"
	if _, err := f.s.prepareNativeRecoveryWithProbe(other, absentNativeFixturePID); err == nil {
		t.Fatal("two exclusive recovery leases acquired")
	}
	after, _ := f.s.Worktree(f.root.ID)
	if jsonText(before) != jsonText(after) {
		t.Fatal("lease changed admitted agent/view/runtime/snapshot or capacity")
	}
	if _, err := f.s.queueDelegationTurn(r.Owner, r.Runtime, r.Job, nextDelegationTurn(f)); err == nil || !strings.Contains(err.Error(), "exclusive cold recovery") {
		t.Fatalf("warm dispatch escaped cold lease: %v", err)
	}
}
func TestNativeRecoveryOwnerLiveHostAndUnknownLivenessRefuse(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	bad := r
	bad.Owner.ParentNativeID = "other-parent"
	if _, err := f.s.prepareNativeRecoveryWithProbe(bad, absentNativeFixturePID); err == nil {
		t.Fatal("cross-owner recovery admitted")
	}
	for _, probe := range []func(int) error{func(int) error { return nil }, func(int) error { return syscall.EPERM }} {
		if _, err := f.s.prepareNativeRecoveryWithProbe(r, probe); err == nil || !strings.Contains(err.Error(), "absence is unproven") {
			t.Fatalf("unproven old host admitted: %v", err)
		}
	}
}
func TestNativeRecoveryRejectsChangedSnapshotPinnedViewAndPendingTurn(t *testing.T) {
	t.Run("changed-leaf", func(t *testing.T) {
		f, r := nativeRecoveryFixture(t)
		if _, err := f.s.db.Exec(`UPDATE agent_sessions SET adapter=json_set(adapter,'$.leaf','different') WHERE id=?`, f.job.ChildID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err == nil {
			t.Fatal("changed native leaf admitted")
		}
	})
	t.Run("pinned-view", func(t *testing.T) {
		f, r := nativeRecoveryFixture(t)
		if _, err := f.s.db.Exec(`UPDATE views SET pinned=1 WHERE target=?`, f.job.ChildID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err == nil {
			t.Fatal("pinned native view admitted")
		}
	})
	t.Run("pending-turn", func(t *testing.T) {
		f, r := nativeRecoveryFixture(t)
		if _, err := f.s.queueDelegationTurn(r.Owner, r.Runtime, r.Job, nextDelegationTurn(f)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err == nil {
			t.Fatal("queued turn silently taken over")
		}
	})
}
func TestNativeRecoveryLegacyFiniteBudgetProofIsNotGuessed(t *testing.T) {
	for _, raw := range []string{`{"steps":[{"toolBudget":{"hard":10}}]}`, `{"usageBudget":{"tokens":{"hard":1000}}}`, `{"inheritedChildRuntime":{"capabilityCeiling":{"toolBudget":{"soft":5,"hard":8}}}}`} {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if !nativeRecoveryNeedsBudgetProof(value) {
			t.Fatalf("unproven budget ignored: %s", raw)
		}
	}
	f, r := nativeRecoveryFixture(t)
	a := f.a
	a.Contract = json.RawMessage(`{"toolBudget":{"hard":10}}`)
	a.ContractDigest = delegationDigest(a.Contract)
	if _, err := f.s.db.Exec(`UPDATE delegation_jobs SET admission=?,contract_digest=? WHERE id=?`, delegationAdmissionJSON(a), a.ContractDigest, f.job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err == nil || !strings.Contains(err.Error(), "finite-budget accounting") {
		t.Fatalf("budget reset permitted: %v", err)
	}
}
func TestNativeRecoverySkipsOnlyDefinitelyUnpublishedSuffix(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	turn, err := f.s.queueDelegationTurn(r.Owner, r.Runtime, r.Job, nextDelegationTurn(f))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.cancelUnpublishedDelegationTurn(r.Owner, r.Runtime, r.Job, turn.ID, "fixture publication never occurred"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err != nil {
		t.Fatalf("honest unpublished suffix rejected: %v", err)
	}
	var state string
	var result string
	if err = f.s.db.QueryRow(`SELECT state,result FROM delegation_turns WHERE id=?`, turn.ID).Scan(&state, &result); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || !strings.Contains(result, `"unpublished":true`) {
		t.Fatal("cancelled attempt history rewritten")
	}
}
