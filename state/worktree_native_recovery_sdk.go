package main

import (
	"encoding/json"
	"errors"
	"os"
)

func (s *Store) authorizeNativeRecoverySDKOpen(request NativeRecoveryRequest, child, runtime string) (NativeRecoveryLease, error) {
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
	if saved != jsonText(request) || state != "claimed" || json.Unmarshal([]byte(raw), &lease) != nil || child != job.ChildID || runtime == "" || runtime != lease.HostRuntime || lease.SDKOpened {
		return empty, errors.New("SDK cold open requires its exact unused physical ownership epoch")
	}
	if err = delegationChildTx(tx, job, runtime); err != nil {
		return empty, err
	}
	turn, _, _, err := readDelegationTurnTx(tx, job.ID, lease.NewTurnID)
	if err != nil {
		return empty, err
	}
	if turn.State != "queued" {
		return empty, errors.New("SDK cold open turn is no longer unpublished")
	}
	source, err := os.ReadFile(lease.SessionFile)
	if err != nil || delegationDigest(source) != lease.SourceDigest {
		return empty, errors.New("SDK cold source fingerprint drift")
	}
	sidecar, err := os.ReadFile(lease.SessionFile + ".native-host.json")
	if err != nil || delegationDigest(sidecar) != lease.SidecarDigest {
		return empty, errors.New("SDK cold ownership record drift")
	}
	lease.SDKOpened = true
	if _, err = tx.Exec(`UPDATE native_recovery_leases SET lease=? WHERE id=? AND state='claimed'`, jsonText(lease), id); err != nil {
		return empty, err
	}
	return lease, tx.Commit()
}

// Actual SDK UUID/file/leaf and model are observed by the sole new host; do not
// accept a proposed replacement identity, or call the ordinary fresh binder.
func (s *Store) bindNativeRecoverySDK(request NativeRecoveryRequest, child, runtime, native string, snapshot PiSnapshot) error {
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
	var lease NativeRecoveryLease
	if saved != jsonText(request) || state != "claimed" || json.Unmarshal([]byte(raw), &lease) != nil || !lease.SDKOpened || child != job.ChildID || runtime != lease.HostRuntime || runtime == "" || native != lease.NativeID || snapshot.File != lease.SessionFile || snapshot.Leaf != lease.Leaf || !snapshot.Persisted || snapshot.Backend == "durable" || snapshot.Provider+"/"+snapshot.Model != lease.ModelID || snapshot.Thinking != lease.Thinking {
		return errors.New("actual cold SDK binding changed its admitted identity/leaf/model/thinking")
	}
	if err = delegationChildTx(tx, job, runtime); err != nil {
		return err
	}
	lease.State = "completed"
	if _, err = tx.Exec(`UPDATE native_recovery_leases SET state='completed',lease=? WHERE id=? AND state='claimed'`, jsonText(lease), id); err != nil {
		return err
	}
	return tx.Commit()
}
