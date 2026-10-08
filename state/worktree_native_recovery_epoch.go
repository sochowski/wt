package main

import (
	"encoding/json"
	"errors"
	"syscall"
)

// Called only by the launcher while holding the root AND original node locks.
// Allocate a new physical ownership token without creating a logical child or
// changing its native identity, permissions, cwd, snapshot or budgets.
func (s *Store) startNativeRecoveryEpoch(request NativeRecoveryRequest, turn string) (NativeRecoveryLease, error) {
	return s.startNativeRecoveryEpochWithProbe(request, turn, func(pid int) error { return syscall.Kill(pid, 0) })
}
func (s *Store) startNativeRecoveryEpochWithProbe(request NativeRecoveryRequest, turn string, probe func(int) error) (NativeRecoveryLease, error) {
	empty := NativeRecoveryLease{}
	tx, err := s.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	job, err := ownedDelegationJobTx(tx, request.Owner, request.Runtime, request.Job)
	if err != nil {
		return empty, err
	}
	id := delegationDigest([]byte(jsonText([]string{job.ID, request.Operation})))
	var saved, raw, state string
	if err = tx.QueryRow(`SELECT request,lease,state FROM native_recovery_leases WHERE id=?`, id).Scan(&saved, &raw, &state); err != nil {
		return empty, err
	}
	var lease NativeRecoveryLease
	if saved != jsonText(request) || state != "claimed" || json.Unmarshal([]byte(raw), &lease) != nil || lease.NewTurnID != turn || turn == "" || lease.HostRuntime != "" {
		return empty, errors.New("cold epoch requires its exact unstarted claimed lease and new turn")
	}
	t, r, _, err := readDelegationTurnTx(tx, job.ID, turn)
	if err != nil {
		return empty, err
	}
	if t.State != "queued" || r.RunID != request.Operation || r.PreviousTurnID != request.PreviousTurn || r.ContractDigest != lease.ContractDigest {
		return empty, errors.New("cold epoch turn drift")
	}
	if err = delegationChildTx(tx, job, lease.ChildRuntime); err != nil {
		return empty, err
	}
	var adapterRaw string
	if err = tx.QueryRow(`SELECT adapter FROM agent_sessions WHERE root_id=? AND id=?`, job.RootID, job.ChildID).Scan(&adapterRaw); err != nil {
		return empty, err
	}
	var adapter PiSnapshot
	if json.Unmarshal([]byte(adapterRaw), &adapter) != nil || adapter.File != lease.SessionFile || adapter.Leaf != lease.Leaf || adapter.Backend == "durable" {
		return empty, errors.New("cold epoch checkpoint drift")
	}
	var protected int
	if err = tx.QueryRow(`SELECT count(*) FROM views WHERE root_id=? AND kind='agent' AND target=? AND (pinned=1 OR manager<>?)`, job.RootID, job.ChildID, job.ParentID).Scan(&protected); err != nil {
		return empty, err
	}
	if protected != 0 {
		return empty, errors.New("cold epoch cannot replace pinned/peer/human managed view")
	}
	record, err := readNativeRecoverySidecar(lease.SessionFile + ".native-host.json")
	if err != nil {
		return empty, err
	}
	pid, ok := record["hostPid"].(float64)
	if !ok || pid <= 0 || pid > 2147483647 || pid != float64(int(pid)) || record["jobId"] != job.ID || record["nativeId"] != lease.NativeID || record["sessionFile"] != lease.SessionFile || record["turnId"] != request.PreviousTurn || record["ownerSessionId"] != request.Owner.OwnerSessionID || record["parentSessionId"] != request.Owner.ParentNativeID || record["provider"] != request.Owner.Provider || !errors.Is(probe(int(pid)), syscall.ESRCH) {
		return empty, errors.New("original native host absence/identity no longer proven")
	}
	lease.HostRuntime = newID()
	result, err := tx.Exec(`UPDATE agent_sessions SET runtime=? WHERE root_id=? AND id=? AND runtime=? AND stopped=0`, lease.HostRuntime, job.RootID, job.ChildID, lease.ChildRuntime)
	if err != nil {
		return empty, err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return empty, errors.New("cold epoch lost original child ownership")
	}
	if _, err = tx.Exec(`UPDATE native_recovery_leases SET lease=? WHERE id=? AND state='claimed'`, jsonText(lease), id); err != nil {
		return empty, err
	}
	return lease, tx.Commit()
}
