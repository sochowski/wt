package main

import (
	"syscall"
	"testing"
)

func TestNativeRecoveryClaimIsOneShotAndDoesNotDispatchOrChangeRuntime(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	if _, err := f.s.nativeRecoveryAdmissionWithProbe(r, absentNativeFixturePID, true); err == nil {
		t.Fatal("claim without preparation")
	}
	if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err != nil {
		t.Fatal(err)
	}
	before, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := f.s.nativeRecoveryAdmissionWithProbe(r, absentNativeFixturePID, true)
	if err != nil || lease.State != "claimed" || lease.Observed {
		t.Fatalf("invalid claim: %+v %v", lease, err)
	}
	after, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jsonText(before) != jsonText(after) {
		t.Fatal("claim fabricated a runtime, identity or process")
	}
	if _, err = f.s.nativeRecoveryAdmissionWithProbe(r, absentNativeFixturePID, true); err == nil {
		t.Fatal("consumed SDK startup permit replayed")
	}
	observed, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID)
	if err != nil || observed.State != "claimed" || !observed.Observed {
		t.Fatalf("claim receipt lost: %+v %v", observed, err)
	}
}

func TestNativeRecoveryClaimRechecksFactsBeforeConsumingPermit(t *testing.T) {
	for _, kind := range []string{"live-host", "unknown-host", "runtime-drift", "leaf-drift", "pinned"} {
		t.Run(kind, func(t *testing.T) {
			f, r := nativeRecoveryFixture(t)
			if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err != nil {
				t.Fatal(err)
			}
			probe := absentNativeFixturePID
			switch kind {
			case "live-host":
				probe = func(int) error { return nil }
			case "unknown-host":
				probe = func(int) error { return syscall.EPERM }
			case "runtime-drift":
				if _, err := f.s.db.Exec(`UPDATE agent_sessions SET runtime='unexpected-owner' WHERE id=?`, f.job.ChildID); err != nil {
					t.Fatal(err)
				}
			case "leaf-drift":
				if _, err := f.s.db.Exec(`UPDATE agent_sessions SET adapter=json_set(adapter,'$.leaf','unexpected-leaf') WHERE id=?`, f.job.ChildID); err != nil {
					t.Fatal(err)
				}
			case "pinned":
				if _, err := f.s.db.Exec(`UPDATE views SET pinned=1 WHERE target=?`, f.job.ChildID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.s.nativeRecoveryAdmissionWithProbe(r, probe, true); err == nil {
				t.Fatal("drifted recovery claim succeeded")
			}
			var state string
			if err := f.s.db.QueryRow(`SELECT state FROM native_recovery_leases WHERE job_id=?`, f.job.ID).Scan(&state); err != nil || state != "prepared" {
				t.Fatalf("failed check consumed permit: %s %v", state, err)
			}
		})
	}
}
