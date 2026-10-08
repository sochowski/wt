package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Actual package runner, compiled launcher, private tmux and public SDK. The
// historical checkpoint is explicitly settled SDK fixture data, not live state.
func TestNativeRecoveryFullHostedRunner(t *testing.T) {
	for _, phase := range []string{"success", "barrier-kill"} {
		t.Run(phase, func(t *testing.T) { runNativeRecoveryFullHosted(t, phase) })
	}
}
func runNativeRecoveryFullHosted(t *testing.T, phase string) {
	sdk := os.Getenv("WT_NATIVE_SDK_TEST_MODULE")
	fork := os.Getenv("WT_COLD_SDK_FORK")
	if sdk == "" || fork == "" || os.Getenv("WT_NATIVE_TMUX_TEST") != "1" {
		t.Skip("explicit private SDK/fork/tmux opt-ins required")
	}
	f := newDelegationFixture(t)
	root := f.root.Cwd
	private, err := os.MkdirTemp("/private/tmp", "wt-hosted-")
	if err != nil {
		t.Fatal(err)
	}
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(private, "sock")
	t.Cleanup(func() { exec.Command(realTmux, "-S", socket, "kill-server").Run(); os.RemoveAll(private) })
	for _, dir := range []string{"home", "agent", "bin", "state", "config", "original", "cold"} {
		if err = os.MkdirAll(filepath.Join(private, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	shim := fmt.Sprintf("#!/bin/sh\nexec %q -S %q -f /dev/null \"$@\"\n", realTmux, socket)
	if err = os.WriteFile(filepath.Join(private, "bin", "tmux"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(private, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WT_STATUS_DIR", filepath.Join(private, "state"))
	t.Setenv("WT_CONFIG_DIR", filepath.Join(private, "config"))
	t.Setenv("HOME", filepath.Join(private, "home"))
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	sdkRoot := filepath.Dir(filepath.Dir(sdk))
	agentDir := filepath.Join(private, "agent")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != "POST" {
			http.Error(w, "unexpected fixture request", 400)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&body); err != nil {
			http.Error(w, "bad fixture body", 400)
			return
		}
		if !strings.Contains(jsonText(body["messages"]), "Only the new hosted instruction") {
			http.Error(w, "missing new task", 400)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"fixture","object":"chat.completion.chunk","created":1,"model":"native-host","choices":[{"index":0,"delta":{"content":"Hosted cold result"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	models := map[string]any{"providers": map[string]any{"synthetic": map[string]any{"baseUrl": server.URL + "/v1", "apiKey": "fixture-only", "models": []any{map[string]any{"id": "native-host", "name": "native-host", "api": "openai-completions", "reasoning": false, "input": []string{"text"}, "contextWindow": 128000, "maxTokens": 512, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}}}}}
	if err = os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(jsonText(models)), 0600); err != nil {
		t.Fatal(err)
	}
	seed := `const pi=await import(process.env.WT_NATIVE_SDK_TEST_MODULE);const root=process.argv[1];const m=pi.SessionManager.create(root,root+'/sessions');m.appendModelChange('synthetic','native-host');m.appendThinkingLevelChange('off');m.appendMessage({role:'user',content:'Settled historical SDK fixture, never redispatch',timestamp:Date.now()});m.appendMessage({role:'assistant',content:[{type:'text',text:'Historical fixture complete'}],api:'openai-completions',provider:'synthetic',model:'native-host',usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:'stop',timestamp:Date.now()});console.log(JSON.stringify({nativeId:m.getSessionId(),sessionFile:m.getSessionFile(),leaf:m.getLeafId(),cwd:m.getCwd()}));`
	out, err := exec.Command("node", "--input-type=module", "--eval", seed, root).CombinedOutput()
	if err != nil {
		t.Fatalf("genesis: %v %s", err, out)
	}
	var expected struct {
		NativeID    string `json:"nativeId"`
		SessionFile string `json:"sessionFile"`
		Leaf        string `json:"leaf"`
		Cwd         string `json:"cwd"`
	}
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err, string(out))
	}
	step := map[string]any{"agent": "fixture-reviewer", "task": "Historical fixture admission", "parentSessionId": f.a.ParentNativeID, "cwd": root, "context": "fresh", "model": "synthetic/native-host", "thinking": "off", "tools": []string{"read"}, "extensions": []string{}, "allowNestedSubagents": false, "inheritProjectContext": false, "inheritGlobalContext": false, "inheritSkills": false, "waitToolEnabled": false, "completionGuard": false, "systemPrompt": "Private read-only hosted fixture. Preserve its native identity."}
	original := map[string]any{"id": f.a.RunID, "sessionId": f.a.OwnerSessionID, "cwd": root, "steps": []any{step}, "placeholder": "{previous}", "resultPath": filepath.Join(private, "original", "result.json"), "asyncDir": filepath.Join(private, "original"), "artifactConfig": map[string]any{"enabled": false}, "share": false, "piPackageRoot": sdkRoot}
	f.a.Provider = "wt-interactive-v1"
	f.a.Role = "fixture-reviewer"
	f.a.Contract = json.RawMessage(jsonText(original))
	f.a.ContractDigest = delegationDigest(f.a.Contract)
	f.r.ContractDigest = f.a.ContractDigest
	f.reserve(t)
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
	dead := exec.Command("/usr/bin/true")
	if err = dead.Run(); err != nil {
		t.Fatal(err)
	}
	previous := map[string]any{"version": 1, "provider": f.a.Provider, "ownerSessionId": f.a.OwnerSessionID, "parentSessionId": f.a.ParentNativeID, "jobId": f.job.ID, "nativeId": expected.NativeID, "sessionFile": expected.SessionFile, "turnId": f.turn.ID, "runId": f.r.RunID, "configDigest": f.job.ContractDigest, "controlPath": filepath.Join(private, "original", "native-control.json"), "hostPid": dead.Process.Pid}
	if err = os.WriteFile(expected.SessionFile+".native-host.json", []byte(jsonText(previous)), 0600); err != nil {
		t.Fatal(err)
	}
	master, err := tmux("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", f.root.Name, "sleep 120")
	if err != nil {
		t.Fatal(err, master)
	}
	if _, err = tmux("set-option", "-t", "="+f.root.Name, "@wt-root", f.root.ID); err != nil {
		t.Fatal(err)
	}
	w, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	var childView View
	for _, v := range w.Views {
		if v.Target == f.a.ParentID {
			if err = f.s.bindView(w, v, master); err != nil {
				t.Fatal(err)
			}
		}
		if v.Target == f.job.ChildID {
			childView = v
		}
	}
	pane, err := tmux("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "="+w.Name, "sleep 120")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.bindView(w, childView, pane); err != nil {
		t.Fatal(err)
	}
	if _, err = tmux("set-option", "-p", "-t", pane, "remain-on-exit", "on"); err != nil {
		t.Fatal(err)
	}
	if _, err = tmux("respawn-pane", "-k", "-t", pane, "/usr/bin/true"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		d, _ := tmux("display-message", "-p", "-t", pane, "#{pane_dead}")
		if d == "1" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	binary := filepath.Join(private, "bin", "wt-state")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err = build.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	env := append(os.Environ(), "WT_STATE="+binary, "WT_ROOT_ID="+w.ID, "WT_AGENT_ID="+f.a.ParentID, "WT_RUNTIME_ID=parent-runtime")
	call := func(op string, input any) ([]byte, error) {
		cmd := exec.Command(binary, "worktree", "_delegation", op)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(jsonText(input))
		return cmd.CombinedOutput()
	}
	r := NativeRecoveryRequest{Version: 1, Owner: f.a.DelegationOwner, Runtime: "parent-runtime", Job: f.job.ID, PreviousTurn: f.turn.ID, Operation: "hosted-cold-operation"}
	for _, op := range []string{"prepare-cold", "claim-cold"} {
		if out, err = call(op, r); err != nil {
			t.Fatalf("%s: %v %s", op, err, out)
		}
	}
	var lease NativeRecoveryLease
	if err = json.Unmarshal(out, &lease); err != nil {
		t.Fatal(err)
	}
	next := nextDelegationTurn(f)
	next.RunID = r.Operation
	next.RequestID = r.Operation
	next.Prompt = "Task: Only the new hosted instruction"
	next.PromptDigest = delegationDigest([]byte(next.Prompt))
	out, err = call("queue-cold", map[string]any{"recovery": r, "request": next})
	if err != nil {
		t.Fatal(err, string(out))
	}
	var turn DelegationTurn
	if err = json.Unmarshal(out, &turn); err != nil {
		t.Fatal(err)
	}
	config := map[string]any{}
	for k, v := range original {
		config[k] = v
	}
	newStep := map[string]any{}
	for k, v := range step {
		newStep[k] = v
	}
	newStep["task"] = "Only the new hosted instruction"
	newStep["sessionFile"] = expected.SessionFile
	asyncDir := filepath.Join(private, "cold")
	config["steps"] = []any{newStep}
	config["id"] = r.Operation
	config["asyncDir"] = asyncDir
	config["resultPath"] = filepath.Join(asyncDir, "result.json")
	config["nativeContinuation"] = previous
	config["nativeColdRecovery"] = true
	driver, err := filepath.Abs("../config/pi-wt/native-provider.js")
	if err != nil {
		t.Fatal(err)
	}
	binding := map[string]any{"version": 1, "provider": f.a.Provider, "ownerSessionId": f.a.OwnerSessionID, "parentSessionId": f.a.ParentNativeID, "runId": r.Operation, "jobId": f.job.ID, "turnId": turn.ID, "previousTurnId": f.turn.ID, "configDigest": delegationDigest([]byte(jsonText(config))), "conversationDigest": f.job.ContractDigest, "nativeId": expected.NativeID, "sessionFile": expected.SessionFile, "driverModule": driver, "controlPath": filepath.Join(asyncDir, "native-control.json"), "coldRecovery": map[string]any{"version": 1, "leaseId": lease.ID, "operation": r.Operation, "leaf": lease.Leaf, "cwd": root, "sourceDigest": lease.SourceDigest, "sidecarDigest": lease.SidecarDigest, "modelId": lease.ModelID, "thinking": lease.Thinking, "request": r}}
	config["nativeExecution"] = binding
	config["runnerProcessInstanceId"] = "private-hosted-instance"
	config["launchBarrierToken"] = "private-hosted-barrier"
	cfg := filepath.Join(asyncDir, "launch.json")
	if err = os.WriteFile(cfg, []byte(jsonText(config)), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(asyncDir, "native-runner.json"), []byte(jsonText(config)), 0600); err != nil {
		t.Fatal(err)
	}
	aliasesScript := fmt.Sprintf(`import {resolveHostPeerAliases} from %q;console.log(JSON.stringify(resolveHostPeerAliases(%q)));`, filepath.Join(fork, "src/runs/background/runner-aliases.ts"), sdkRoot)
	out, err = exec.Command("node", "--experimental-strip-types", "--input-type=module", "--eval", aliasesScript).CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var aliases struct {
		Aliases map[string]string `json:"aliases"`
		Missing []string          `json:"missing"`
	}
	if err = json.Unmarshal(out, &aliases); err != nil || len(aliases.Missing) > 0 {
		t.Fatal(err, string(out))
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	childEnv := map[string]string{}
	for _, value := range env {
		kv := strings.SplitN(value, "=", 2)
		if len(kv) == 2 {
			childEnv[kv[0]] = kv[1]
		}
	}
	childEnv["PI_SUBAGENT_REQUIRED_NATIVE_PROVIDER"] = f.a.Provider
	childEnv["JITI_ALIAS"] = jsonText(aliases.Aliases)
	childEnv["PI_CODING_AGENT_PACKAGE_ROOT"] = sdkRoot
	launch := delegationHostLaunch{Owner: r.Owner, Runtime: r.Runtime, Job: r.Job, Turn: turn.ID, Command: node, Args: []string{filepath.Join(fork, "node_modules/jiti/lib/jiti-cli.mjs"), filepath.Join(fork, "src/runs/background/subagent-runner.ts"), cfg}, Env: childEnv, Cwd: root, Recovery: &r}
	out, err = call("launch", launch)
	if err != nil {
		capture, _ := tmux("capture-pane", "-p", "-t", pane)
		t.Fatalf("host launch: %v %s\n%s", err, out, capture)
	}
	var launched struct {
		PID  int    `json:"pid"`
		Pane string `json:"pane"`
	}
	if err = json.Unmarshal(out, &launched); err != nil || launched.PID <= 0 || launched.Pane != pane {
		t.Fatal("launcher replaced original view/pane", err, string(out))
	}
	t.Cleanup(func() { syscall.Kill(launched.PID, syscall.SIGKILL) })
	readyFile := filepath.Join(asyncDir, "runner-startup-ready.json")
	readyDeadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(readyDeadline) {
		if _, err = os.Stat(readyFile); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	readyBytes, readyErr := os.ReadFile(readyFile)
	if readyErr != nil {
		capture, _ := tmux("capture-pane", "-p", "-S", "-120", "-t", pane)
		t.Fatalf("actual runner never reached barrier: %v\n%s", readyErr, capture)
	}
	var ready struct {
		Version  int    `json:"version"`
		RunID    string `json:"runId"`
		Instance string `json:"runnerProcessInstanceId"`
		PID      int    `json:"pid"`
		Token    string `json:"token"`
	}
	if err = json.Unmarshal(readyBytes, &ready); err != nil || ready.Version != 1 || ready.PID != launched.PID || ready.RunID != r.Operation || ready.Instance != "private-hosted-instance" || ready.Token != "private-hosted-barrier" {
		t.Fatal("startup readiness identity mismatch", err)
	}
	source, err := os.ReadFile(expected.SessionFile)
	if err != nil || delegationDigest(source) != lease.SourceDigest {
		t.Fatal("SDK wrote transcript before startup barrier", err)
	}
	if requests.Load() != 0 {
		t.Fatal("model crossed the actual observed startup barrier")
	}
	if phase == "barrier-kill" {
		if err = syscall.Kill(launched.PID, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 100; i++ {
			if syscall.Kill(launched.PID, 0) == syscall.ESRCH {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if syscall.Kill(launched.PID, 0) != syscall.ESRCH {
			t.Fatal("private killed runner absence unproven")
		}
		reopenRecoveryFixture(t, &f)
		if _, err = call("launch", launch); err == nil {
			t.Fatal("barrier death relaunched consumed physical epoch")
		}
		if requests.Load() != 0 {
			t.Fatal("barrier death dispatched a model request")
		}
		if _, err = os.Stat(filepath.Join(asyncDir, "native-publication-observed.json")); !os.IsNotExist(err) {
			t.Fatal("barrier readiness falsely published dispatch")
		}
		queued, _, _, readErr := readDelegationTurnForFixture(f.s, f.job.ID, turn.ID)
		if readErr != nil || queued.State != "queued" {
			t.Fatal("barrier death invented turn completion", readErr, queued.State)
		}
		t.Logf("actual hosted barrier SIGKILL retained unpublished epoch: pid=%d native=%s pane=%s", launched.PID, expected.NativeID, pane)
		return
	}
	if err = os.WriteFile(filepath.Join(asyncDir, "runner-startup-proceed.json"), []byte(`{"action":"proceed","token":"private-hosted-barrier"}`), 0600); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(asyncDir, "result.json")
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = os.Stat(result); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	proof, err := os.ReadFile(result)
	if err != nil {
		capture, _ := tmux("capture-pane", "-p", "-S", "-120", "-t", pane)
		t.Fatalf("runner result: %v\n%s", err, capture)
	}
	var outcome map[string]any
	if err = json.Unmarshal(proof, &outcome); err != nil || outcome["success"] != true {
		t.Fatalf("hosted runner failed: %v %s", err, proof)
	}
	if requests.Load() != 1 {
		t.Fatalf("dispatch count %d", requests.Load())
	}
	current, _, _, err := readDelegationTurnForFixture(f.s, f.job.ID, turn.ID)
	if err != nil || current.State != "completed" {
		t.Fatal("WT actual completion missing", err, current.State)
	}
	observed, err := os.ReadFile(filepath.Join(asyncDir, "native-publication-observed.json"))
	if err != nil {
		t.Fatal("no actual runner publication receipt", err)
	}
	lock, lockErr := lockFile(nodeLockPath(f.job.ChildID))
	if lockErr == nil {
		lock.Close()
		t.Fatal("retained host lost exclusive original node lock")
	}
	if _, err = call("launch", launch); err == nil {
		t.Fatal("hosted recovery launch repeated")
	}
	after, err := f.s.Worktree(f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := after.agent(f.job.ChildID)
	if err != nil || a.NativeID != expected.NativeID || a.Adapter.File != expected.SessionFile || a.Adapter.Leaf == expected.Leaf {
		t.Fatal("hosted SDK identity/checkpoint mismatch", err)
	}
	if len(after.Agents) != len(w.Agents) || len(after.Views) != len(w.Views) {
		t.Fatal("hosted recovery replaced logical resources")
	}
	t.Logf("full hosted runner passed: native=%s pid=%d pane=%s requests=%d receipt=%s", expected.NativeID, launched.PID, pane, requests.Load(), observed)
}
