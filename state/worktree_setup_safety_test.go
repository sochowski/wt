package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupRemoteDefaultChangeAndSymbolicFetchProtection(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "default-change")
	t.Setenv("WT_BASE_DIR", t.TempDir())
	source, remote := setupFixture(t, "main")
	p, err := s.beginSetup(SetupPlan{Name: w.Name, RootID: w.ID, Repos: []SetupRepo{{Source: source}}})
	if err != nil {
		t.Fatal(err)
	}
	old, _ := setupGit(source, "rev-parse", "HEAD")
	setupGit(source, "push", "origin", "HEAD:refs/heads/release-default")
	setupGit(remote, "symbolic-ref", "HEAD", "refs/heads/release-default")
	p, err = s.runSetup(p, nil)
	if err != nil || p.Repos[0].Problem != "" || p.Repos[0].SelectedRef != "refs/remotes/origin/release-default" {
		t.Fatalf("remote HEAD not respected: %+v %v", p, err)
	}
	// A remote-tracking symbolic ref must never route fetch into a local branch.
	w2 := testRoot(t, s, "symbolic-protection")
	setupGit(source, "symbolic-ref", "refs/remotes/origin/release-default", "refs/heads/main")
	p, err = s.beginSetup(SetupPlan{Name: w2.Name, RootID: w2.ID, Repos: []SetupRepo{{Source: source}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.runSetup(p, nil)
	if err != nil || !strings.Contains(p.Repos[0].Problem, "symbolic fetch destination") {
		t.Fatalf("unsafe symbolic ref accepted: %+v %v", p, err)
	}
	current, _ := setupGit(source, "rev-parse", "HEAD")
	if old != current {
		t.Fatal("local branch moved")
	}
}
func TestSetupCachedExactConsentAndAncestorChange(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "cached-consent")
	base := t.TempDir()
	t.Setenv("WT_BASE_DIR", base)
	source, _ := setupFixture(t, "main")
	p, err := s.previewSetup(SetupPlan{Name: w.Name, RootID: w.ID, Repos: []SetupRepo{{Source: source, Cached: true, Base: "HEAD"}}})
	if err != nil {
		t.Fatal(err)
	}
	accepted := p.Repos[0].SHA
	setupGit(source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "moved after preview")
	p, err = s.beginSetup(p)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.loadSetup(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.runSetup(p, nil)
	if err != nil || p.Repos[0].Problem != "" || p.Repos[0].SHA != accepted {
		t.Fatalf("cached consent moved: %+v %v", p, err)
	}
	w2 := testRoot(t, s, "ancestor-change")
	p, err = s.beginSetup(SetupPlan{Name: w2.Name, RootID: w2.ID, Repos: []SetupRepo{{Source: source}}})
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(p.Repos[0].Destination)
	// Use another WT base to avoid moving/removing any previously created worktree.
	separate := filepath.Join(t.TempDir(), "base")
	t.Setenv("WT_BASE_DIR", separate)
	p, err = s.beginSetup(SetupPlan{Name: w2.Name, RootID: w2.ID, Repos: []SetupRepo{{Source: source}}})
	if err != nil {
		t.Fatal(err)
	}
	parent = filepath.Dir(p.Repos[0].Destination)
	os.MkdirAll(separate, 0755)
	if err = os.Symlink(t.TempDir(), parent); err != nil {
		t.Fatal(err)
	}
	p, err = s.runSetup(p, nil)
	if err != nil || !strings.Contains(p.Repos[0].Problem, "ancestor changed") {
		t.Fatalf("symlink redirect accepted: %+v %v", p, err)
	}
	if _, err = setupGit(source, "show-ref", "--verify", "refs/heads/"+w2.Name); err == nil {
		t.Fatal("branch created through redirected destination")
	}
}

func TestSetupForgetSerializesAndMissingJournalStops(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "in-flight")
	lock, err := s.rootLock(w)
	if err != nil {
		t.Fatal(err)
	}
	if err = worktreeCommand(s, []string{"forget", w.Name, ""}); err == nil {
		t.Fatal("forgot root during setup lock")
	}
	lock.Close()
	if _, err = s.Worktree(w.ID); err != nil {
		t.Fatal("root was lost")
	}
	if err = s.saveSetup(SetupPlan{ID: newID(), RootID: w.ID}); err == nil {
		t.Fatal("missing journal accepted")
	}
}
