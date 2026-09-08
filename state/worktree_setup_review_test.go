package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func checkedSetupGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := setupGit(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSetupReviewedDestinationCannotChangeBeforeBegin(t *testing.T) {
	for _, change := range []string{"environment", "symlink"} {
		t.Run(change, func(t *testing.T) {
			s := worktreeTestStore(t)
			w := testRoot(t, s, "reviewed-base")
			source, _ := setupFixture(t, "main")
			a, b := t.TempDir(), t.TempDir()
			base := a
			if change == "symlink" {
				base = filepath.Join(t.TempDir(), "base")
				if err := os.Symlink(a, base); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("WT_BASE_DIR", base)
			p, err := s.previewSetup(SetupPlan{Name: w.Name, RootID: w.ID, Repos: []SetupRepo{{Source: source}}})
			if err != nil {
				t.Fatal(err)
			}
			if change == "environment" {
				t.Setenv("WT_BASE_DIR", b)
			} else {
				if err := os.Remove(base); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(b, base); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.beginSetup(p); err == nil || !strings.Contains(err.Error(), "review a new plan") {
				t.Fatalf("changed reviewed plan accepted: %v", err)
			}
			var count int
			if err = s.db.QueryRow(`SELECT count(*) FROM checkout_setups`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("journal mutated: %d %v", count, err)
			}
			for _, dir := range []string{a, b} {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("destination mutated: %s %v", dir, err)
				}
			}
		})
	}
}

func TestSetupReservedBranchRejectedBeforePreparation(t *testing.T) {
	s := worktreeTestStore(t)
	source, _ := setupFixture(t, "main")
	t.Setenv("WT_BASE_DIR", t.TempDir())
	_, err := s.previewSetup(SetupPlan{Name: "HEAD", Repos: []SetupRepo{{Source: source}}})
	if err == nil || !strings.Contains(err.Error(), "Git branch name") {
		t.Fatalf("reserved branch accepted: %v", err)
	}
	if _, err = s.Worktree("HEAD"); err == nil {
		t.Fatal("preview created root")
	}
	// Repo-free sessions do not create a Git branch and retain name compatibility.
	if _, err = s.previewSetup(SetupPlan{Name: "HEAD"}); err != nil {
		t.Fatal(err)
	}
}

func TestSetupDiscoveryFollowsDirectorySymlinks(t *testing.T) {
	source, _ := setupFixture(t, "main")
	root := t.TempDir()
	if err := os.Symlink(source, filepath.Join(root, "api")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(root, "api-alias")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_REPO_DIRS", root)
	paths := discoverSetupRepos()
	if len(paths) != 1 || paths[0] != source {
		t.Fatalf("symlink discovery/dedup: %v", paths)
	}
}

func TestSetupPartialCloneDoesNotImplicitlyFetch(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "offline-partial")
	source, remote := setupFixture(t, "main")
	checkedSetupGit(t, remote, "config", "uploadpack.allowFilter", "true")
	checkedSetupGit(t, remote, "config", "uploadpack.allowAnySHA1InWant", "true")
	if err := os.WriteFile(filepath.Join(source, "payload"), []byte("blob intentionally absent from filtered clone\n"), 0600); err != nil {
		t.Fatal(err)
	}
	checkedSetupGit(t, source, "add", "payload")
	checkedSetupGit(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "payload")
	checkedSetupGit(t, source, "push", "origin", "main")
	dir := t.TempDir()
	clone := filepath.Join(dir, "partial")
	checkedSetupGit(t, dir, "clone", "--filter=blob:none", "--no-checkout", "file://"+remote, clone)
	blob := checkedSetupGit(t, source, "rev-parse", "HEAD:payload")
	if err := exec.Command("git", "--no-lazy-fetch", "-C", clone, "cat-file", "-e", blob).Run(); err == nil {
		t.Fatal("fixture unexpectedly has blob")
	}
	checkedSetupGit(t, source, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "missing commit")
	missing := checkedSetupGit(t, source, "rev-parse", "HEAD")
	checkedSetupGit(t, source, "push", "origin", "main")
	t.Setenv("WT_BASE_DIR", filepath.Join(dir, "worktrees"))
	// Even an inherited opt-in must not override the setup's no-lazy-fetch rule.
	t.Setenv("GIT_NO_LAZY_FETCH", "0")
	trace := filepath.Join(dir, "transport.trace")
	t.Setenv("GIT_TRACE", trace)
	if _, err := s.previewSetup(SetupPlan{Name: w.Name, RootID: w.ID, Repos: []SetupRepo{{Source: clone, Cached: true, Base: missing}}}); err == nil {
		t.Fatal("preview fetched unavailable commit")
	}
	p, err := s.previewSetup(SetupPlan{Name: w.Name, RootID: w.ID, Repos: []SetupRepo{{Source: clone, Cached: true, Base: "refs/remotes/origin/main"}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.beginSetup(p)
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.runSetup(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Repos[0].State == "attached" || p.Repos[0].Problem == "" {
		t.Fatalf("cached checkout fetched missing blob: %+v", p.Repos[0])
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "upload-pack") || strings.Contains(string(data), " fetch ") {
		t.Fatalf("implicit transport occurred:\n%s", data)
	}
}

func TestSetupPreviewPreservesUnsupportedGitDiagnostic(t *testing.T) {
	s := worktreeTestStore(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf '%s\\n' 'unknown option: --no-lazy-fetch' >&2\nexit 129\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_, err := s.previewSetup(SetupPlan{Name: "valid-task", Repos: []SetupRepo{{Source: t.TempDir()}}})
	if err == nil || !strings.Contains(err.Error(), "unknown option: --no-lazy-fetch") {
		t.Fatalf("Git diagnostic hidden: %v", err)
	}
}

func TestSetupFixtureDisablesAmbientGitConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'commit.gpgsign=true'")
	source, _ := setupFixture(t, "main")
	if os.Getenv("GIT_CONFIG_COUNT") != "0" || os.Getenv("GIT_CONFIG_GLOBAL") != os.DevNull {
		t.Fatal("fixture retained ambient config")
	}
	checkedSetupGit(t, source, "log", "-1", "--format=%s")
}
