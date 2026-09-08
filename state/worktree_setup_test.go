package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupFixture(t *testing.T, branch string) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	t.Setenv("GIT_TEMPLATE_DIR", t.TempDir())
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	source := filepath.Join(dir, "space : ' repo")
	for _, p := range []string{remote, source} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	git := func(path string, args ...string) string {
		t.Helper()
		out, err := setupGit(path, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git(remote, "init", "--bare", "-b", branch)
	git(source, "init", "-b", branch)
	git(source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
	git(source, "remote", "add", "origin", remote)
	git(source, "push", "-u", "origin", branch)
	git(source, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+branch)
	return source, remote
}
func TestSetupFetchedExactMultiRepoDirtyAndResume(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "billing-migration")
	t.Setenv("WT_BASE_DIR", filepath.Join(t.TempDir(), "work trees : here"))
	a, remote := setupFixture(t, "trunk")
	b, _ := setupFixture(t, "custom-default")
	if err := os.WriteFile(filepath.Join(a, "dirty"), []byte("staged\n"), 0644); err != nil {
		t.Fatal(err)
	}
	setupGit(a, "add", "dirty")
	os.WriteFile(filepath.Join(a, "dirty"), []byte("working\n"), 0644)
	before, _ := setupGit(a, "status", "--porcelain=v1")
	local, _ := setupGit(a, "rev-parse", "HEAD")
	index, _ := setupGit(a, "write-tree")
	// Advance the remote without changing the selected original's branch/index.
	other := filepath.Join(t.TempDir(), "other")
	if _, err := setupGit(filepath.Dir(other), "clone", remote, other); err != nil {
		t.Fatal(err)
	}
	if _, err := setupGit(other, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "latest"); err != nil {
		t.Fatal(err)
	}
	setupGit(other, "push", "origin", "trunk")
	latest, _ := setupGit(other, "rev-parse", "HEAD")
	plan, err := s.previewSetup(SetupPlan{Name: w.Name, RootID: w.ID, Repos: []SetupRepo{{Source: a}, {Source: b}}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Repos[0].Key == plan.Repos[1].Key {
		t.Fatal("duplicate basenames not disambiguated")
	}
	if _, err = os.Stat(plan.Repos[0].Destination); !os.IsNotExist(err) {
		t.Fatal("preview mutated checkout")
	}
	plan, err = s.beginSetup(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = s.runSetup(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range plan.Repos {
		if r.State != "attached" {
			t.Fatalf("%+v", r)
		}
		if filepath.Base(r.Destination) != w.Name {
			t.Fatal(r.Destination)
		}
		branch, _ := setupGit(r.Destination, "branch", "--show-current")
		if branch != w.Name {
			t.Fatal(branch)
		}
	}
	if plan.Repos[0].SHA != latest || plan.Repos[0].SelectedRef != "refs/remotes/origin/trunk" {
		t.Fatalf("wrong fetched provenance: %+v", plan.Repos[0])
	}
	after, _ := setupGit(a, "status", "--porcelain=v1")
	head, _ := setupGit(a, "rev-parse", "HEAD")
	afterIndex, _ := setupGit(a, "write-tree")
	if before != after || local != head || index != afterIndex {
		t.Fatal("original modified")
	}
	prior, _ := s.Worktree(w.ID)
	plan, err = s.runSetup(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	now, _ := s.Worktree(w.ID)
	if len(now.Checkouts) != 2 || now.Agents[0].ID != prior.Agents[0].ID {
		t.Fatal("resume replaced resources")
	}
	// Provenance is an initial commit, not a lock on later commits.
	setupGit(plan.Repos[0].Destination, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "task")
	plan, err = s.runSetup(plan, nil)
	if err != nil || plan.Repos[0].Problem != "" {
		t.Fatalf("advanced checkout: %+v %v", plan, err)
	}
}
func TestSetupOfflinePartialCollisionsAndReconciliation(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "offline-task")
	t.Setenv("WT_BASE_DIR", t.TempDir())
	a, _ := setupFixture(t, "main")
	b, remote := setupFixture(t, "next")
	c, _ := setupFixture(t, "master")
	plan, err := s.beginSetup(SetupPlan{Name: w.Name, RootID: w.ID, Repos: []SetupRepo{{Source: a}, {Source: b}, {Source: c}}})
	if err != nil {
		t.Fatal(err)
	}
	os.Rename(remote, remote+".offline")
	plan, err = s.runSetup(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Repos[0].State != "attached" || plan.Repos[1].Problem == "" || plan.Repos[2].State != "not_attempted" {
		t.Fatalf("partial: %+v", plan)
	}
	// Explicit cached consent; the failed fetch never silently reached this path.
	plan.Repos[1].Cached = true
	plan.Repos[1].Base = "refs/remotes/origin/next"
	s.saveSetup(plan)
	plan, err = s.runSetup(plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range plan.Repos {
		if r.State != "attached" {
			t.Fatalf("cached failed: %+v", r)
		}
	}
	if _, err = s.previewSetup(SetupPlan{Name: w.Name, Repos: []SetupRepo{{Source: a}}}); err == nil {
		t.Fatal("adopted collision")
	}
	// Interrupted add with our marker is safely reconciled; unrelated paths aren't.
	plan.Repos[0].State = "creating"
	s.saveSetup(plan)
	plan, err = s.runSetup(plan, nil)
	if err != nil || plan.Repos[0].State != "attached" {
		t.Fatal("verified reconciliation failed")
	}
	marker, _ := setupMarker(plan.Repos[1])
	os.Remove(marker)
	plan.Repos[1].State = "creating"
	s.saveSetup(plan)
	plan, err = s.runSetup(plan, nil)
	if err != nil || !strings.Contains(plan.Repos[1].Problem, "unverified") {
		t.Fatalf("unrelated path adopted: %+v %v", plan, err)
	}
}
func TestSetupPreviewNoNetworkAndValidation(t *testing.T) {
	s := worktreeTestStore(t)
	t.Setenv("WT_BASE_DIR", t.TempDir())
	a, remote := setupFixture(t, "special")
	os.Rename(remote, remote+".offline")
	p, err := s.previewSetup(SetupPlan{Name: "preview", Repos: []SetupRepo{{Source: a}}})
	if err != nil || p.Repos[0].SHA == "" {
		t.Fatalf("offline preview %v", err)
	}
	for _, p := range []SetupPlan{{Name: "bad/name", Repos: []SetupRepo{{Source: a}}}, {Name: "valid", Repos: []SetupRepo{{Source: a}, {Source: a}}}, {Name: "valid", Repos: []SetupRepo{{Source: a, Alias: "root"}}}, {Name: "valid", Repos: []SetupRepo{{Source: a, Cached: true, Base: "missing"}}}} {
		if _, err = s.previewSetup(p); err == nil {
			t.Fatalf("invalid plan accepted: %+v", p)
		}
	}
	// Caller Git environment cannot redirect operations into another repository.
	other, _ := setupFixture(t, "other")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	_, id, err := setupIdentity(a)
	if err != nil || id != filepath.Join(a, ".git") {
		t.Fatalf("Git environment redirected source: %s %v", id, err)
	}
}
