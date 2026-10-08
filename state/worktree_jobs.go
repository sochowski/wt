package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	delegationContractLimit = 256 * 1024
	delegationPromptLimit   = 128 * 1024
	delegationResultLimit   = 128 * 1024
	delegationTurnLimit     = 64
)

// The store binds opaque package-admitted native contracts; it does not interpret
// profiles or attest tool enforcement. Only the interactive host may execute them.
// No operation in this file starts a process or writes a native transcript.
func migrateDelegationJobs(tx *sql.Tx) error {
	_, err := tx.Exec(`
 CREATE TABLE delegation_jobs(
 id TEXT PRIMARY KEY, root_id TEXT NOT NULL REFERENCES roots(id) ON DELETE CASCADE,
 parent_id TEXT NOT NULL, child_id TEXT NOT NULL UNIQUE,
 parent_native_id TEXT NOT NULL, owner_session_id TEXT NOT NULL, provider TEXT NOT NULL,
 admission TEXT NOT NULL, contract_digest TEXT NOT NULL,
 native_id TEXT NOT NULL DEFAULT '', session_file TEXT NOT NULL DEFAULT '',
 FOREIGN KEY(root_id,parent_id) REFERENCES agent_sessions(root_id,id),
 FOREIGN KEY(root_id,child_id) REFERENCES agent_sessions(root_id,id));
 CREATE INDEX delegation_jobs_root ON delegation_jobs(root_id);
 CREATE TABLE delegation_turns(
 id TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES delegation_jobs(id) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL, request TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','claimed','completed','failed','uncertain')),
 runtime TEXT NOT NULL DEFAULT '', result TEXT NOT NULL DEFAULT '',
 UNIQUE(job_id,ordinal));
 CREATE UNIQUE INDEX delegation_active_turn ON delegation_turns(job_id) WHERE state IN ('queued','claimed','uncertain');
 PRAGMA user_version=5;`)
	return err
}

// DelegationOwner is checked against the current WT runtime and native parent.
// The owner key is the package's persisted session path or its native session ID.
// Runtime tokens fence stale processes, not malicious code with the same UID.
type DelegationOwner struct {
	RootID         string `json:"root_id"`
	ParentID       string `json:"parent_id"`
	ParentNativeID string `json:"parent_native_id"`
	OwnerSessionID string `json:"owner_session_id"`
	Provider       string `json:"provider"`
}

type DelegationAdmission struct {
	Version int `json:"version"`
	DelegationOwner
	RunID          string          `json:"run_id"`
	StepIndex      int             `json:"step_index"`
	Cwd            string          `json:"cwd"`
	Role           string          `json:"role"`
	Label          string          `json:"label"`
	ContractDigest string          `json:"contract_digest"`
	Contract       json.RawMessage `json:"contract"`
}

type DelegationJob struct {
	ID             string `json:"id"`
	RootID         string `json:"root_id"`
	ParentID       string `json:"parent_id"`
	ChildID        string `json:"child_id"`
	ContractDigest string `json:"contract_digest"`
	NativeID       string `json:"native_id"`
	SessionFile    string `json:"session_file"`
}

// Each continuation names the host's preceding completed/failed turn. Only an
// explicitly unpublished suffix may be skipped; this never branches/replays.
type DelegationTurnRequest struct {
	RunID          string `json:"run_id"`
	StepIndex      int    `json:"step_index"`
	RequestID      string `json:"request_id"`
	PreviousTurnID string `json:"previous_turn_id"`
	ContractDigest string `json:"contract_digest"`
	PromptDigest   string `json:"prompt_digest"`
	Prompt         string `json:"prompt"`
}

type DelegationTurn struct {
	ID      string `json:"id"`
	JobID   string `json:"job_id"`
	Ordinal int    `json:"ordinal"`
	State   string `json:"state"`
	Runtime string `json:"runtime"`
}

// Completion is a bounded host observation, not an agent-callable receipt or an
// acceptance verdict. The package must still validate the native result/evidence.
type DelegationResult struct {
	Unpublished bool   `json:"unpublished,omitempty"`
	State       string `json:"state"`
	NativeID    string `json:"native_id"`
	SessionFile string `json:"session_file"`
	Leaf        string `json:"leaf"`
	Output      string `json:"output"`
	Error       string `json:"error"`
}

