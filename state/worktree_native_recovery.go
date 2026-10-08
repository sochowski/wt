package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Separate explicit recovery admission. It neither starts a host nor opens a
// native SDK session. The lease is not a prompt/dispatch permit.
type NativeRecoveryRequest struct {
	Version      int             `json:"version"`
	Owner        DelegationOwner `json:"owner"`
	Runtime      string          `json:"runtime"`
	Job          string          `json:"job"`
	PreviousTurn string          `json:"previous_turn"`
	Operation    string          `json:"operation"`
}
type NativeRecoveryLease struct {
	ID             string `json:"id"`
	Job            string `json:"job"`
	Operation      string `json:"operation"`
	State          string `json:"state"`
	NativeID       string `json:"native_id"`
	SessionFile    string `json:"session_file"`
	Leaf           string `json:"leaf"`
	ChildRuntime   string `json:"child_runtime"`
	NewTurnID      string `json:"new_turn_id,omitempty"`
	HostRuntime    string `json:"host_runtime,omitempty"`
	SDKOpened      bool   `json:"sdk_opened,omitempty"`
	ContractDigest string `json:"contract_digest"`
	SidecarDigest  string `json:"sidecar_digest"`
	SourceDigest   string `json:"source_digest"`
	ModelID        string `json:"model_id"`
	Thinking       string `json:"thinking"`
	// An observed lease is not authority to repeat SDK creation/publication.
	Observed bool `json:"observed"`
}

