package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type delegationFixture struct {
	s       *Store
	root    Worktree
	a       DelegationAdmission
	r       DelegationTurnRequest
	job     DelegationJob
	turn    DelegationTurn
	runtime string
}

func newDelegationFixture(t *testing.T) delegationFixture {
	t.Helper()
	s := worktreeTestStore(t)
	w := testRoot(t, s, "delegation")
	if _, err := s.db.Exec(`UPDATE agent_sessions SET runtime='parent-runtime',native_id='parent-native' WHERE id=?`, w.Agents[0].ID); err != nil {
		t.Fatal(err)
	}
	contract := json.RawMessage(`{"version":1,"tools":["read","write","contact_supervisor"],"systemPrompt":"<active_agent name=\"worker\"/>"}`)
	a := DelegationAdmission{Version: 1, DelegationOwner: DelegationOwner{RootID: w.ID, ParentID: w.Agents[0].ID, ParentNativeID: "parent-native", OwnerSessionID: "parent-native", Provider: "wt-interactive"}, RunID: "run-1", StepIndex: 0, Cwd: w.Cwd, Role: "worker", Label: "worker-checkout-a", Contract: contract, ContractDigest: delegationDigest(contract)}
	r := DelegationTurnRequest{RunID: a.RunID, StepIndex: 0, RequestID: "start", ContractDigest: a.ContractDigest, Prompt: "Inspect the local fixture"}
	r.PromptDigest = delegationDigest([]byte(r.Prompt))
	return delegationFixture{s: s, root: w, a: a, r: r, runtime: "child-runtime"}
}
func (f *delegationFixture) reserve(t *testing.T) {
	t.Helper()
	var err error
	f.job, f.turn, err = f.s.reserveDelegation(f.a, "parent-runtime", f.r)
	if err != nil {
		t.Fatal(err)
	}
}
func (f *delegationFixture) bind(t *testing.T) {
	t.Helper()
	if _, err := f.s.db.Exec(`UPDATE agent_sessions SET runtime=? WHERE id=?`, f.runtime, f.job.ChildID); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(f.root.Cwd, "child-native.jsonl")
	adapter := PiSnapshot{Version: 1, File: file}
	if err := f.s.updateAgent(f.root.ID, f.job.ChildID, f.runtime, "idle", "child-native", &adapter); err != nil {
		t.Fatal(err)
	}
	if err := f.s.bindDelegationNative(f.job.ID, f.job.ChildID, f.runtime, "child-native", file); err != nil {
		t.Fatal(err)
	}
	f.job.NativeID, f.job.SessionFile = "child-native", file
}
func (f *delegationFixture) complete(t *testing.T) DelegationResult {
	t.Helper()
	if _, err := f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err != nil {
		t.Fatal(err)
	}
	adapter := PiSnapshot{Version: 1, File: f.job.SessionFile, Persisted: true, Leaf: "observed-leaf"}
	if err := f.s.updateAgent(f.root.ID, f.job.ChildID, f.runtime, "idle", f.job.NativeID, &adapter); err != nil {
		t.Fatal(err)
	}
	result := DelegationResult{State: "completed", NativeID: f.job.NativeID, SessionFile: f.job.SessionFile, Leaf: adapter.Leaf, Output: "Native host result; acceptance remains package-owned"}
	if err := f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, result); err != nil {
		t.Fatal(err)
	}
	return result
}
func nextDelegationTurn(f delegationFixture) DelegationTurnRequest {
	r := f.r
	r.RunID = "resume-run"
	r.RequestID = "follow-up"
	r.PreviousTurnID = f.turn.ID
	r.Prompt = "Continue the exact conversation"
	r.PromptDigest = delegationDigest([]byte(r.Prompt))
	return r
}