func delegationDigest(value []byte) string {
	h := sha256.Sum256(value)
	return hex.EncodeToString(h[:])
}
func delegationText(value string, max int) bool {
	return value != "" && len(value) <= max && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}
func delegationHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}
func validateDelegationAdmission(a DelegationAdmission) error {
	if a.Version != 1 || a.StepIndex < 0 || !delegationText(a.RunID, 256) || !delegationText(a.Role, 128) || !safeName.MatchString(a.Label) || a.Label == "human" {
		return errors.New("invalid delegation version, run, role, step or peer label")
	}
	for _, v := range []string{a.RootID, a.ParentID, a.ParentNativeID, a.Provider} {
		if !delegationText(v, 256) {
			return errors.New("missing or invalid delegation identity")
		}
	}
	if !delegationText(a.OwnerSessionID, 4096) || !filepath.IsAbs(a.Cwd) || len(a.Cwd) > 4096 {
		return errors.New("invalid delegation owner or cwd")
	}
	if len(a.Contract) > delegationContractLimit || !utf8.Valid(a.Contract) || !json.Valid(a.Contract) || len(a.Contract) < 2 || a.Contract[0] != '{' || !delegationHash(a.ContractDigest) || delegationDigest(a.Contract) != a.ContractDigest {
		return errors.New("delegation requires a bounded JSON object and its exact SHA-256 digest")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, a.Contract); err != nil || !bytes.Equal(compact.Bytes(), a.Contract) {
		return errors.New("delegation contract must use compact JSON so its exact digest survives persistence")
	}
	return nil
}
func validateDelegationTurn(r DelegationTurnRequest) error {
	if !delegationText(r.RunID, 256) || r.StepIndex < 0 || !delegationText(r.RequestID, 256) || !delegationHash(r.ContractDigest) || !delegationHash(r.PromptDigest) || !delegationText(r.Prompt, delegationPromptLimit) || delegationDigest([]byte(r.Prompt)) != r.PromptDigest {
		return errors.New("invalid delegation turn identity or bounded prompt digest")
	}
	if r.PreviousTurnID != "" && !delegationHash(r.PreviousTurnID) {
		return errors.New("invalid previous delegation turn identity")
	}
	return nil
}
func delegationAdmissionJSON(a DelegationAdmission) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	// Admission validation has already checked the only raw JSON member.
	_ = encoder.Encode(a)
	return strings.TrimSuffix(out.String(), "\n")
}
func delegationJobID(a DelegationAdmission) string {
	return delegationDigest([]byte(jsonText([]any{a.DelegationOwner, a.RunID, a.StepIndex})))
}
func delegationTurnID(job string, r DelegationTurnRequest) string {
	return delegationDigest([]byte(jsonText([]any{job, r.RunID, r.StepIndex, r.RequestID})))
}
func delegationOwnerTx(tx *sql.Tx, o DelegationOwner, runtime string) error {
	if err := liveActorTx(tx, o.RootID, o.ParentID, runtime); err != nil {
		return err
	}
	var native, file string
	if err := tx.QueryRow(`SELECT native_id,coalesce(json_extract(adapter,'$.file'),'') FROM agent_sessions WHERE root_id=? AND id=?`, o.RootID, o.ParentID).Scan(&native, &file); err != nil {
		return err
	}
	if native == "" || native != o.ParentNativeID || o.OwnerSessionID != native && (file == "" || o.OwnerSessionID != file) {
		return errors.New("delegation native parent/owner mismatch")
	}
	return nil
}
func readDelegationJobTx(tx *sql.Tx, id string) (DelegationJob, DelegationAdmission, error) {
	var j DelegationJob
	var a DelegationAdmission
	var raw string
	err := tx.QueryRow(`SELECT id,root_id,parent_id,child_id,contract_digest,native_id,session_file,admission FROM delegation_jobs WHERE id=?`, id).Scan(&j.ID, &j.RootID, &j.ParentID, &j.ChildID, &j.ContractDigest, &j.NativeID, &j.SessionFile, &raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &a)
	}
	return j, a, err
}
func ownedDelegationJobTx(tx *sql.Tx, owner DelegationOwner, runtime, id string) (DelegationJob, error) {
	var j DelegationJob
	var saved DelegationOwner
	// Keep the potentially large admission body off the polling path.
	err := tx.QueryRow(`SELECT id,root_id,parent_id,child_id,contract_digest,native_id,session_file,parent_native_id,owner_session_id,provider FROM delegation_jobs WHERE id=?`, id).Scan(&j.ID, &j.RootID, &j.ParentID, &j.ChildID, &j.ContractDigest, &j.NativeID, &j.SessionFile, &saved.ParentNativeID, &saved.OwnerSessionID, &saved.Provider)
	saved.RootID, saved.ParentID = j.RootID, j.ParentID
	if err == nil && saved != owner {
		err = errors.New("delegation parent/provider/job identity mismatch")
	}
	if err == nil {
		err = delegationOwnerTx(tx, owner, runtime)
	}
	return j, err
}
func readDelegationTurnTx(tx *sql.Tx, job, id string) (DelegationTurn, DelegationTurnRequest, string, error) {
	var t DelegationTurn
	var r DelegationTurnRequest
	var raw, result string
	err := tx.QueryRow(`SELECT id,job_id,ordinal,state,runtime,request,result FROM delegation_turns WHERE job_id=? AND id=?`, job, id).Scan(&t.ID, &t.JobID, &t.Ordinal, &t.State, &t.Runtime, &raw, &result)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &r)
	}
	return t, r, result, err
}

