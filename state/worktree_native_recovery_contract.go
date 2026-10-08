package main

import (
	"encoding/json"
	"errors"
	"reflect"
)

// Preserve ALL originally admitted fields, not merely the fields a new package
// happens to recognise. Only narrowly enumerated turn-local paths may change.
func (s *Store) validateColdNativeHostContract(job DelegationJob, launch delegationHostLaunch, binding, config, original map[string]any) error {
	r := launch.Recovery
	if r == nil || r.Version != 1 || config["id"] != r.Operation || binding["nativeId"] != job.NativeID || binding["sessionFile"] != job.SessionFile || binding["previousTurnId"] != r.PreviousTurn || binding["conversationDigest"] != job.ContractDigest {
		return errors.New("cold native config changes its operation/original conversation")
	}
	cold, ok := binding["coldRecovery"].(map[string]any)
	if !ok || cold["version"] != json.Number("1") || cold["operation"] != r.Operation || cold["leaseId"] != delegationDigest([]byte(jsonText([]string{job.ID, r.Operation}))) {
		return errors.New("cold native runner lacks its explicit authoritative lease identity")
	}
	var raw, state string
	if err := s.db.QueryRow(`SELECT lease,state FROM native_recovery_leases WHERE id=?`, cold["leaseId"]).Scan(&raw, &state); err != nil {
		return err
	}
	var lease NativeRecoveryLease
	if json.Unmarshal([]byte(raw), &lease) != nil || state != "claimed" || lease.NewTurnID != launch.Turn || lease.HostRuntime != "" || cold["sourceDigest"] != lease.SourceDigest || cold["sidecarDigest"] != lease.SidecarDigest || cold["leaf"] != lease.Leaf || cold["modelId"] != lease.ModelID || cold["thinking"] != lease.Thinking {
		return errors.New("cold native lease/SDK expectation drift")
	}
	steps, ok := config["steps"].([]any)
	oldSteps, oldOK := original["steps"].([]any)
	if !ok || !oldOK || len(steps) != 1 || len(oldSteps) != 1 {
		return errors.New("cold native host requires the original single step")
	}
	step, ok := steps[0].(map[string]any)
	oldStep, oldOK := oldSteps[0].(map[string]any)
	if !ok || !oldOK || step["sessionFile"] != job.SessionFile {
		return errors.New("cold native host transcript differs from original recorded session")
	}
	var turnRequestJSON string
	if err := s.db.QueryRow(`SELECT request FROM delegation_turns WHERE job_id=? AND id=?`, job.ID, launch.Turn).Scan(&turnRequestJSON); err != nil {
		return err
	}
	var turnRequest DelegationTurnRequest
	if json.Unmarshal([]byte(turnRequestJSON), &turnRequest) != nil || turnRequest.Prompt != "Task: "+stringValue(step["task"]) {
		return errors.New("cold runner task differs from queued one-shot prompt")
	}
	normalized := func(input map[string]any, skip map[string]bool) map[string]any {
		out := map[string]any{}
		for k, v := range input {
			if !skip[k] {
				out[k] = v
			}
		}
		return out
	}
	// These fields carry routing/results, not permissions/model/acceptance.
	topLocal := map[string]bool{"id": true, "asyncDir": true, "resultPath": true, "completionOwnerId": true, "childIntercomTargets": true, "nativeContinuation": true, "nativeColdRecovery": true, "steps": true, "runnerProcessInstanceId": true, "launchBarrierToken": true}
	stepLocal := map[string]bool{"task": true, "sessionFile": true}
	if !reflect.DeepEqual(normalized(config, topLocal), normalized(original, topLocal)) || !reflect.DeepEqual(normalized(step, stepLocal), normalized(oldStep, stepLocal)) {
		return errors.New("cold native runner changes original role/resources/permissions/acceptance/ceilings")
	}
	return nil
}
func stringValue(value any) string { v, _ := value.(string); return v }