func TestDelegationReservationIsAtomicExactAndDoesNotLaunch(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	job, turn, err := f.s.reserveDelegation(f.a, "parent-runtime", f.r)
	if err != nil || job != f.job || turn != f.turn {
		t.Fatalf("reservation retry changed binding: %+v %+v %v", job, turn, err)
	}
	w, err := f.s.Worktree(f.root.ID)
	if err != nil || len(w.Agents) != 2 || len(w.Views) != 2 {
		t.Fatalf("reservation not atomic: %+v %v", w, err)
	}
	child, err := w.agent(job.ChildID)
	if err != nil || child.Name != f.a.Label || child.Parent != f.a.ParentID || child.Creator != f.a.ParentID || child.Cwd != f.a.Cwd || child.Runtime != "" || child.NativeID != "" {
		t.Fatalf("bad unlaunched child: %+v %v", child, err)
	}
	var view View
	for _, candidate := range w.Views {
		if candidate.Target == child.ID {
			view = candidate
		}
	}
	if view.Manager != f.a.ParentID || view.Pane != "" {
		t.Fatalf("bad per-agent view: %+v", view)
	}
	if _, _, err := f.s.viewCommand(w, view); err == nil || !strings.Contains(err.Error(), "ordinary Pi launch is forbidden") {
		t.Fatalf("reserved child escaped to normal launcher: %v", err)
	}
	f.bind(t)
	w, _ = f.s.Worktree(f.root.ID)
	if err = f.s.runAgent(w, child.ID, f.runtime); err == nil || !strings.Contains(err.Error(), "ordinary Pi launch is forbidden") {
		t.Fatalf("internal launch bypassed delegated-host fence: %v", err)
	}
	if _, err = os.Stat(f.job.SessionFile); !os.IsNotExist(err) {
		t.Fatalf("WT wrote native transcript: %v", err)
	}
	tx, _ := f.s.db.Begin()
	_, saved, err := readDelegationJobTx(tx, job.ID)
	tx.Rollback()
	if err != nil || !reflect.DeepEqual(saved, f.a) || delegationDigest(saved.Contract) != saved.ContractDigest {
		t.Fatalf("opaque contract bytes changed during round-trip: %+v %v", saved, err)
	}
}

func TestDelegationAdmissionRejectsBeforeReservation(t *testing.T) {
	for _, name := range []string{"version", "digest", "oversize", "noncompact", "array", "cwd", "role", "step", "provider", "parent", "owner", "prompt", "first-lineage"} {
		t.Run(name, func(t *testing.T) {
			f := newDelegationFixture(t)
			switch name {
			case "version":
				f.a.Version = 2
			case "digest":
				f.a.ContractDigest = strings.Repeat("a", 64)
			case "oversize":
				f.a.Contract = json.RawMessage(`{"data":"` + strings.Repeat("x", delegationContractLimit) + `"}`)
				f.a.ContractDigest = delegationDigest(f.a.Contract)
			case "noncompact":
				f.a.Contract = json.RawMessage(`{ "tools": [] }`)
				f.a.ContractDigest = delegationDigest(f.a.Contract)
			case "array":
				f.a.Contract = json.RawMessage(`[]`)
				f.a.ContractDigest = delegationDigest(f.a.Contract)
			case "cwd":
				f.a.Cwd = t.TempDir()
			case "role":
				f.a.Role = ""
			case "step":
				f.a.StepIndex = -1
			case "provider":
				f.a.Provider = ""
			case "parent":
				f.a.ParentNativeID = "different-native"
			case "owner":
				f.a.OwnerSessionID = "/wrong/parent.jsonl"
			case "prompt":
				f.r.Prompt += " changed"
			case "first-lineage":
				f.r.PreviousTurnID = strings.Repeat("a", 64)
			}
			if _, _, err := f.s.reserveDelegation(f.a, "parent-runtime", f.r); err == nil {
				t.Fatal("invalid admission accepted")
			}
			w, _ := f.s.Worktree(f.root.ID)
			if len(w.Agents) != 1 || len(w.Views) != 1 {
				t.Fatal("rejected admission created child or view")
			}
		})
	}
}