// Reservation atomically creates the job, unlaunched child, view and first turn.
// A retry can observe this reservation but cannot acquire dispatch authority.
func (s *Store) reserveDelegation(a DelegationAdmission, runtime string, r DelegationTurnRequest) (DelegationJob, DelegationTurn, error) {
	var emptyJob DelegationJob
	var emptyTurn DelegationTurn
	if err := validateDelegationAdmission(a); err != nil {
		return emptyJob, emptyTurn, err
	}
	if err := validateDelegationTurn(r); err != nil {
		return emptyJob, emptyTurn, err
	}
	if r.PreviousTurnID != "" || r.RunID != a.RunID || r.StepIndex != a.StepIndex || r.ContractDigest != a.ContractDigest {
		return emptyJob, emptyTurn, errors.New("first turn does not match admitted job")
	}
	cwd, err := canonicalDir(a.Cwd)
	if err != nil || cwd != a.Cwd {
		return emptyJob, emptyTurn, errors.New("delegation cwd must be an existing canonical directory")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return emptyJob, emptyTurn, err
	}
	defer tx.Rollback()
	if err = delegationOwnerTx(tx, a.DelegationOwner, runtime); err != nil {
		return emptyJob, emptyTurn, err
	}
	id := delegationJobID(a)
	j, old, err := readDelegationJobTx(tx, id)
	if err == nil {
		if delegationAdmissionJSON(old) != delegationAdmissionJSON(a) {
			return emptyJob, emptyTurn, errors.New("delegation admission changed; refusing replacement")
		}
		t, prior, _, e := readDelegationTurnTx(tx, id, delegationTurnID(id, r))
		if e == nil && prior != r {
			e = errors.New("delegation first-turn request changed")
		}
		return j, t, e
	}
	if err != sql.ErrNoRows {
		return emptyJob, emptyTurn, err
	}
	var allowed, count int
	if err = tx.QueryRow(`SELECT count(*) FROM roots r JOIN sessions s ON r.name=s.name WHERE r.id=? AND (s.workspace_path=? OR EXISTS(SELECT 1 FROM checkouts c WHERE c.root_id=r.id AND c.path=?))`, a.RootID, cwd, cwd).Scan(&allowed); err != nil {
		return emptyJob, emptyTurn, err
	}
	if allowed != 1 {
		return emptyJob, emptyTurn, errors.New("delegation cwd must be the session home or an explicitly attached checkout")
	}
	if err = tx.QueryRow(`SELECT count(*) FROM agent_sessions WHERE root_id=?`, a.RootID).Scan(&count); err != nil {
		return emptyJob, emptyTurn, err
	}
	if count >= 8 {
		return emptyJob, emptyTurn, errors.New("worktree limit: 8 agents (including retained delegated children)")
	}
	j = DelegationJob{ID: id, RootID: a.RootID, ParentID: a.ParentID, ChildID: newID(), ContractDigest: a.ContractDigest}
	if _, err = tx.Exec(`INSERT INTO agent_sessions(id,name,root_id,parent,creator,cwd) VALUES(?,?,?,?,?,?)`, j.ChildID, a.Label, a.RootID, a.ParentID, a.ParentID, cwd); err != nil {
		return emptyJob, emptyTurn, err
	}
	if _, err = tx.Exec(`INSERT INTO views(id,root_id,kind,target,manager) VALUES(?,?,'agent',?,?)`, newID(), a.RootID, j.ChildID, a.ParentID); err != nil {
		return emptyJob, emptyTurn, err
	}
	if _, err = tx.Exec(`INSERT INTO delegation_jobs(id,root_id,parent_id,child_id,parent_native_id,owner_session_id,provider,admission,contract_digest) VALUES(?,?,?,?,?,?,?,?,?)`, id, a.RootID, a.ParentID, j.ChildID, a.ParentNativeID, a.OwnerSessionID, a.Provider, delegationAdmissionJSON(a), a.ContractDigest); err != nil {
		return emptyJob, emptyTurn, err
	}
	t := DelegationTurn{ID: delegationTurnID(id, r), JobID: id, Ordinal: 0, State: "queued"}
	if _, err = tx.Exec(`INSERT INTO delegation_turns(id,job_id,ordinal,request,state) VALUES(?,?,?,?,?)`, t.ID, id, t.Ordinal, jsonText(r), t.State); err != nil {
		return emptyJob, emptyTurn, err
	}
	return j, t, tx.Commit()
}

