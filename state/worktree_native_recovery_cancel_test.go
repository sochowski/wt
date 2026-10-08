package main

import "testing"

func TestNativeRecoveryCancelOnlyUnconsumedPreparationPreservesHistory(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err != nil {
		t.Fatal(err)
	}
	wrong := r
	wrong.Owner.OwnerSessionID = "another-owner"
	if err := f.s.cancelPreparedNativeRecovery(wrong); err == nil {
		t.Fatal("another owner cancelled lease")
	}
	if err := f.s.cancelPreparedNativeRecovery(r); err != nil {
		t.Fatal(err)
	}
	if err := f.s.cancelPreparedNativeRecovery(r); err != nil {
		t.Fatal("safe cancellation not idempotent", err)
	}
	observed, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID)
	if err != nil || !observed.Observed || observed.State != "failed" {
		t.Fatalf("cancelled identity resurrected: %+v %v", observed, err)
	}
	if _, err = f.s.nativeRecoveryAdmissionWithProbe(r, absentNativeFixturePID, true); err == nil {
		t.Fatal("cancelled operation granted SDK permission")
	}
	next := r
	next.Operation = "new-explicit-cold-operation"
	if _, err = f.s.prepareNativeRecoveryWithProbe(next, absentNativeFixturePID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.s.db.QueryRow(`SELECT count(*) FROM native_recovery_leases WHERE job_id=?`, r.Job).Scan(&count); err != nil || count != 2 {
		t.Fatal("old operation erased", count, err)
	}
}
func TestNativeRecoveryConsumedClaimCannotBeCancelledOrReplayed(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	if _, err := f.s.prepareNativeRecoveryWithProbe(r, absentNativeFixturePID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.nativeRecoveryAdmissionWithProbe(r, absentNativeFixturePID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.s.cancelPreparedNativeRecovery(r); err == nil {
		t.Fatal("possibly opened SDK lease cancelled")
	}
	next := r
	next.Operation = "different-operation"
	if _, err := f.s.prepareNativeRecoveryWithProbe(next, absentNativeFixturePID); err == nil {
		t.Fatal("uncertain claimed lease bypassed by new operation")
	}
}
