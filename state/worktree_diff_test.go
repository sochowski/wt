package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiffPositionInertRecovery(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	valid := &DiffPosition{File: "deleted/nested.txt", Line: 55, Column: 2, Topline: 40, Leftcol: 0}
	state := ViewState{Version: 1, Base: "123", Diff: valid}
	encoded, _ := json.Marshal(state)
	var recovered ViewState
	if err := json.Unmarshal(encoded, &recovered); err != nil {
		t.Fatal(err)
	}
	if err := validateDiffPosition(root, recovered); err != nil {
		t.Fatal(err)
	}
	if *recovered.Diff != *valid {
		t.Fatal("diff position changed")
	}
	for _, file := range []string{"../outside", outside, "escape/missing.txt", "bad\nfile", ".", ""} {
		p := *valid
		p.File = file
		if validateDiffPosition(root, ViewState{Diff: &p}) == nil {
			t.Errorf("accepted %q", file)
		}
	}
	p := *valid
	p.Line = 0
	if validateDiffPosition(root, ViewState{Diff: &p}) == nil {
		t.Fatal("accepted zero line")
	}
}
