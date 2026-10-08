package main

import (
	"errors"
	"testing"
)

func TestColdNativePlacementPreservesDeadViewAndRefusesUnsafeObservations(t *testing.T) {
	job := DelegationJob{ChildID: "original-child", ParentID: "original-parent"}
	view := View{ID: "original-view", Kind: "agent", Target: job.ChildID, Manager: job.ParentID}
	for _, tc := range []struct {
		name, dead, clients string
		deadErr, clientsErr error
		wantErr             bool
	}{
		{name: "same-dead-unfocused-pane", dead: "1", clients: "%other\n"},
		{name: "live-repair-shell", dead: "0", wantErr: true},
		{name: "empty-death-observation", wantErr: true},
		{name: "unknown-death", deadErr: errors.New("disconnected"), wantErr: true},
		{name: "human-active-dead-pane", dead: "1", clients: "%old\n", wantErr: true},
		{name: "unknown-human-focus", dead: "1", clientsErr: errors.New("disconnected"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := func(args ...string) (string, error) {
				switch args[0] {
				case "display-message":
					return tc.dead, tc.deadErr
				case "list-clients":
					return tc.clients, tc.clientsErr
				default:
					t.Fatalf("placement emitted a mutation: %v", args)
					return "", nil
				}
			}
			err := validateColdNativePlacement(job, view, "%old", probe)
			if (err != nil) != tc.wantErr {
				t.Fatal("unsafe placement decision", err)
			}
		})
	}
	for _, v := range []View{{}, {ID: view.ID, Kind: view.Kind, Target: view.Target, Manager: view.Manager, Pinned: true}, {ID: view.ID, Kind: view.Kind, Target: view.Target, Manager: "peer"}, {ID: view.ID, Kind: view.Kind, Target: "replacement-child", Manager: view.Manager}} {
		if err := validateColdNativePlacement(job, v, "", func(...string) (string, error) { t.Fatal("protected view queried/mutated tmux"); return "", nil }); err == nil {
			t.Fatal("protected/replacement view accepted")
		}
	}
	if err := validateColdNativePlacement(job, view, "", func(...string) (string, error) { t.Fatal("unplaced view queried/mutated tmux"); return "", nil }); err != nil {
		t.Fatal(err)
	}
}
