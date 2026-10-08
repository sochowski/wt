package main

import (
	"encoding/json"
	"errors"
	"io"
)

// Private versioned control transport. No model completion tool, no launch and
// no warm-continuation fallback. The enclosing command enforces UTF-8/size.
func nativeRecoveryCommand(s *Store, operation string, input io.Reader) (any, error) {
	if operation == "open-cold" || operation == "bind-cold" {
		var payload struct {
			Recovery NativeRecoveryRequest `json:"recovery"`
			Child    string                `json:"child"`
			Runtime  string                `json:"runtime"`
			Native   string                `json:"native"`
			Snapshot PiSnapshot            `json:"snapshot"`
		}
		decoder := json.NewDecoder(input)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return nil, err
		}
		if decoder.Decode(new(any)) != io.EOF || payload.Recovery.Version != 1 {
			return nil, errors.New("invalid versioned cold SDK request")
		}
		if operation == "open-cold" {
			return s.authorizeNativeRecoverySDKOpen(payload.Recovery, payload.Child, payload.Runtime)
		}
		err := s.bindNativeRecoverySDK(payload.Recovery, payload.Child, payload.Runtime, payload.Native, payload.Snapshot)
		return struct {
			OK bool `json:"ok"`
		}{err == nil}, err
	}
	if operation == "queue-cold" {
		var payload struct {
			Recovery NativeRecoveryRequest `json:"recovery"`
			Request  DelegationTurnRequest `json:"request"`
		}
		decoder := json.NewDecoder(input)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return nil, err
		}
		if decoder.Decode(new(any)) != io.EOF || payload.Recovery.Version != 1 {
			return nil, errors.New("invalid versioned cold native queue request")
		}
		r := payload.Recovery
		return s.queueDelegationTurnWithColdRecovery(r.Owner, r.Runtime, r.Job, payload.Request, &r)
	}
	var request NativeRecoveryRequest
	decoder := json.NewDecoder(input)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("native recovery requires exactly one bounded request")
	}
	if request.Version != 1 {
		return nil, errors.New("unsupported native recovery protocol version")
	}
	switch operation {
	case "prepare-cold":
		return s.prepareNativeRecovery(request)
	case "claim-cold":
		return s.claimNativeRecovery(request)
	case "cancel-cold":
		err := s.cancelPreparedNativeRecovery(request)
		return struct {
			OK bool `json:"ok"`
		}{err == nil}, err
	default:
		return nil, errors.New("unsupported native recovery operation")
	}
}
