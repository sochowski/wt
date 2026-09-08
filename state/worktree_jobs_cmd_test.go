package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDelegationCommandRoundTripAndMalformedInput(t *testing.T) {
	f := newDelegationFixture(t)
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(map[string]any{"admission": f.a, "runtime": "parent-runtime", "request": f.r}); err != nil {
		t.Fatal(err)
	}
	result, err := delegationCommand(f.s, "reserve", &body)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var reservation struct {
		Job  DelegationJob  `json:"job"`
		Turn DelegationTurn `json:"turn"`
	}
	if err = json.Unmarshal(encoded, &reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.Job.ID == "" || reservation.Turn.State != "queued" {
		t.Fatalf("missing reservation: %s", encoded)
	}
	for _, input := range []string{`{} {}`, `{"unexpected":true}`, "\xc2", strings.Repeat(" ", 1024*1024+1)} {
		if _, err := delegationCommand(f.s, "status", strings.NewReader(input)); err == nil {
			t.Fatalf("invalid command input accepted: %q", input[:min(len(input), 30)])
		}
	}
	if _, err := delegationCommand(f.s, "unknown", strings.NewReader(`{}`)); err == nil {
		t.Fatal("unknown operation accepted")
	}
}

func TestDelegationHostCommandAdmittedConfig(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	var config map[string]any
	if err := json.Unmarshal(f.a.Contract, &config); err != nil {
		t.Fatal(err)
	}
	binding := map[string]any{"jobId": f.job.ID, "turnId": f.turn.ID, "configDigest": f.job.ContractDigest, "provider": f.a.Provider, "ownerSessionId": f.a.OwnerSessionID, "parentSessionId": f.a.ParentNativeID}
	config["nativeExecution"] = binding
	file := filepath.Join(t.TempDir(), "native-runner.json")
	request := delegationHostLaunch{Owner: f.a.DelegationOwner, Turn: f.turn.ID, Command: "/usr/bin/node", Args: []string{"/package/jiti-cli.mjs", "/package/subagent-runner.ts", file}}
	write := func() {
		t.Helper()
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if err := f.s.validateDelegationHostCommand(f.job, request); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"jobId", "turnId", "configDigest", "provider", "ownerSessionId", "parentSessionId"} {
		original := binding[key]
		binding[key] = "mismatch"
		write()
		if err := f.s.validateDelegationHostCommand(f.job, request); err == nil {
			t.Fatalf("accepted mismatched %s", key)
		}
		binding[key] = original
	}
	original := config["tools"]
	config["tools"] = []string{}
	write()
	if err := f.s.validateDelegationHostCommand(f.job, request); err == nil {
		t.Fatal("accepted weakened admitted tools")
	}
	config["tools"] = original
	write()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, append(data, []byte(` {}`)...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.s.validateDelegationHostCommand(f.job, request); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}
