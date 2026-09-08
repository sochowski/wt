package main

import (
	"strings"
	"testing"
)

func TestDelegationUnpublishedSuffixDoesNotAdvanceRetainedHost(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	f.bind(t)
	f.complete(t)
	hostTurn := f.turn
	var abandoned []DelegationTurn
	for i := 1; i <= 3; i++ {
		r := nextDelegationTurn(f)
		r.RequestID += strings.Repeat("-new", i)
		turn, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, r)
		if err != nil || turn.Ordinal != i {
			t.Fatalf("fresh continuation after unpublished suffix: %+v %v", turn, err)
		}
		if err := f.s.cancelUnpublishedDelegationTurn(f.a.DelegationOwner, "stale", f.job.ID, turn.ID, "not published"); err == nil {
			t.Fatal("stale publisher retired a turn")
		}
		body := jsonText(map[string]any{"owner": f.a.DelegationOwner, "runtime": "parent-runtime", "job": f.job.ID, "turn": turn.ID, "error": "not published"})
		for retry := 0; retry < 2; retry++ {
			if _, err := delegationCommand(f.s, "cancel-unpublished", strings.NewReader(body)); err != nil {
				t.Fatal(err)
			}
		}
		_, status, result, err := f.s.delegationStatus(f.a.DelegationOwner, "parent-runtime", f.job.ID, turn.ID)
		if err != nil || status.State != "failed" || status.Runtime != "" || !result.Unpublished {
			t.Fatalf("missing exact tombstone: %+v %+v %v", status, result, err)
		}
		abandoned = append(abandoned, turn)
	}
	turn, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, nextDelegationTurn(f))
	if err != nil || turn.Ordinal != 4 {
		t.Fatalf("same retained host cannot continue: %+v %v", turn, err)
	}
	for _, old := range abandoned {
		if _, err := f.s.claimDelegationTurn(f.job.ID, old.ID, f.job.ChildID, f.runtime); err == nil {
			t.Fatal("unpublished prompt replayed")
		}
	}
	f.turn = turn
	f.complete(t)
	// A real completed turn cannot be skipped like an unpublished one.
	f.turn = hostTurn
	r := nextDelegationTurn(f)
	r.RequestID = "forbidden-old-host"
	if _, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, r); err == nil {
		t.Fatal("completed turn bypassed")
	}
	w, err := f.s.Worktree(f.root.ID)
	if err != nil || len(w.Agents) != 2 || w.Agents[1].ID != f.job.ChildID {
		t.Fatalf("continuation replaced its child: %+v %v", w, err)
	}
}

func TestDelegationOnlyDefiniteUnpublicationMayBeSkipped(t *testing.T) {
	for _, state := range []string{"queued", "claimed", "uncertain", "failed", "completed"} {
		t.Run(state, func(t *testing.T) {
			f := newDelegationFixture(t)
			f.reserve(t)
			f.bind(t)
			f.complete(t)
			r := nextDelegationTurn(f)
			turn, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, r)
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case "claimed", "uncertain":
				if _, err := f.s.claimDelegationTurn(f.job.ID, turn.ID, f.job.ChildID, f.runtime); err != nil {
					t.Fatal(err)
				}
				if state == "uncertain" {
					f.runtime = "replacement-runtime"
					f.bind(t)
					if err := f.s.recoverDelegationTurn(f.job.ID, turn.ID, f.job.ChildID, f.runtime); err != nil {
						t.Fatal(err)
					}
				}
			case "failed":
				if err := f.s.failQueuedDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, turn.ID, "publication not independently known"); err != nil {
					t.Fatal(err)
				}
			case "completed":
				next := f
				next.turn = turn
				next.complete(t)
			}
			r.RequestID = "do-not-skip"
			if _, err := f.s.queueDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, r); err == nil {
				t.Fatal("unsafe suffix skipped")
			}
			if state != "queued" {
				if err := f.s.cancelUnpublishedDelegationTurn(f.a.DelegationOwner, "parent-runtime", f.job.ID, turn.ID, "not published"); err == nil {
					t.Fatal("terminal/claimed turn relabeled unpublished")
				}
			}
		})
	}
}

func TestDelegationHostCannotAttestPublisherTombstone(t *testing.T) {
	f := newDelegationFixture(t)
	f.reserve(t)
	f.bind(t)
	result := f.complete(t)
	result.Unpublished = true
	if err := f.s.finishDelegationTurn(f.job.ID, f.turn.ID, f.job.ChildID, f.runtime, result); err == nil {
		t.Fatal("host completion fabricated publisher attestation")
	}
}