func migrateNativeRecovery(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE native_recovery_leases(
 id TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES delegation_jobs(id) ON DELETE CASCADE,
 request TEXT NOT NULL, lease TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('prepared','claimed','completed','failed','uncertain')));
 CREATE UNIQUE INDEX native_recovery_active ON native_recovery_leases(job_id)
 WHERE state IN ('prepared','claimed','uncertain'); PRAGMA user_version=7;`)
	return err
}

// Legacy finite-budget counters were not checkpointed by the native host. Refuse
// them rather than treating an opaque contract or reconstructed guess as proof.
func nativeRecoveryNeedsBudgetProof(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if (key == "toolBudget" || key == "usageBudget") && child != nil {
				return true
			}
			if nativeRecoveryNeedsBudgetProof(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if nativeRecoveryNeedsBudgetProof(child) {
				return true
			}
		}
	}
	return false
}

func readNativeRecoverySidecar(file string) (map[string]any, error) {
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 {
		return nil, errors.New("missing/nonregular/bounded native host record")
	}
	real, err := filepath.EvalSymlinks(file)
	if err != nil || real != file {
		return nil, errors.New("native host record path is not canonical")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var record map[string]any
	if err = json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Store) prepareNativeRecovery(request NativeRecoveryRequest) (NativeRecoveryLease, error) {
	return s.prepareNativeRecoveryWithProbe(request, func(pid int) error { return syscall.Kill(pid, 0) })
}

// Probe injection is private fixture support; no command can supply liveness.
func (s *Store) prepareNativeRecoveryWithProbe(request NativeRecoveryRequest, probe func(int) error) (NativeRecoveryLease, error) {
	return s.nativeRecoveryAdmissionWithProbe(request, probe, false)
}

// Consume the prepared operation exactly once, rechecking its authoritative
// checkpoint/liveness/protection facts. Claim alone still cannot dispatch a
// prompt, publish a host or change the admitted native identity/runtime.
func (s *Store) claimNativeRecovery(request NativeRecoveryRequest) (NativeRecoveryLease, error) {
	return s.nativeRecoveryAdmissionWithProbe(request, func(pid int) error { return syscall.Kill(pid, 0) }, true)
}

// Only preparation can be cancelled: after claim, SDK opening/publication may
// have happened even if its caller vanished. No lease timeout/reap is safe.
func (s *Store) cancelPreparedNativeRecovery(request NativeRecoveryRequest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	job, err := ownedDelegationJobTx(tx, request.Owner, request.Runtime, request.Job)
	if err != nil {
		return err
	}
	id := delegationDigest([]byte(jsonText([]string{job.ID, request.Operation})))
	var saved, raw, state string
	if err = tx.QueryRow(`SELECT request,lease,state FROM native_recovery_leases WHERE id=?`, id).Scan(&saved, &raw, &state); err != nil {
		return err
	}
	if saved != jsonText(request) {
		return errors.New("native recovery cancellation identity changed")
	}
	if state == "failed" {
		return nil
	}
	if state != "prepared" {
		return errors.New("native recovery claim consumed; publication may be uncertain, cancellation forbidden")
	}
	var lease NativeRecoveryLease
	if err = json.Unmarshal([]byte(raw), &lease); err != nil {
		return err
	}
	lease.State = "failed"
	changed, err := tx.Exec(`UPDATE native_recovery_leases SET state='failed',lease=? WHERE id=? AND state='prepared'`, jsonText(lease), id)
	if err != nil {
		return err
	}
	if count, err := changed.RowsAffected(); err != nil || count != 1 {
		return errors.New("native recovery cancellation lost exclusivity")
	}
	return tx.Commit()
}

func (s *Store) nativeRecoveryAdmissionWithProbe(request NativeRecoveryRequest, probe func(int) error, claim bool) (NativeRecoveryLease, error) {
	empty := NativeRecoveryLease{}
	if request.Version != 1 || !delegationText(request.Operation, 256) || !delegationHash(request.PreviousTurn) || request.Operation == request.PreviousTurn {
		return empty, errors.New("explicit distinct native recovery identity required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	job, err := ownedDelegationJobTx(tx, request.Owner, request.Runtime, request.Job)
	if err != nil {
		return empty, err
	}
	_, admission, err := readDelegationJobTx(tx, job.ID)
	if err != nil {
		return empty, err
	}
	if err = validateDelegationAdmission(admission); err != nil || admission.ContractDigest != job.ContractDigest {
		return empty, errors.New("original native admission integrity mismatch")
	}
	prior, priorRequest, raw, err := readDelegationTurnTx(tx, job.ID, request.PreviousTurn)
	if err != nil {
		return empty, err
	}
	var result DelegationResult
	if json.Unmarshal([]byte(raw), &result) != nil || prior.State != "completed" || result.Unpublished || result.NativeID != job.NativeID || result.SessionFile != job.SessionFile || result.Leaf == "" || request.Operation == priorRequest.RunID || request.Operation == priorRequest.RequestID {
		return empty, errors.New("native recovery requires a genuine settled successful checkpoint")
	}
	var contract any
	if json.Unmarshal(admission.Contract, &contract) != nil || nativeRecoveryNeedsBudgetProof(contract) {
		return empty, errors.New("native recovery lacks authoritative retained finite-budget accounting")
	}
	id := delegationDigest([]byte(jsonText([]string{job.ID, request.Operation})))
	var savedRequest, savedLease, state string
	err = tx.QueryRow(`SELECT request,lease,state FROM native_recovery_leases WHERE id=?`, id).Scan(&savedRequest, &savedLease, &state)
	if err == nil {
		if savedRequest != jsonText(request) {
			return empty, errors.New("native recovery operation changed")
		}
		if err = json.Unmarshal([]byte(savedLease), &empty); err != nil {
			return empty, err
		}
		if !claim {
			empty.State, empty.Observed = state, true
			return empty, nil
		}
		if state != "prepared" {
			return NativeRecoveryLease{}, errors.New("native recovery operation already consumed; SDK startup cannot be replayed")
		}
	} else if err != sql.ErrNoRows {
		return empty, err
	} else if claim {
		return empty, errors.New("native recovery claim requires a prepared operation")
	}
	var attempts int
	if err = tx.QueryRow(`SELECT count(*) FROM native_recovery_leases WHERE job_id=?`, job.ID).Scan(&attempts); err != nil {
		return empty, err
	}
	if !claim && attempts >= delegationTurnLimit {
		return empty, errors.New("native recovery operation capacity exhausted; no eviction/replacement")
	}
	var pending int
	if err = tx.QueryRow(`SELECT count(*) FROM delegation_turns WHERE job_id=? AND state IN ('queued','claimed','uncertain')`, job.ID).Scan(&pending); err != nil {
		return empty, err
	}
	if pending != 0 {
		return empty, errors.New("native recovery cannot take over an active or uncertain turn")
	}
	var suffix, lastOrdinal int
	if err = tx.QueryRow(`SELECT max(ordinal) FROM delegation_turns WHERE job_id=?`, job.ID).Scan(&lastOrdinal); err != nil {
		return empty, err
	}
	if err = tx.QueryRow(`SELECT count(*) FROM delegation_turns WHERE job_id=? AND ordinal>? AND state='failed' AND runtime='' AND json_extract(result,'$.unpublished')=1 AND json_extract(request,'$.previous_turn_id')=?`, job.ID, prior.Ordinal, request.PreviousTurn).Scan(&suffix); err != nil {
		return empty, err
	}
	if suffix != lastOrdinal-prior.Ordinal {
		return empty, errors.New("native checkpoint is not the settled conversation head")
	}
	var native, adapterJSON, parent, childRuntime string
	var stopped bool
	if err = tx.QueryRow(`SELECT native_id,adapter,parent,runtime,stopped FROM agent_sessions WHERE root_id=? AND id=?`, job.RootID, job.ChildID).Scan(&native, &adapterJSON, &parent, &childRuntime, &stopped); err != nil {
		return empty, err
	}
	var adapter PiSnapshot
	if json.Unmarshal([]byte(adapterJSON), &adapter) != nil || stopped || parent != job.ParentID || native != job.NativeID || adapter.Backend == "durable" || !adapter.Persisted || adapter.File != job.SessionFile || adapter.Leaf != result.Leaf {
		return empty, errors.New("native recovery snapshot/owner/stopped identity mismatch")
	}
	var protected int
	if err = tx.QueryRow(`SELECT count(*) FROM views WHERE root_id=? AND kind='agent' AND target=? AND (pinned=1 OR manager<>?)`, job.RootID, job.ChildID, job.ParentID).Scan(&protected); err != nil {
		return empty, err
	}
	if protected != 0 {
		return empty, errors.New("native recovery view is pinned or peer/human managed")
	}
	// Actual view/process placement has additional human-active protection at
	// launch; this primitive grants no permission to move/respawn any view.
	info, err := os.Lstat(job.SessionFile)
	if err != nil || !info.Mode().IsRegular() {
		return empty, errors.New("genuine native session file missing/nonregular")
	}
	sidecar, err := readNativeRecoverySidecar(job.SessionFile + ".native-host.json")
	if err != nil {
		return empty, err
	}
	for key, expected := range map[string]string{"provider": request.Owner.Provider, "ownerSessionId": request.Owner.OwnerSessionID, "parentSessionId": request.Owner.ParentNativeID, "jobId": job.ID, "nativeId": job.NativeID, "sessionFile": job.SessionFile, "turnId": prior.ID, "runId": priorRequest.RunID} {
		if sidecar[key] != expected {
			return empty, fmt.Errorf("native recovery exact %s mismatch", key)
		}
	}
	digest, _ := sidecar["conversationDigest"].(string)
	if digest == "" {
		digest, _ = sidecar["configDigest"].(string)
	}
	pid, ok := sidecar["hostPid"].(float64)
	if sidecar["version"] != float64(1) || digest != job.ContractDigest || !ok || pid <= 0 || pid > 2147483647 || pid != float64(int(pid)) {
		return empty, errors.New("invalid recorded native host/admission identity")
	}
	if !errors.Is(probe(int(pid)), syscall.ESRCH) {
		return empty, errors.New("old native host absence is unproven")
	}
	source, err := os.ReadFile(job.SessionFile)
	if err != nil || len(source) > 16*1024*1024 {
		return NativeRecoveryLease{}, errors.New("native transcript cannot be fingerprinted within its bound")
	}
	sidecarBytes, err := os.ReadFile(job.SessionFile + ".native-host.json")
	if err != nil {
		return NativeRecoveryLease{}, err
	}
	modelID := adapter.Provider + "/" + adapter.Model
	lease := NativeRecoveryLease{ID: id, Job: job.ID, Operation: request.Operation, State: "prepared", NativeID: job.NativeID, SessionFile: job.SessionFile, Leaf: result.Leaf, ChildRuntime: childRuntime, ContractDigest: job.ContractDigest, SidecarDigest: delegationDigest(sidecarBytes), SourceDigest: delegationDigest(source), ModelID: modelID, Thinking: adapter.Thinking}
	if claim {
		if empty.ChildRuntime != childRuntime || empty.NativeID != lease.NativeID || empty.Leaf != lease.Leaf || empty.SessionFile != lease.SessionFile || empty.ContractDigest != lease.ContractDigest || empty.SourceDigest != lease.SourceDigest || empty.SidecarDigest != lease.SidecarDigest || empty.ModelID != lease.ModelID || empty.Thinking != lease.Thinking {
			return NativeRecoveryLease{}, errors.New("native recovery ownership changed after preparation")
		}
		lease.State = "claimed"
		changed, updateErr := tx.Exec(`UPDATE native_recovery_leases SET state='claimed',lease=? WHERE id=? AND state='prepared'`, jsonText(lease), id)
		if updateErr != nil {
			return NativeRecoveryLease{}, updateErr
		}
		if count, countErr := changed.RowsAffected(); countErr != nil || count != 1 {
			return NativeRecoveryLease{}, errors.New("native recovery claim lost exclusivity")
		}
	} else if _, err = tx.Exec(`INSERT INTO native_recovery_leases(id,job_id,request,lease,state) VALUES(?,?,?,?,?)`, id, job.ID, jsonText(request), jsonText(lease), lease.State); err != nil {
		return empty, err
	}
	return lease, tx.Commit()
}
