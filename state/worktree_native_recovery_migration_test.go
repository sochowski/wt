package main

import "testing"

func TestNativeRecoveryV6UpgradePreservesAdmittedNativeIdentity(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	before, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`DROP TABLE native_recovery_leases; PRAGMA user_version=6;`); err != nil {
		t.Fatal(err)
	}
	if err = f.s.migrateWorktrees(); err != nil {
		t.Fatal(err)
	}
	after, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if jsonText(before) != jsonText(after) {
		t.Fatal("metadata migration changed native conversation, scope, views or permissions")
	}
	if _, err = f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err != nil {
		t.Fatal(err)
	}
	if err = f.s.migrateWorktrees(); err != nil {
		t.Fatalf("v7 reopen is not idempotent: %v", err)
	}
	observed, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID)
	if err != nil || !observed.Observed {
		t.Fatalf("migration lost retained lease: %+v %v", observed, err)
	}
}
