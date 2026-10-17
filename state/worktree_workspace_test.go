package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func workspaceFixture(t *testing.T) (*Store, Worktree) {
	t.Helper()
	s := worktreeTestStore(t)
	t.Setenv("WT_BASE_DIR", filepath.Join(t.TempDir(), "worktrees"))
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	t.Setenv("WT_AGENT_ID", "")
	w, err := s.createNamedRoot("workspace-task", "")
	if err != nil {
		t.Fatal(err)
	}
	return s, w
}
func TestWorkspaceNewMapAddAndExplicitCwd(t *testing.T) {
	s, w := workspaceFixture(t)
	if err := worktreeCommand(s, []string{"new", "empty-explicit", "--cwd=", "--offline"}); err == nil {
		t.Fatal("empty explicit cwd became managed workspace")
	}
	if w.Cwd != filepath.Join(os.Getenv("WT_BASE_DIR"), ".sessions", w.Name) || w.Agents[0].Cwd != w.Cwd || w.Workspace == nil {
		t.Fatalf("wrong home: %+v", w)
	}
	source, _ := setupFixture(t, "main")
	p, err := s.beginSetup(SetupPlan{RootID: w.ID, Name: w.Name, Repos: []SetupRepo{{Source: source, Alias: "api"}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.runSetup(p, nil)
	if err != nil || p.Repos[0].Problem != "" {
		t.Fatalf("%+v %v", p, err)
	}
	after, err := s.Worktree(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Agents[0].ID != w.Agents[0].ID || after.Cwd != w.Cwd {
		t.Fatal("Add replaced parent")
	}
	target, err := os.Readlink(filepath.Join(w.Cwd, "repos", "api"))
	if err != nil || target != p.Repos[0].Destination {
		t.Fatal(target, err)
	}
	content, err := os.ReadFile(filepath.Join(w.Cwd, "WORKSPACE.md"))
	if err != nil || !strings.Contains(string(content), p.Repos[0].Destination) {
		t.Fatal(string(content), err)
	}
	if _, err = containedPath(w.Cwd, "repos/api"); err == nil {
		t.Fatal("shortcut bypassed root view containment")
	}
	if got, err := after.targetPath("api"); err != nil || got != target {
		t.Fatal(got, err)
	}
	explicit := t.TempDir()
	os.WriteFile(filepath.Join(explicit, "AGENTS.md"), []byte("user instructions"), 0600)
	old, err := s.createNamedRoot("explicit", explicit)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.refreshWorkspace(old); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(explicit)
	if old.Cwd != explicit || old.Workspace != nil || len(entries) != 1 {
		t.Fatal("explicit cwd adopted")
	}
	legacy := testRoot(t, s, "legacy")
	if err = s.refreshWorkspace(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(filepath.Join(legacy.Cwd, "WORKSPACE.md")); !os.IsNotExist(err) {
		t.Fatal("legacy cwd written")
	}
}
func TestWorkspacePublicationConflictsFailClosed(t *testing.T) {
	for _, kind := range []string{"home-symlink", "home-directory", "repos-symlink", "repos-directory", "scratch-symlink", "map-symlink", "map-file", "map-edit", "map-hardlink", "shortcut-symlink", "shortcut-file", "new-shortcut-file", "container-symlink"} {
		t.Run(kind, func(t *testing.T) {
			s, w := workspaceFixture(t)
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel")
			os.WriteFile(sentinel, []byte("untouched"), 0600)
			path := filepath.Join(w.Cwd, "WORKSPACE.md")
			switch kind {
			case "home-symlink", "home-directory":
				path = w.Cwd
			case "repos-symlink", "repos-directory":
				path = filepath.Join(w.Cwd, "repos")
			case "scratch-symlink":
				path = filepath.Join(w.Cwd, "scratch")
			case "container-symlink":
				path = filepath.Dir(w.Cwd)
			case "shortcut-symlink", "shortcut-file", "new-shortcut-file":
				if _, err := s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?)`, newID(), w.ID, "api", outside); err != nil {
					t.Fatal(err)
				}
				if kind != "new-shortcut-file" {
					if err := s.refreshWorkspace(w); err != nil {
						t.Fatal(err)
					}
				}
				path = filepath.Join(w.Cwd, "repos", "api")
			}
			if kind == "map-edit" {
				os.WriteFile(path, []byte("user edit"), 0600)
			} else {
				if kind != "new-shortcut-file" {
					if err := os.Rename(path, path+".original"); err != nil {
						t.Fatal(err)
					}
				}
				switch {
				case strings.HasSuffix(kind, "directory"):
					os.Mkdir(path, 0700)
				case kind == "map-hardlink":
					os.Link(sentinel, path)
				case strings.HasSuffix(kind, "file"):
					os.WriteFile(path, []byte("user file"), 0600)
				default:
					os.Symlink(outside, path)
					if kind == "map-symlink" {
						os.Remove(path)
						os.Symlink(sentinel, path)
					}
				}
			}
			if err := s.refreshWorkspace(w); err == nil {
				t.Fatal("substituted object accepted")
			}
			content, _ := os.ReadFile(sentinel)
			if string(content) != "untouched" {
				t.Fatal("outside file changed")
			}
			entries, _ := os.ReadDir(outside)
			if len(entries) != 1 {
				t.Fatal("publication escaped")
			}
			if kind == "map-edit" {
				content, _ = os.ReadFile(path)
				if string(content) != "user edit" {
					t.Fatal("user map edit overwritten")
				}
			}
		})
	}
}
func TestWorkspacePreexistingHomesAndContainersNotAdopted(t *testing.T) {
	for _, kind := range []string{"container-dir", "container-link", "home-dir", "home-link", "home-file"} {
		t.Run(kind, func(t *testing.T) {
			s := worktreeTestStore(t)
			base := t.TempDir()
			t.Setenv("WT_BASE_DIR", base)
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_TMPDIR", t.TempDir())
			t.Setenv("WT_AGENT_ID", "")
			path := filepath.Join(base, ".sessions")
			if strings.HasPrefix(kind, "home-") {
				if _, err := s.createNamedRoot("first", ""); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(path, "conflict")
			}
			if strings.HasSuffix(kind, "dir") {
				os.Mkdir(path, 0700)
			} else if strings.HasSuffix(kind, "file") {
				os.WriteFile(path, []byte("user"), 0600)
			} else {
				os.Symlink(t.TempDir(), path)
			}
			if _, err := s.createNamedRoot("conflict", ""); err == nil {
				t.Fatal("adopted preexisting path")
			}
			if _, exists, err := s.Get("conflict"); err != nil || exists {
				t.Fatal("conflicting root created")
			}
		})
	}
}
func TestSetupReadableAliasesStableCollisionsAndExplicitReservations(t *testing.T) {
	s := worktreeTestStore(t)
	t.Setenv("WT_BASE_DIR", t.TempDir())
	a, _ := setupFixture(t, "main")
	b, _ := setupFixture(t, "main")
	p, err := s.previewSetup(SetupPlan{Name: "aliases", Repos: []SetupRepo{{Source: a}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Repos[0].Alias != repoSlug(filepath.Base(a)) {
		t.Fatal(p.Repos[0].Alias)
	}
	p, err = s.previewSetup(SetupPlan{Name: "aliases", Repos: []SetupRepo{{Source: a}, {Source: b}}})
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := s.previewSetup(SetupPlan{Name: "aliases", Repos: []SetupRepo{{Source: b}, {Source: a}}})
	if err != nil {
		t.Fatal(err)
	}
	if p.Repos[0].Alias == p.Repos[1].Alias || p.Repos[0].Alias != reverse.Repos[1].Alias {
		t.Fatal("unstable aliases")
	}
	explicit := repoSlug(filepath.Base(a))
	p, err = s.previewSetup(SetupPlan{Name: "aliases", Repos: []SetupRepo{{Source: a}, {Source: b, Alias: explicit}}})
	if err != nil || p.Repos[1].Alias != explicit || p.Repos[0].Alias == explicit {
		t.Fatal(p, err)
	}
}

func TestWorkspaceLegacyAttachDetachRefreshAndPreserveConversation(t *testing.T) {
	s, w := workspaceFixture(t)
	if _, err := s.db.Exec(`UPDATE agent_sessions SET native_id='native-exact-conversation' WHERE id=?`, w.Agents[0].ID); err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	source, _ := setupFixture(t, "main")
	if err := worktreeCommand(s, []string{"checkout", "attach", w.ID, "api", source}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(w.Cwd, "repos", "api")
	if target, err := os.Readlink(link); err != nil || target != source {
		t.Fatal(target, err)
	}
	if err := worktreeCommand(s, []string{"checkout", "detach", w.ID, "api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("detached shortcut remains")
	}
	data, err := os.ReadFile(filepath.Join(w.Cwd, "WORKSPACE.md"))
	if err != nil || strings.Contains(string(data), source) {
		t.Fatal("map advertises detached checkout", err)
	}
	if _, err := os.Stat(filepath.Join(source, ".git")); err != nil {
		t.Fatal("borrowed contents removed")
	}
	after, err := s.Worktree(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Agents[0].ID != w.Agents[0].ID || after.Agents[0].NativeID != w.Agents[0].NativeID || after.Cwd != w.Cwd {
		t.Fatal("legacy attachment changed conversation")
	}
	explicit := t.TempDir()
	old, err := s.createNamedRoot("explicit-attach", explicit)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"checkout", "attach", old.ID, "api", source}, {"checkout", "detach", old.ID, "api"}} {
		if err = worktreeCommand(s, args); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(explicit)
	if err != nil || len(entries) != 0 {
		t.Fatal("explicit cwd was written", err)
	}
	// The same readable alias can be reattached after a successful retirement.
	if err := worktreeCommand(s, []string{"checkout", "attach", w.ID, "api", source}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(link, link+".owned"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("user replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	err = worktreeCommand(s, []string{"checkout", "detach", w.ID, "api"})
	if err == nil || !strings.Contains(err.Error(), "publication incomplete") {
		t.Fatal("partial publication not surfaced", err)
	}
	data, err = os.ReadFile(link)
	if err != nil || string(data) != "user replacement" {
		t.Fatal("modified shortcut removed", err)
	}
}

func TestWorkspaceRetirementReplacementRacePreservesUnverifiedContents(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			_, w := workspaceFixture(t)
			path := filepath.Join(w.Cwd, "repos")
			fd, err := workspaceOpenDir(path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			target := t.TempDir()
			alias := filepath.Join(path, "api")
			if err = os.Symlink(target, alias); err != nil {
				t.Fatal(err)
			}
			expected, err := workspaceReadLink(fd, "api")
			if err != nil {
				t.Fatal(err)
			}
			// Deterministic replacement after publication's identity precheck and before
			// the retirement helper's rename, without a probabilistic timing loop.
			if err = os.Rename(alias, alias+".owned"); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				err = os.WriteFile(alias, []byte("preserve me"), 0600)
			case "symlink":
				err = os.Symlink(target+"/other", alias)
			case "directory":
				err = os.Mkdir(alias, 0700)
				if err == nil {
					err = os.WriteFile(filepath.Join(alias, "child"), []byte("preserve me"), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			err = workspaceRetire(fd, w.Cwd, "api", expected)
			if err == nil || !strings.Contains(err.Error(), "unverified contents preserved at") {
				t.Fatal("race not surfaced", err)
			}
			matches, e := filepath.Glob(filepath.Join(path, ".retire-*", "entry"))
			if e != nil || len(matches) != 1 || !strings.Contains(err.Error(), matches[0]) {
				t.Fatal(matches, err, e)
			}
			if kind == "symlink" {
				got, e := os.Readlink(matches[0])
				if e != nil || got != target+"/other" {
					t.Fatal(got, e)
				}
			} else {
				file := matches[0]
				if kind == "directory" {
					file = filepath.Join(file, "child")
				}
				got, e := os.ReadFile(file)
				if e != nil || string(got) != "preserve me" {
					t.Fatal(string(got), e)
				}
			}
			if _, err = os.Stat(target); err != nil {
				t.Fatal("target removed", err)
			}
		})
	}
}
