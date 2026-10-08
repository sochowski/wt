package main

import (
	"os"
	"testing"
)

// Durable ledger reopen tests. These are not SIGKILL/PTY launcher tests.
func reopenRecoveryFixture(t *testing.T, f *delegationFixture) {
	t.Helper()
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(os.Getenv("WT_DB"))
	if err != nil {
		t.Fatal(err)
	}
	f.s = reopened
	t.Cleanup(func() { reopened.Close() })
}
func TestNativeRecoveryReopenNeverRepeatsConsumedStartup(t *testing.T) {
	for _, stage := range []string{"queued", "epoch", "sdk-open", "sdk-bound"} {
		t.Run(stage, func(t *testing.T) {
			f, r, turn := coldEpochFixture(t)
			var epoch NativeRecoveryLease
			var err error
			if stage != "queued" {
				epoch, err = f.s.startNativeRecoveryEpochWithProbe(r, turn.ID, absentNativeFixturePID)
				if err != nil {
					t.Fatal(err)
				}
			}
			if stage == "sdk-open" || stage == "sdk-bound" {
				if _, err = f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, epoch.HostRuntime); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "sdk-bound" {
				root, err := f.s.Worktree(f.root.ID)
				if err != nil {
					t.Fatal(err)
				}
				child, err := root.agent(f.job.ChildID)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.s.bindNativeRecoverySDK(r, f.job.ChildID, epoch.HostRuntime, epoch.NativeID, child.Adapter); err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.s.Worktree(f.root.ID)
			if err != nil {
				t.Fatal(err)
			}
			reopenRecoveryFixture(t, &f)
			after, err := f.s.Worktree(f.root.ID)
			if err != nil {
				t.Fatal(err)
			}
			if jsonText(before) != jsonText(after) {
				t.Fatal("reopen changed logical identity, runtime, view, snapshot or capacity")
			}
			receipt, err := f.s.prepareNativeRecoveryWithProbe(r, func(int) error { t.Fatal("receipt observation reprobed/relaunched"); return nil })
			if err != nil || !receipt.Observed {
				t.Fatal("lost durable operation receipt", err)
			}
			if _, err = f.s.claimNativeRecovery(r); err == nil {
				t.Fatal("reopened operation consumed its claim twice")
			}
			if err = f.s.cancelPreparedNativeRecovery(r); err == nil {
				t.Fatal("reopen cancelled a consumed/uncertain operation")
			}
			next := nextDelegationTurn(f)
			next.RunID = r.Operation
			next.RequestID = r.Operation
			if _, err = f.s.queueDelegationTurnWithColdRecovery(r.Owner, r.Runtime, r.Job, next, &r); err == nil {
				t.Fatal("reopen queued old cold instruction twice")
			}
			if stage != "queued" {
				if _, err = f.s.startNativeRecoveryEpochWithProbe(r, turn.ID, absentNativeFixturePID); err == nil {
					t.Fatal("reopen allocated a second physical epoch")
				}
			}
			if stage == "sdk-open" || stage == "sdk-bound" {
				if _, err = f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, epoch.HostRuntime); err == nil {
					t.Fatal("reopen constructed a second SDK writer")
				}
			}
			old, _, _, err := readDelegationTurnForFixture(f.s, f.job.ID, f.turn.ID)
			if err != nil || old.State != "completed" {
				t.Fatal("reopen rewrote/replayed original settled work", err)
			}
		})
	}
}
func TestNativeRecoverySDKOpenDriftDoesNotConsumePermit(t *testing.T) {
	for _, file := range []string{"transcript", "sidecar"} {
		t.Run(file, func(t *testing.T) {
			f, r, turn := coldEpochFixture(t)
			epoch, err := f.s.startNativeRecoveryEpochWithProbe(r, turn.ID, absentNativeFixturePID)
			if err != nil {
				t.Fatal(err)
			}
			target := f.job.SessionFile
			if file == "sidecar" {
				target += ".native-host.json"
			}
			original, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(target, append(original, []byte(" ")...), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, epoch.HostRuntime); err == nil {
				t.Fatal("drifted source opened SDK")
			}
			// Fixture restores exact bytes to prove a rejected pre-open check did not
			// claim success or consume the permit; production files are never repaired.
			if err = os.WriteFile(target, original, 0600); err != nil {
				t.Fatal(err)
			}
			permit, err := f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, epoch.HostRuntime)
			if err != nil || !permit.SDKOpened {
				t.Fatal("rejected drift falsely consumed SDK opening permit", err)
			}
		})
	}
}
