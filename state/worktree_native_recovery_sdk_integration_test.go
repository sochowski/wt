package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Opt-in compiled control/real SDK/PTY seam. Genesis is settled fixture data,
// not a previously launched native host. The tmux launcher is NOT covered.
func TestNativeRecoveryCompiledControlsWithActualSDKPTY(t *testing.T) {
	for _, phase := range []string{"success", "open", "bind", "claim", "model"} {
		t.Run(phase, func(t *testing.T) { runNativeRecoveryCompiledSDKPTY(t, phase) })
	}
}
func runNativeRecoveryCompiledSDKPTY(t *testing.T, phase string) {
	sdk := os.Getenv("WT_NATIVE_SDK_TEST_MODULE")
	fork := os.Getenv("WT_COLD_SDK_FORK")
	runner := os.Getenv("WT_COLD_SDK_PTY_RUNNER")
	if runner == "" && fork != "" {
		runner = filepath.Join(fork, "test/support/native-cold-sdk-pty.py")
	}
	if sdk == "" || fork == "" || runner == "" {
		t.Skip("explicit private SDK/fork/PTY runner required")
	}
	f := newDelegationFixture(t)
	root := f.root.Cwd
	for _, dir := range []string{"home", "agent"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	seed := `const pi=await import(process.env.WT_NATIVE_SDK_TEST_MODULE);const root=process.env.NATIVE_HOST_TEST_ROOT;const m=pi.SessionManager.create(root,root+'/sessions');m.appendModelChange('synthetic','native-host');m.appendThinkingLevelChange('off');m.appendMessage({role:'user',content:'Settled historical fixture; never redispatch',timestamp:Date.now()});m.appendMessage({role:'assistant',content:[{type:'text',text:'Settled fixture result'}],api:'openai-completions',provider:'synthetic',model:'native-host',usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:Date.now()});console.log(JSON.stringify({nativeId:m.getSessionId(),sessionFile:m.getSessionFile(),leaf:m.getLeafId(),cwd:m.getCwd()}));`
	seedCmd := exec.Command("node", "--input-type=module", "--eval", seed)
	seedCmd.Env = append(os.Environ(), "HOME="+filepath.Join(root, "home"), "PI_CODING_AGENT_DIR="+filepath.Join(root, "agent"), "NATIVE_HOST_TEST_ROOT="+root)
	data, err := seedCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("SDK genesis: %v %s", err, data)
	}
	var expected struct {
		NativeID    string `json:"nativeId"`
		SessionFile string `json:"sessionFile"`
		Leaf        string `json:"leaf"`
		Cwd         string `json:"cwd"`
	}
	if err = json.Unmarshal(data, &expected); err != nil {
		t.Fatalf("SDK identity: %v %s", err, data)
	}
	f.a.Provider = "wt-interactive-v1"
	f.a.Role = "fixture-reviewer"
	f.a.Contract = json.RawMessage(`{"version":1,"tools":["read"],"model":"synthetic/native-host","thinking":"off","systemPrompt":"Private read-only SDK fixture. Do not replay prior work."}`)
	f.a.ContractDigest = delegationDigest(f.a.Contract)
	f.r.ContractDigest = f.a.ContractDigest
	f.reserve(t)
	// Private fixture initialization only. All subsequent SDK open/bind/claim/
	// checkpoint/finish operations go through the separately compiled CLI.
	if _, err = f.s.db.Exec(`UPDATE agent_sessions SET runtime=? WHERE id=?`, f.runtime, f.job.ChildID); err != nil {
		t.Fatal(err)
	}
	snapshot := PiSnapshot{Version: 1, File: expected.SessionFile, Leaf: expected.Leaf, Persisted: true, Provider: "synthetic", Model: "native-host", Thinking: "off"}
	if err = f.s.updateAgent(f.root.ID, f.job.ChildID, f.runtime, "idle", expected.NativeID, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err = f.s.bindDelegationNative(f.job.ID, f.job.ChildID, f.runtime, expected.NativeID, expected.SessionFile); err != nil {
		t.Fatal(err)
	}
	f.job.NativeID, f.job.SessionFile = expected.NativeID, expected.SessionFile
	if _, err = f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err != nil {
		t.Fatal(err)
	}
	if err = f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, DelegationResult{State: "completed", NativeID: expected.NativeID, SessionFile: expected.SessionFile, Leaf: expected.Leaf, Output: "Settled SDK fixture result"}); err != nil {
		t.Fatal(err)
	}
	exited := exec.Command("/usr/bin/true")
	if err = exited.Run(); err != nil {
		t.Fatal(err)
	}
	sidecar := map[string]any{"version": 1, "provider": f.a.Provider, "ownerSessionId": f.a.OwnerSessionID, "parentSessionId": f.a.ParentNativeID, "jobId": f.job.ID, "nativeId": expected.NativeID, "sessionFile": expected.SessionFile, "turnId": f.turn.ID, "runId": f.r.RunID, "configDigest": f.job.ContractDigest, "hostPid": exited.Process.Pid}
	if err = os.WriteFile(expected.SessionFile+".native-host.json", []byte(jsonText(sidecar)), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "wt-state")
	build := exec.Command("go", "build", "-o", binary, ".")
	if data, err = build.CombinedOutput(); err != nil {
		t.Fatalf("compiled WT: %v %s", err, data)
	}
	parentEnv := append(os.Environ(), "WT_STATE="+binary, "WT_ROOT_ID="+f.root.ID, "WT_AGENT_ID="+f.a.ParentID, "WT_RUNTIME_ID=parent-runtime")
	call := func(op string, input any) []byte {
		t.Helper()
		cmd := exec.Command(binary, "worktree", "_delegation", op)
		cmd.Env = parentEnv
		cmd.Stdin = strings.NewReader(jsonText(input))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("compiled %s: %v %s", op, err, out)
		}
		return out
	}
	r := NativeRecoveryRequest{Version: 1, Owner: f.a.DelegationOwner, Runtime: "parent-runtime", Job: f.job.ID, PreviousTurn: f.turn.ID, Operation: "compiled-cold-operation"}
	call("prepare-cold", r)
	leaseBytes := call("claim-cold", r)
	var lease NativeRecoveryLease
	if err = json.Unmarshal(leaseBytes, &lease); err != nil {
		t.Fatal(err)
	}
	next := nextDelegationTurn(f)
	next.RunID = r.Operation
	next.RequestID = r.Operation
	next.Prompt = "Only the new cold instruction"
	next.PromptDigest = delegationDigest([]byte(next.Prompt))
	var turn DelegationTurn
	if err = json.Unmarshal(call("queue-cold", map[string]any{"recovery": r, "request": next}), &turn); err != nil {
		t.Fatal(err)
	}
	before, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The launcher-only epoch primitive is deliberately invoked in-process here;
	// this seam does not pretend to prove tmux/root/node-lock host publication.
	lease, err = f.s.startNativeRecoveryEpoch(r, turn.ID)
	if err != nil {
		t.Fatal(err)
	}
	driver, err := filepath.Abs("../config/pi-wt/native-provider.js")
	if err != nil {
		t.Fatal(err)
	}
	binding := map[string]any{"version": 1, "provider": f.a.Provider, "ownerSessionId": f.a.OwnerSessionID, "parentSessionId": f.a.ParentNativeID, "runId": r.Operation, "jobId": f.job.ID, "turnId": turn.ID, "previousTurnId": f.turn.ID, "configDigest": f.job.ContractDigest, "conversationDigest": f.job.ContractDigest, "nativeId": expected.NativeID, "sessionFile": expected.SessionFile, "coldRecovery": map[string]any{"version": 1, "leaseId": lease.ID, "operation": r.Operation, "leaf": lease.Leaf, "cwd": root, "sourceDigest": lease.SourceDigest, "sidecarDigest": lease.SidecarDigest, "modelId": lease.ModelID, "thinking": lease.Thinking, "request": r}}
	control := filepath.Join(root, "control.json")
	if err = os.WriteFile(control, []byte(jsonText(map[string]any{"expected": expected, "binding": binding, "driverModule": driver})), 0600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "sdk-pty.log")
	ptyCmd := exec.Command("python3", runner)
	ptyCmd.Env = append(parentEnv, "NATIVE_HOST_TEST_ROOT="+root, "NATIVE_HOST_TEST_SCRIPT="+filepath.Join(fork, "test/support/native-cold-sdk-pty.mjs"), "NATIVE_HOST_TEST_LOG="+log, "NATIVE_COLD_WT_CONTROL_FILE="+control, "WT_AGENT_ID="+f.job.ChildID, "WT_RUNTIME_ID="+lease.HostRuntime, "NATIVE_COLD_CRASH_PHASE="+phase)
	data, err = ptyCmd.CombinedOutput()
	if phase != "success" {
		if err == nil {
			t.Fatal("private SDK survived injected SIGKILL")
		}
		markerBytes, readErr := os.ReadFile(filepath.Join(root, "crash-phase.json"))
		if readErr != nil {
			t.Fatalf("no exact private crash boundary: %v %s", readErr, data)
		}
		var marker struct {
			Phase    string `json:"phase"`
			PID      int    `json:"pid"`
			Requests int    `json:"requests"`
			Opens    int    `json:"opens"`
			Creates  int    `json:"creates"`
		}
		if readErr = json.Unmarshal(markerBytes, &marker); readErr != nil || marker.Phase != phase || marker.PID <= 0 || syscall.Kill(marker.PID, 0) != syscall.ESRCH {
			t.Fatal("private crash ownership/absence unproven", readErr)
		}
		if marker.Opens != 1 || phase == "open" && marker.Creates != 0 || phase != "model" && marker.Requests != 0 || phase == "model" && marker.Requests != 1 {
			t.Fatalf("unexpected pre-crash SDK/model execution: %s", markerBytes)
		}
		t.Logf("actual private SDK SIGKILL: %s", markerBytes)
		reopenRecoveryFixture(t, &f)
		if _, openErr := f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, lease.HostRuntime); openErr == nil {
			t.Fatal("SIGKILL reopened a consumed SDK writer permit")
		}
		if _, epochErr := f.s.startNativeRecoveryEpoch(r, turn.ID); epochErr == nil {
			t.Fatal("SIGKILL created a second physical epoch")
		}
		if cancelErr := f.s.cancelPreparedNativeRecovery(r); cancelErr == nil {
			t.Fatal("SIGKILL cleared consumed uncertainty")
		}
		old, _, _, oldErr := readDelegationTurnForFixture(f.s, f.job.ID, f.turn.ID)
		if oldErr != nil || old.State != "completed" {
			t.Fatal("SIGKILL changed original settled turn", oldErr)
		}
		pending, _, _, pendingErr := readDelegationTurnForFixture(f.s, f.job.ID, turn.ID)
		wantState := "queued"
		if phase == "claim" || phase == "model" {
			wantState = "claimed"
		}
		if pendingErr != nil || pending.State != wantState {
			t.Fatal("crash falsely completed/failed/forgot dispatch", pendingErr, pending.State)
		}
		return
	}
	if err != nil {
		terminal, _ := os.ReadFile(log)
		t.Fatalf("compiled WT/SDK PTY: %v %s\n%s", err, data, terminal)
	}
	t.Logf("private compiled SDK proof: %s", data)
	proofBytes, proofErr := os.ReadFile(filepath.Join(root, "proof.json"))
	if proofErr != nil {
		t.Fatal(proofErr)
	}
	t.Logf("actual SDK counts/identity: %s", proofBytes)
	current, _, _, err := readDelegationTurnForFixture(f.s, f.job.ID, turn.ID)
	if err != nil || current.State != "completed" {
		t.Fatal("real SDK driver did not commit actual new-turn completion", err, current.State)
	}
	after, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Agents) != len(before.Agents) || len(after.Views) != len(before.Views) {
		t.Fatal("SDK recovery changed logical capacity/views")
	}
	child, err := after.agent(f.job.ChildID)
	if err != nil {
		t.Fatal(err)
	}
	if child.NativeID != expected.NativeID || child.Adapter.File != expected.SessionFile || child.Adapter.Leaf == expected.Leaf {
		t.Fatal("actual SDK replaced native identity or failed to checkpoint new leaf")
	}
	if _, err = f.s.authorizeNativeRecoverySDKOpen(r, f.job.ChildID, lease.HostRuntime); err == nil {
		t.Fatal("completed compiled SDK opening permit replayed")
	}
}