func (s *Store) queueDelegationTurn(owner DelegationOwner, runtime, job string, r DelegationTurnRequest) (DelegationTurn, error) {
	return s.queueDelegationTurnWithColdRecovery(owner, runtime, job, r, nil)
}

func (s *Store) queueDelegationTurnWithColdRecovery(owner DelegationOwner, runtime, job string, r DelegationTurnRequest, cold *NativeRecoveryRequest) (DelegationTurn, error) {
	var empty DelegationTurn
	if err := validateDelegationTurn(r); err != nil {
		return empty, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	j, err := ownedDelegationJobTx(tx, owner, runtime, job)
	if err != nil {
		return empty, err
	}
	if r.ContractDigest != j.ContractDigest || j.NativeID == "" || r.PreviousTurnID == "" {
		return empty, errors.New("continuation requires the admitted contract and exact bound native conversation")
	}
	var recovering int
	if err = tx.QueryRow(`SELECT count(*) FROM native_recovery_leases WHERE job_id=? AND state IN ('prepared','claimed','uncertain')`, job).Scan(&recovering); err != nil {
		return empty, err
	}
	var coldID string
	var coldLease NativeRecoveryLease
	if cold != nil {
		if cold.Owner != owner || cold.Runtime != runtime || cold.Job != job || cold.PreviousTurn != r.PreviousTurnID || cold.Operation != r.RunID || cold.Operation != r.RequestID {
			return empty, errors.New("cold native turn does not match the explicit recovery operation")
		}
		coldID = delegationDigest([]byte(jsonText([]string{job, cold.Operation})))
		var saved, raw, leaseState string
		if err = tx.QueryRow(`SELECT request,lease,state FROM native_recovery_leases WHERE id=?`, coldID).Scan(&saved, &raw, &leaseState); err != nil {
			return empty, err
		}
		if saved != jsonText(*cold) || leaseState != "claimed" || recovering != 1 || json.Unmarshal([]byte(raw), &coldLease) != nil || coldLease.NewTurnID != "" {
			return empty, errors.New("cold native turn requires its exact unused claimed recovery lease")
		}
	} else if recovering != 0 {
		return empty, errors.New("native conversation has an exclusive cold recovery lease; warm dispatch is blocked")
	}
	id := delegationTurnID(job, r)
	t, prior, _, err := readDelegationTurnTx(tx, job, id)
	if err == nil {
		if prior != r {
			return empty, errors.New("delegation continuation request changed")
		}
		return t, nil
	}
	if err != sql.ErrNoRows {
		return empty, err
	}
	var lastID, state string
	var ordinal int
	if err = tx.QueryRow(`SELECT id,state,ordinal FROM delegation_turns WHERE job_id=? ORDER BY ordinal DESC LIMIT 1`, job).Scan(&lastID, &state, &ordinal); err != nil {
		return empty, err
	}
	previous, _, rawResult, err := readDelegationTurnTx(tx, job, r.PreviousTurnID)
	if err != nil {
		return empty, err
	}
	var previousResult DelegationResult
	if json.Unmarshal([]byte(rawResult), &previousResult) != nil || previousResult.Unpublished || previous.State != "completed" && previous.State != "failed" {
		return empty, errors.New("previous turn is not a terminal published host turn")
	}
	if cold != nil && previous.State != "completed" {
		return empty, errors.New("cold native turn requires a successful settled predecessor")
	}
	if lastID != r.PreviousTurnID {
		var skipped int
		err = tx.QueryRow(`SELECT count(*) FROM delegation_turns WHERE job_id=? AND ordinal>? AND ordinal<=?
            AND state='failed' AND runtime='' AND json_extract(result,'$.unpublished')=1
            AND json_extract(request,'$.previous_turn_id')=?`, job, previous.Ordinal, ordinal, r.PreviousTurnID).Scan(&skipped)
		if err != nil {
			return empty, err
		}
		if ordinal <= previous.Ordinal || skipped != ordinal-previous.Ordinal {
			return empty, errors.New("previous turn is not the terminal conversation head; refusing replay or branching")
		}
	}
	if ordinal+1 >= delegationTurnLimit {
		return empty, fmt.Errorf("delegation conversation limit: %d retained turns; no automatic eviction or fresh replacement", delegationTurnLimit)
	}
	if err = delegationChildTx(tx, j, ""); err != nil {
		return empty, err
	}
	t = DelegationTurn{ID: id, JobID: job, Ordinal: ordinal + 1, State: "queued"}
	if _, err = tx.Exec(`INSERT INTO delegation_turns(id,job_id,ordinal,request,state) VALUES(?,?,?,?,?)`, id, job, t.Ordinal, jsonText(r), t.State); err != nil {
		return empty, err
	}
	if cold != nil {
		coldLease.NewTurnID = t.ID
		if _, err = tx.Exec(`UPDATE native_recovery_leases SET lease=? WHERE id=? AND state='claimed'`, jsonText(coldLease), coldID); err != nil {
			return empty, err
		}
	}
	return t, tx.Commit()
}

// runtime=="" checks the retained conversation identity, not permission to run.
func delegationChildTx(tx *sql.Tx, j DelegationJob, runtime string) error {
	var native, file, currentRuntime, parent string
	var stopped bool
	if err := tx.QueryRow(`SELECT native_id,coalesce(json_extract(adapter,'$.file'),''),runtime,parent,stopped FROM agent_sessions WHERE root_id=? AND id=?`, j.RootID, j.ChildID).Scan(&native, &file, &currentRuntime, &parent, &stopped); err != nil {
		return err
	}
	if stopped || parent != j.ParentID || currentRuntime == "" || runtime != "" && runtime != currentRuntime || j.NativeID != "" && (native != j.NativeID || file != j.SessionFile) {
		return errors.New("delegation child runtime/native/parent mismatch or stopped child")
	}
	return nil
}

// The native session is bound once, before the first prompt. A persisted file is
// not required yet because Pi may not flush its new transcript until that turn.
func (s *Store) bindDelegationNative(job, child, runtime, native, file string) error {
	if !delegationText(native, 256) || !delegationText(file, 4096) || !filepath.IsAbs(file) || filepath.Clean(file) != file {
		return errors.New("exact native identity and absolute session file required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, _, err := readDelegationJobTx(tx, job)
	if err != nil {
		return err
	}
	if child != j.ChildID || j.NativeID != "" && (j.NativeID != native || j.SessionFile != file) {
		return errors.New("delegation native identity cannot be replaced")
	}
	if err = delegationChildTx(tx, j, runtime); err != nil || runtime == "" {
		return errors.New("stale delegation host runtime")
	}
	var actualNative, actualFile string
	if err = tx.QueryRow(`SELECT native_id,coalesce(json_extract(adapter,'$.file'),'') FROM agent_sessions WHERE root_id=? AND id=?`, j.RootID, child).Scan(&actualNative, &actualFile); err != nil {
		return err
	}
	if native != actualNative || file != actualFile {
		return errors.New("host native binding differs from WT checkpoint")
	}
	_, err = tx.Exec(`UPDATE delegation_jobs SET native_id=?,session_file=? WHERE id=?`, native, file, job)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// A claim is a one-shot dispatch permit. Reopening a claimed turn never grants a
// second permit, even to the same runtime. A crash in the prompt window is uncertain.
func (s *Store) claimDelegationTurn(job, turn, child, runtime string) (DelegationTurnRequest, error) {
	var empty DelegationTurnRequest
	tx, err := s.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	j, _, err := readDelegationJobTx(tx, job)
	if err != nil {
		return empty, err
	}
	if child != j.ChildID || runtime == "" || j.NativeID == "" {
		return empty, errors.New("unbound or wrong delegation host")
	}
	if err = delegationChildTx(tx, j, runtime); err != nil {
		return empty, err
	}
	t, r, _, err := readDelegationTurnTx(tx, job, turn)
	if err != nil {
		return empty, err
	}
	if t.State != "queued" {
		return empty, errors.New("delegation turn already claimed or terminal; never redispatch")
	}
	if _, err = tx.Exec(`UPDATE delegation_turns SET state='claimed',runtime=? WHERE id=?`, runtime, turn); err != nil {
		return empty, err
	}
	return r, tx.Commit()
}

func (s *Store) finishDelegationTurn(job, turn, child, runtime string, result DelegationResult) error {
	if result.Unpublished || result.State != "completed" && result.State != "failed" || !delegationText(result.Leaf, 256) || len(result.Output)+len(result.Error) > delegationResultLimit || !utf8.ValidString(result.Output) || !utf8.ValidString(result.Error) || strings.ContainsRune(result.Output+result.Error, 0) || result.State == "completed" && (strings.TrimSpace(result.Output) == "" || result.Error != "") || result.State == "failed" && strings.TrimSpace(result.Error) == "" {
		return errors.New("invalid bounded delegation result")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, _, err := readDelegationJobTx(tx, job)
	if err != nil {
		return err
	}
	if child != j.ChildID || runtime == "" || j.NativeID == "" || result.NativeID != j.NativeID || result.SessionFile != j.SessionFile {
		return errors.New("delegation completion native identity mismatch")
	}
	if err = delegationChildTx(tx, j, runtime); err != nil {
		return err
	}
	t, _, prior, err := readDelegationTurnTx(tx, job, turn)
	if err != nil {
		return err
	}
	if (t.State == "completed" || t.State == "failed") && prior == jsonText(result) {
		return nil
	}
	if t.State != "claimed" || t.Runtime != runtime {
		return errors.New("only the claiming host can finish its turn; uncertain/terminal turns cannot be overwritten")
	}
	var leaf string
	var persisted bool
	if err = tx.QueryRow(`SELECT coalesce(json_extract(adapter,'$.leaf'),''),coalesce(json_extract(adapter,'$.persisted'),0) FROM agent_sessions WHERE root_id=? AND id=?`, j.RootID, child).Scan(&leaf, &persisted); err != nil {
		return err
	}
	if !persisted || leaf != result.Leaf {
		return errors.New("delegation completion requires the exact persisted native leaf checkpoint")
	}
	if _, err = tx.Exec(`UPDATE delegation_turns SET state=?,result=? WHERE id=?`, result.State, jsonText(result), turn); err != nil {
		return err
	}
	return tx.Commit()
}

// A start failure may retire only an unclaimed turn. This is not cancellation:
// once the host has claimed dispatch, only its observation or crash uncertainty
// can settle tracking. Any already-created interactive process remains host-owned.
func (s *Store) failQueuedDelegationTurn(owner DelegationOwner, runtime, job, turn, message string) error {
	return s.failPreparedDelegationTurn(owner, runtime, job, turn, message, false)
}

// Only the publishing owner may attest a definite failure before publication.
// Unlike an ordinary failed turn, this tombstone never advances the host head.
func (s *Store) cancelUnpublishedDelegationTurn(owner DelegationOwner, runtime, job, turn, message string) error {
	return s.failPreparedDelegationTurn(owner, runtime, job, turn, message, true)
}

func (s *Store) failPreparedDelegationTurn(owner DelegationOwner, runtime, job, turn, message string, unpublished bool) error {
	if !delegationText(message, 4096) {
		return errors.New("bounded delegation start failure required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := ownedDelegationJobTx(tx, owner, runtime, job)
	if err != nil {
		return err
	}
	t, _, prior, err := readDelegationTurnTx(tx, job, turn)
	if err != nil {
		return err
	}
	result := jsonText(DelegationResult{State: "failed", NativeID: j.NativeID, SessionFile: j.SessionFile, Error: message, Unpublished: unpublished})
	if t.State == "failed" && prior == result {
		return nil
	}
	if t.State != "queued" || t.Runtime != "" {
		return errors.New("delegation dispatch already claimed or terminal; start failure cannot cancel the child")
	}
	if _, err = tx.Exec(`UPDATE delegation_turns SET state='failed',result=? WHERE id=?`, result, turn); err != nil {
		return err
	}
	return tx.Commit()
}

// Recovery never redispatches. Only a replacement live runtime may mark the old
// claim uncertain. It cannot submit completion for work it did not observe.
func (s *Store) recoverDelegationTurn(job, turn, child, runtime string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, _, err := readDelegationJobTx(tx, job)
	if err != nil {
		return err
	}
	if child != j.ChildID || runtime == "" {
		return errors.New("wrong delegation recovery host")
	}
	if err = delegationChildTx(tx, j, runtime); err != nil {
		return err
	}
	t, _, _, err := readDelegationTurnTx(tx, job, turn)
	if err != nil {
		return err
	}
	if t.State == "claimed" {
		if t.Runtime == runtime {
			return errors.New("claiming host must finish or retain its own live turn")
		}
		if _, err = tx.Exec(`UPDATE delegation_turns SET state='uncertain' WHERE id=?`, turn); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Indexed exact lookup; no inbox or whole-transcript scan. The separate result
// read is bounded by admission/completion limits and is never inferred from prose.
func (s *Store) delegationStatus(owner DelegationOwner, runtime, job, turn string) (DelegationJob, DelegationTurn, *DelegationResult, error) {
	var j DelegationJob
	var t DelegationTurn
	tx, err := s.db.Begin()
	if err != nil {
		return j, t, nil, err
	}
	defer tx.Rollback()
	j, err = ownedDelegationJobTx(tx, owner, runtime, job)
	if err != nil {
		return j, t, nil, err
	}
	var raw string
	// Do not select the prompt or contract on status/result polling.
	err = tx.QueryRow(`SELECT id,job_id,ordinal,state,runtime,result FROM delegation_turns WHERE job_id=? AND id=?`, job, turn).Scan(&t.ID, &t.JobID, &t.Ordinal, &t.State, &t.Runtime, &raw)
	if err != nil || raw == "" {
		return j, t, nil, err
	}
	var result DelegationResult
	err = json.Unmarshal([]byte(raw), &result)
	return j, t, &result, err
}

// Ordinary update/hooks cannot silently replace a delegated conversation after
// binding. A new host runtime is permitted, a different native identity is not.
func checkDelegationNativeUpdate(tx *sql.Tx, root, child, native string, adapter *PiSnapshot) error {
	var expectedNative, expectedFile string
	err := tx.QueryRow(`SELECT native_id,session_file FROM delegation_jobs WHERE root_id=? AND child_id=?`, root, child).Scan(&expectedNative, &expectedFile)
	if err == sql.ErrNoRows || err == nil && expectedNative == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if native != expectedNative || adapter != nil && adapter.File != expectedFile {
		return errors.New("delegated native conversation cannot be replaced by a checkpoint")
	}
	return nil
}

func (s *Store) isDelegatedChild(root, child string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM delegation_jobs WHERE root_id=? AND child_id=?`, root, child).Scan(&count)
	return count != 0, err
}

// The admitted host may settle only its own pre-prompt startup failure. No
// replacement runtime may finish a claim whose execution it did not observe.
func (s *Store) failDelegationHostStartup(job, turn, child, runtime, message string) error {
	if runtime == "" {
		return errors.New("startup failure requires the exact host runtime")
	}
	if len(message) > 32768 || !utf8.ValidString(message) {
		return errors.New("invalid startup failure")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, _, err := readDelegationJobTx(tx, job)
	if err != nil {
		return err
	}
	if child != j.ChildID {
		return errors.New("startup failure child mismatch")
	}
	if err = delegationChildTx(tx, j, runtime); err != nil {
		return err
	}
	t, _, _, err := readDelegationTurnTx(tx, job, turn)
	if err != nil {
		return err
	}
	if t.State != "queued" && !(t.State == "claimed" && t.Runtime == runtime) {
		return errors.New("startup failure is not the host's current turn")
	}
	result := jsonText(DelegationResult{State: "failed", NativeID: j.NativeID, SessionFile: j.SessionFile, Error: message})
	if _, err = tx.Exec(`UPDATE delegation_turns SET state='failed',result=? WHERE id=?`, result, turn); err != nil {
		return err
	}
	return tx.Commit()
}
