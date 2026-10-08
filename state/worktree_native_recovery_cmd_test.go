package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestNativeRecoveryControlUsesRealKernelAbsenceAndOneShotStoreClaim(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	// A genuinely exited PRIVATE process, not an invented PID or a live host.
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	file := f.job.SessionFile + ".native-host.json"
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var sidecar map[string]any
	if err = json.Unmarshal(data, &sidecar); err != nil {
		t.Fatal(err)
	}
	sidecar["hostPid"] = cmd.Process.Pid
	data, _ = json.Marshal(sidecar)
	if err = os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	call := func(op string) (any, error) { return delegationCommand(f.s, op, strings.NewReader(jsonText(r))) }
	prepared, err := call("prepare-cold")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.(NativeRecoveryLease).State != "prepared" {
		t.Fatal(prepared)
	}
	claimed, err := call("claim-cold")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.(NativeRecoveryLease).State != "claimed" {
		t.Fatal(claimed)
	}
	if _, err = call("claim-cold"); err == nil {
		t.Fatal("CLI transport replayed SDK startup permit")
	}
	if _, err = call("cancel-cold"); err == nil {
		t.Fatal("CLI transport released uncertain claimed lease")
	}
}
func TestNativeRecoveryControlRejectsCallerSuppliedProofOrEpoch(t *testing.T) {
	f, r := nativeRecoveryFixture(t)
	for _, body := range []string{jsonText(r) + " {}", strings.Replace(jsonText(r), `"version":1`, `"version":2`, 1), strings.TrimSuffix(jsonText(r), "}") + `,"hostGone":true}`, strings.TrimSuffix(jsonText(r), "}") + `,"runtimeEpoch":"fake"}`, strings.TrimSuffix(jsonText(r), "}") + `,"budgetProven":true}`} {
		if _, err := delegationCommand(f.s, "prepare-cold", strings.NewReader(body)); err == nil {
			t.Fatalf("caller forged recovery facts: %s", body)
		}
	}
	var count int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM native_recovery_leases`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected transport persisted lease", count, err)
	}
}