func TestDelegationDistinctAttachedCwdsAndEmptyVersusNullContract(t *testing.T) {
	f := newDelegationFixture(t)
	for i, contract := range []string{`{"tools":[]}`, `{"tools":null}`} {
		cwd := t.TempDir()
		if _, err := f.s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?)`, newID(), f.root.ID, string(rune('a'+i)), cwd); err != nil {
			t.Fatal(err)
		}
		f.a.Cwd = cwd
		f.a.StepIndex = i
		f.a.Label = "worker-" + string(rune('a'+i))
		f.a.Contract = json.RawMessage(contract)
		f.a.ContractDigest = delegationDigest(f.a.Contract)
		f.r.StepIndex = i
		f.r.ContractDigest = f.a.ContractDigest
		f.reserve(t)
	}
	w, _ := f.s.Worktree(f.root.ID)
	if len(w.Agents) != 3 || w.Agents[1].Cwd == w.Agents[2].Cwd {
		t.Fatal("separate attached checkout bindings lost")
	}
	if w.Agents[0].Cwd != f.root.Cwd || w.Agents[0].NativeID != "parent-native" {
		t.Fatal("parent cwd/native identity changed")
	}
}

func TestDelegationRetryCollisionAndCapacityRollback(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	a := f.a
	a.Label = "different-label"
	if _, _, err := f.s.reserveDelegation(a, "parent-runtime", f.r); err == nil {
		t.Fatal("same identity with changed admission accepted")
	}
	r := f.r
	r.Prompt = "Different prompt"
	r.PromptDigest = delegationDigest([]byte(r.Prompt))
	if _, _, err := f.s.reserveDelegation(f.a, "parent-runtime", r); err == nil {
		t.Fatal("same identity with changed prompt accepted")
	}
	for i := 1; i < 7; i++ {
		a = f.a
		a.StepIndex = i
		a.Label = "worker-" + string(rune('a'+i))
		r = f.r
		r.StepIndex = i
		if _, _, err := f.s.reserveDelegation(a, "parent-runtime", r); err != nil {
			t.Fatal(err)
		}
	}
	a.StepIndex++
	r.StepIndex++
	a.Label = "overflow"
	if _, _, err := f.s.reserveDelegation(a, "parent-runtime", r); err == nil {
		t.Fatal("agent cap exceeded")
	}
	var jobs int
	f.s.db.QueryRow(`SELECT count(*) FROM delegation_jobs`).Scan(&jobs)
	w, _ := f.s.Worktree(f.root.ID)
	if jobs != 7 || len(w.Agents) != 8 || len(w.Views) != 8 {
		t.Fatal("failed reservation was not rolled back")
	}
}

func TestDelegationClaimIsExclusiveAcrossDatabaseConnections(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	f.bind(t)
	second, err := Open(os.Getenv("WT_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, s := range []*Store{f.s, second} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			_, err := s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime)
			results <- err
		}(s)
	}
	wg.Wait()
	close(results)
	claimed := 0
	for err := range results {
		if err == nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("got %d dispatch permits", claimed)
	}
}

func TestDelegationNativeAndCompletionFencing(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	if _, err := f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err == nil {
		t.Fatal("unbound host claimed prompt")
	}
	f.bind(t)
	if err := f.s.bindDelegationNative(f.job.ID, f.job.ChildID, f.runtime, "replacement", f.job.SessionFile); err == nil {
		t.Fatal("native binding replaced")
	}
	if err := f.s.updateAgent(f.root.ID, f.job.ChildID, f.runtime, "idle", "replacement", nil); err == nil {
		t.Fatal("normal hook replaced bound native")
	}
	wrongAdapter := PiSnapshot{Version: 1, File: "/wrong/native.jsonl"}
	if err := f.s.updateAgent(f.root.ID, f.job.ChildID, f.runtime, "idle", "", &wrongAdapter); err == nil {
		t.Fatal("checkpoint replaced bound transcript")
	}
	if _, err := f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, "stale"); err == nil {
		t.Fatal("stale host claimed turn")
	}
	result := f.complete(t)
	if err := f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, result); err != nil {
		t.Fatalf("exact completion retry rejected: %v", err)
	}
	result.Output = "forged replacement"
	if err := f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, result); err == nil {
		t.Fatal("terminal result overwritten")
	}
	job, turn, saved, err := f.s.delegationStatus(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID)
	if err != nil || job != f.job || turn.State != "completed" || saved.Output == result.Output {
		t.Fatalf("bad exact result: %+v %+v %+v %v", job, turn, saved, err)
	}
	owner := f.a.DelegationOwner
	owner.Provider = "another-provider"
	if _, _, _, err := f.s.delegationStatus(owner, "parent-runtime", f.job.ID, f.turn.ID); err == nil {
		t.Fatal("cross-provider result lookup allowed")
	}
}

func TestDelegationCompletionRequiresClaimPersistedLeafAndBounds(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	f.bind(t)
	result := DelegationResult{State: "completed", NativeID: f.job.NativeID, SessionFile: f.job.SessionFile, Leaf: "leaf", Output: "result"}
	if err := f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, result); err == nil {
		t.Fatal("unclaimed result accepted")
	}
	if _, err := f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err != nil {
		t.Fatal(err)
	}
	if err := f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, result); err == nil {
		t.Fatal("unpersisted leaf accepted")
	}
	adapter := PiSnapshot{Version: 1, File: f.job.SessionFile, Persisted: true, Leaf: "leaf"}
	if err := f.s.updateAgent(f.root.ID, f.job.ChildID, f.runtime, "idle", "", &adapter); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"leaf", "native", "file", "size", "empty", "error", "failed", "split-utf8"} {
		r := result
		switch name {
		case "leaf":
			r.Leaf = "wrong"
		case "native":
			r.NativeID = "wrong"
		case "file":
			r.SessionFile = "/wrong"
		case "size":
			r.Output = strings.Repeat("x", delegationResultLimit+1)
		case "empty":
			r.Output = " "
		case "error":
			r.Error = "not success"
		case "failed":
			r.State = "failed"
		case "split-utf8":
			r.State = "failed"
			r.Output = "\xc2"
			r.Error = "\xa2"
		}
		if err := f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, r); err == nil {
			t.Fatalf("invalid %s result accepted", name)
		}
		_, turn, _, err := f.s.delegationStatus(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID)
		if err != nil || turn.State != "claimed" {
			t.Fatalf("rejected %s result changed claim: %+v %v", name, turn, err)
		}
	}
	result.State = "failed"
	result.Error = "observed native model failure"
	if err := f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, result); err != nil {
		t.Fatal(err)
	}
}

func TestDelegationRecoveryNeverRedispatchesOrCompletesAnUnobservedTurn(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	f.bind(t)
	if _, err := f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err != nil {
		t.Fatal(err)
	}
	if err := f.s.recoverDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err == nil {
		t.Fatal("live host marked its own claim uncertain")
	}
	if _, err := f.s.db.Exec(`UPDATE agent_sessions SET runtime='replacement-runtime' WHERE id=?`, f.job.ChildID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.recoverDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, "replacement-runtime"); err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []string{f.runtime, "replacement-runtime"} {
		if _, err := f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, runtime); err == nil {
			t.Fatal("uncertain turn replayed")
		}
	}
	_, turn, result, err := f.s.delegationStatus(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID)
	if err != nil || turn.State != "uncertain" || result != nil {
		t.Fatalf("lost uncertain result: %+v %+v %v", turn, result, err)
	}
	if _, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, nextDelegationTurn(f)); err == nil {
		t.Fatal("uncertain conversation silently continued")
	}
}

func TestDelegationContinuationReusesChildAndIsLinearAndBounded(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	f.bind(t)
	if _, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, nextDelegationTurn(f)); err == nil {
		t.Fatal("unfinished conversation accepted follow-up")
	}
	for i := 1; i <= delegationTurnLimit; i++ {
		f.complete(t)
		r := nextDelegationTurn(f)
		r.RequestID = "follow-up-" + strings.Repeat("x", i)
		turn, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, r)
		if i == delegationTurnLimit {
			if err == nil {
				t.Fatal("retained turn cap exceeded")
			}
			break
		}
		if err != nil || turn.Ordinal != i || turn.JobID != f.job.ID {
			t.Fatalf("bad continuation: %+v %v", turn, err)
		}
		retry, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, r)
		if err != nil || retry != turn {
			t.Fatal("continuation dedup failed")
		}
		r.RequestID += "-branch"
		if _, err = f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, r); err == nil {
			t.Fatal("concurrent turn/branch accepted")
		}
		f.turn = turn
	}
	w, _ := f.s.Worktree(f.root.ID)
	if len(w.Agents) != 2 || len(w.Views) != 2 || w.Agents[1].ID != f.job.ChildID || w.Agents[1].Stopped {
		t.Fatal("completion/continuation replaced or stopped the retained conversation")
	}
}

func TestDelegationStartFailureCannotCancelAClaimedChild(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	if err := f.s.failQueuedDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID, "interactive host unavailable"); err != nil {
		t.Fatal(err)
	}
	_, turn, result, err := f.s.delegationStatus(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID)
	if err != nil || turn.State != "failed" || result.Error != "interactive host unavailable" || result.NativeID != "" || result.Leaf != "" {
		t.Fatalf("start failure invented native completion: %+v %+v %v", turn, result, err)
	}
	if err = f.s.failQueuedDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID, result.Error); err != nil {
		t.Fatal("start failure retry not idempotent")
	}
	f.bind(t)
	if _, err = f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err == nil {
		t.Fatal("start-failed turn was replayed")
	}
	next, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, nextDelegationTurn(f))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.claimDelegationTurn(f.job.ID, next.ID, f.job.ChildID, f.runtime); err != nil {
		t.Fatal(err)
	}
	if err = f.s.failQueuedDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, next.ID, "not a cancellation channel"); err == nil {
		t.Fatal("parent cancelled claimed dispatch through start failure")
	}
}

func TestDelegationReservationIsExclusiveAcrossDatabaseConnections(t *testing.T) {
	f := newDelegationFixture(t)
	second, err := Open(os.Getenv("WT_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	errors := make(chan error, 2)
	for _, s := range []*Store{f.s, second} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			job, _, err := s.reserveDelegation(f.a, "parent-runtime", f.r)
			ids <- job.ChildID
			errors <- err
		}(s)
	}
	wg.Wait()
	if first, next := <-ids, <-ids; first == "" || first != next {
		t.Fatal("concurrent reservation created distinct children")
	}
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	w, _ := f.s.Worktree(f.root.ID)
	if len(w.Agents) != 2 || len(w.Views) != 2 {
		t.Fatal("concurrent reservation duplicated child/view")
	}
}

func TestDelegationResultJSONFitsSubprocessEnvelope(t *testing.T) {
	result := DelegationResult{State: "completed", NativeID: strings.Repeat("n", 256), SessionFile: "/" + strings.Repeat("p", 4095), Leaf: strings.Repeat("l", 256), Output: strings.Repeat("\x01", delegationResultLimit)}
	if len(jsonText(result)) >= 1024*1024 {
		t.Fatal("worst-case result escaping exceeds WT subprocess bound")
	}
}

func TestDelegationMigrationFromV4AndReopenPreservesResult(t *testing.T) {
	f := newDelegationFixture(t)
	if _, err := f.s.db.Exec(`DROP TABLE delegation_turns; DROP TABLE delegation_jobs; PRAGMA user_version=4;`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.migrateWorktrees(); err != nil {
		t.Fatal(err)
	}
	f.reserve(t)
	f.bind(t)
	result := f.complete(t)
	second, err := Open(os.Getenv("WT_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	job, turn, saved, err := second.delegationStatus(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID)
	if err != nil || job != f.job || turn.State != "completed" || saved == nil || *saved != result {
		t.Fatalf("reopen lost authoritative result: %+v %+v %+v %v", job, turn, saved, err)
	}
	if _, err = second.db.Exec(`DELETE FROM sessions WHERE name=?`, f.root.Name); err != nil {
		t.Fatalf("root removal failed to cascade bounded jobs: %v", err)
	}
}

func TestDelegationHostStartupFailureSettlesOnlyExactUnpublishedTurn(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "session-create", true: "claim"}[claimed], func(t *testing.T) {
			f := newDelegationFixture(t)
			f.reserve(t)
			f.bind(t)
			if claimed {
				if _, err := f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err != nil {
					t.Fatal(err)
				}
			}
			for _, identity := range [][2]string{{"other", f.runtime}, {f.job.ChildID, "stale"}, {f.job.ChildID, ""}} {
				if err := f.s.failDelegationHostStartup(f.job.ID, f.turn.ID, identity[0], identity[1], "fixture"); err == nil {
					t.Fatal("wrong host settled turn")
				}
			}
			for _, runtimeJSON := range []string{"", `,"runtime":""`} {
				body := `{"job":"` + f.job.ID + `","turn":"` + f.turn.ID + `","child":"` + f.job.ChildID + `","error":"fixture"` + runtimeJSON + `}`
				if _, err := delegationCommand(f.s, "fail-host-startup", strings.NewReader(body)); err == nil {
					t.Fatal("command accepted omitted/empty host runtime")
				}
			}
			_, unchanged, _, err := f.s.delegationStatus(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID)
			if err != nil || (unchanged.State == "claimed") != claimed || unchanged.State == "failed" {
				t.Fatalf("rejected runtime mutated the turn: %+v %v", unchanged, err)
			}
			if err := f.s.failDelegationHostStartup(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, "injected pre-prompt failure"); err != nil {
				t.Fatal(err)
			}
			_, turn, result, err := f.s.delegationStatus(f.a.DelegationOwner, "parent-runtime", f.job.ID, f.turn.ID)
			if err != nil || turn.State != "failed" || result.Output != "" {
				t.Fatalf("startup not terminal: %+v %+v %v", turn, result, err)
			}
			if _, err := f.s.claimDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime); err == nil {
				t.Fatal("failed prompt replayed")
			}
			if _, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, nextDelegationTurn(f)); err != nil {
				t.Fatalf("queued blocker retained: %v", err)
			}
		})
	}
}

func TestOrdinaryPiLaunchRequiresNativeProviderBeforeExtensionStartup(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "native-bootstrap-launch")
	dir := t.TempDir()
	t.Setenv("WT_STATUS_DIR", filepath.Join(dir, "state"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	capture := filepath.Join(dir, "launch-env")
	t.Setenv("WT_NATIVE_TEST_CAPTURE", capture)
	stub := "#!/bin/sh\nprintf '%s\\n' \"$PI_SUBAGENT_REQUIRED_NATIVE_PROVIDER\" \"$WT_ROOT_ID\" \"$WT_AGENT_ID\" \"$WT_RUNTIME_ID\" > \"$WT_NATIVE_TEST_CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "pi"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE agent_sessions SET runtime='launch-runtime' WHERE id=?`, w.Agents[0].ID); err != nil {
		t.Fatal(err)
	}
	w, err := s.Worktree(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.runAgent(w, w.Agents[0].ID, "launch-runtime"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"wt-interactive-v1", w.ID, w.Agents[0].ID, "launch-runtime"}
	if got := strings.Split(strings.TrimSpace(string(body)), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("ordinary launch bypasses native requirement: %v", got)
	}
}
