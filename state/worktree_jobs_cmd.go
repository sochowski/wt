package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// Internal package-host transport only. It neither launches ordinary Pi nor
// grants the model a completion tool. All operations retain the store fences.
func delegationCommand(s *Store, operation string, input io.Reader) (any, error) {
	body, err := io.ReadAll(io.LimitReader(input, 1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 1024*1024 || !utf8.Valid(body) {
		return nil, errors.New("delegation command requires bounded UTF-8 JSON")
	}
	if operation == "prepare-cold" || operation == "claim-cold" || operation == "cancel-cold" || operation == "queue-cold" || operation == "open-cold" || operation == "bind-cold" {
		return nativeRecoveryCommand(s, operation, bytes.NewReader(body))
	}
	var request struct {
		Owner     DelegationOwner       `json:"owner"`
		Admission DelegationAdmission   `json:"admission"`
		Request   DelegationTurnRequest `json:"request"`
		Runtime   string                `json:"runtime"`
		Job       string                `json:"job"`
		Turn      string                `json:"turn"`
		Child     string                `json:"child"`
		Result    DelegationResult      `json:"result"`
		Native    string                `json:"native"`
		Snapshot  PiSnapshot            `json:"snapshot"`
		Error     string                `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("delegation command requires exactly one JSON object")
	}
	switch operation {
	case "reserve":
		job, turn, err := s.reserveDelegation(request.Admission, request.Runtime, request.Request)
		return struct {
			Job  DelegationJob  `json:"job"`
			Turn DelegationTurn `json:"turn"`
		}{job, turn}, err
	case "queue":
		return s.queueDelegationTurn(request.Owner, request.Runtime, request.Job, request.Request)
	case "status":
		job, turn, result, err := s.delegationStatus(request.Owner, request.Runtime, request.Job, request.Turn)
		return struct {
			Job    DelegationJob     `json:"job"`
			Turn   DelegationTurn    `json:"turn"`
			Result *DelegationResult `json:"result"`
		}{job, turn, result}, err
	case "claim":
		return s.claimDelegationTurn(request.Job, request.Turn, request.Child, request.Runtime)
	case "finish":
		err = s.finishDelegationTurn(request.Job, request.Turn, request.Child, request.Runtime, request.Result)
	case "fail-host-startup":
		err = s.failDelegationHostStartup(request.Job, request.Turn, request.Child, request.Runtime, request.Error)
	case "fail-queued":
		err = s.failQueuedDelegationTurn(request.Owner, request.Runtime, request.Job, request.Turn, request.Error)
	case "cancel-unpublished":
		err = s.cancelUnpublishedDelegationTurn(request.Owner, request.Runtime, request.Job, request.Turn, request.Error)
	case "recover":
		err = s.recoverDelegationTurn(request.Job, request.Turn, request.Child, request.Runtime)
	case "bind", "checkpoint":
		var root, child string
		if err = s.db.QueryRow(`SELECT root_id,child_id FROM delegation_jobs WHERE id=?`, request.Job).Scan(&root, &child); err != nil {
			return nil, err
		}
		if child != request.Child {
			return nil, errors.New("delegation checkpoint child mismatch")
		}
		if err = s.updateAgent(root, child, request.Runtime, "idle", request.Native, &request.Snapshot); err != nil {
			return nil, err
		}
		if operation == "bind" {
			err = s.bindDelegationNative(request.Job, child, request.Runtime, request.Native, request.Snapshot.File)
		}
	default:
		return nil, errors.New("unknown internal delegation operation")
	}
	return struct {
		OK bool `json:"ok"`
	}{err == nil}, err
}
