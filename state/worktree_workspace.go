package main

// Workspace publication owns only objects it created. All traversal and writes
// are relative to no-follow directory descriptors; path substitution can cause
// an error or leave a detached owned object, never redirect a write to a project.
import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type workspaceObject struct {
	Dev uint64 `json:"dev"`
	Ino uint64 `json:"ino"`
}
type workspaceLink struct {
	workspaceObject
	Target string `json:"target"`
}
type workspaceRecord struct {
	Home    string                     `json:"home"`
	MapHash string                     `json:"map_hash"`
	Objects map[string]workspaceObject `json:"objects"`
	Links   map[string]workspaceLink   `json:"links"`
}
type WorkspaceInfo struct {
	Home    string `json:"home,omitempty"`
	Scratch string `json:"scratch,omitempty"`
}

func workspaceStat(fd int) (workspaceObject, error) {
	var st unix.Stat_t
	err := unix.Fstat(fd, &st)
	return workspaceObject{uint64(st.Dev), uint64(st.Ino)}, err
}
func workspaceDirAt(fd int, name string) (int, error) {
	return unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}
func workspaceOpenDir(path string, create bool) (int, error) {
	if !filepath.IsAbs(path) {
		return -1, errors.New("workspace path must be absolute")
	}
	fd, err := workspaceDirAt(unix.AT_FDCWD, "/")
	if err != nil {
		return -1, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/") {
		if part == "" {
			continue
		}
		next, e := workspaceDirAt(fd, part)
		if e == unix.ENOENT && create {
			e = unix.Mkdirat(fd, part, 0700)
			if e == nil || e == unix.EEXIST {
				next, e = workspaceDirAt(fd, part)
			}
		}
		unix.Close(fd)
		if e != nil {
			return -1, fmt.Errorf("workspace directory %s: %w", path, e)
		}
		fd = next
	}
	return fd, nil
}
func workspaceBase() (string, error) {
	base := os.Getenv("WT_BASE_DIR")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), "worktrees")
	}
	return filepath.Abs(base)
}
func (s *Store) workspaceRecord(root string) (workspaceRecord, bool, error) {
	var text string
	var r workspaceRecord
	err := s.db.QueryRow(`SELECT record FROM workspace_homes WHERE root_id=?`, root).Scan(&text)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	if len(text) > 1024*1024 {
		return r, false, errors.New("workspace ownership record too large")
	}
	err = json.Unmarshal([]byte(text), &r)
	return r, true, err
}
func (s *Store) saveWorkspace(root string, r workspaceRecord) error {
	text := jsonText(r)
	if len(text) > 1024*1024 {
		return errors.New("workspace ownership record too large")
	}
	_, err := s.db.Exec(`INSERT INTO workspace_homes(root_id,record) VALUES(?,?) ON CONFLICT(root_id) DO UPDATE SET record=excluded.record`, root, text)
	return err
}

// The shared .sessions container is also journaled: a pre-existing unrelated
// directory is not implicitly adopted. Never remove partial resources on error.
func (s *Store) provisionWorkspace(name string) (workspaceRecord, error) {
	r := workspaceRecord{Objects: map[string]workspaceObject{}, Links: map[string]workspaceLink{}}
	base, err := workspaceBase()
	if err != nil {
		return r, err
	}
	fd, err := workspaceOpenDir(base, true)
	if err != nil {
		return r, err
	}
	defer unix.Close(fd)
	container := filepath.Join(base, ".sessions")
	var text string
	err = s.db.QueryRow(`SELECT identity FROM workspace_containers WHERE path=?`, container).Scan(&text)
	var expected workspaceObject
	if err == sql.ErrNoRows {
		if err = unix.Mkdirat(fd, ".sessions", 0700); err != nil {
			return r, fmt.Errorf("workspace container conflict (not adopted): %w", err)
		}
	} else if err != nil {
		return r, err
	} else if err = json.Unmarshal([]byte(text), &expected); err != nil {
		return r, err
	}
	parent, err := workspaceDirAt(fd, ".sessions")
	if err != nil {
		return r, err
	}
	defer unix.Close(parent)
	actual, err := workspaceStat(parent)
	if err != nil {
		return r, err
	}
	if text != "" && expected != actual {
		return r, errors.New("workspace container identity changed")
	}
	if text == "" {
		if _, err = s.db.Exec(`INSERT INTO workspace_containers(path,identity) VALUES(?,?)`, container, jsonText(actual)); err != nil {
			return r, err
		}
	}
	if err = unix.Mkdirat(parent, name, 0700); err != nil {
		return r, fmt.Errorf("workspace home conflict (not adopted): %w", err)
	}
	home, err := workspaceDirAt(parent, name)
	if err != nil {
		return r, err
	}
	defer unix.Close(home)
	r.Home = filepath.Join(container, name)
	r.MapHash = fmt.Sprintf("%x", sha256.Sum256(nil))
	r.Objects["."], err = workspaceStat(home)
	if err != nil {
		return r, err
	}
	for _, dir := range []string{"repos", "scratch"} {
		if err = unix.Mkdirat(home, dir, 0700); err != nil {
			return r, err
		}
		child, e := workspaceDirAt(home, dir)
		if e != nil {
			return r, e
		}
		r.Objects[dir], err = workspaceStat(child)
		unix.Close(child)
		if err != nil {
			return r, err
		}
	}
	file, err := unix.Openat(home, "WORKSPACE.md", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return r, err
	}
	r.Objects["WORKSPACE.md"], err = workspaceStat(file)
	unix.Close(file)
	return r, err
}
func workspaceCheck(fd int, expected workspaceObject) error {
	actual, err := workspaceStat(fd)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("workspace object identity changed; resources preserved")
	}
	return nil
}
func workspaceReadLink(fd int, name string) (workspaceLink, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return workspaceLink{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFLNK {
		return workspaceLink{}, errors.New("workspace shortcut replaced by non-symlink")
	}
	b := make([]byte, 65536)
	n, err := unix.Readlinkat(fd, name, b)
	if err != nil {
		return workspaceLink{}, err
	}
	return workspaceLink{workspaceObject{uint64(st.Dev), uint64(st.Ino)}, string(b[:n])}, nil
}

// Move the exposed alias out of the way before verifying it for deletion.
// A replacement raced into that name is preserved in quarantine, never unlinked.
// This protects unverified contents; it is not isolation from hostile same-user
// processes controlling WT's newly created private quarantine directory.
func workspaceRetire(repos int, home, alias string, expected workspaceLink) error {
	quarantine := ".retire-" + newID()
	if err := unix.Mkdirat(repos, quarantine, 0700); err != nil {
		return err
	}
	fd, err := workspaceDirAt(repos, quarantine)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	recovery := filepath.Join(home, "repos", quarantine, "entry")
	if err = unix.Renameat(repos, alias, fd, "entry"); err != nil {
		return fmt.Errorf("shortcut retirement incomplete (%s): %w", recovery, err)
	}
	actual, err := workspaceReadLink(fd, "entry")
	if err != nil || actual != expected {
		return fmt.Errorf("shortcut changed during retirement; unverified contents preserved at %s; reconcile manually", recovery)
	}
	if err = unix.Unlinkat(fd, "entry", 0); err != nil {
		return fmt.Errorf("owned shortcut preserved at %s: %w", recovery, err)
	}
	return unix.Unlinkat(repos, quarantine, unix.AT_REMOVEDIR)
}
func (s *Store) refreshWorkspace(w Worktree) error {
	// Callers may hold a pre-Add snapshot; always publish durable attachments.
	current, err := s.Worktree(w.ID)
	if err != nil {
		return err
	}
	w = current
	r, owned, err := s.workspaceRecord(w.ID)
	if err != nil || !owned {
		return err
	}
	if r.Home != w.Cwd {
		return errors.New("owned workspace cwd changed")
	}
	home, err := workspaceOpenDir(r.Home, false)
	if err != nil {
		return err
	}
	defer unix.Close(home)
	if err = workspaceCheck(home, r.Objects["."]); err != nil {
		return err
	}
	repos, err := workspaceDirAt(home, "repos")
	if err != nil {
		return err
	}
	defer unix.Close(repos)
	if err = workspaceCheck(repos, r.Objects["repos"]); err != nil {
		return err
	}
	scratch, err := workspaceDirAt(home, "scratch")
	if err != nil {
		return err
	}
	defer unix.Close(scratch)
	if err = workspaceCheck(scratch, r.Objects["scratch"]); err != nil {
		return err
	}
	// Open without truncation; verify before writing. A substituted symlink,
	// unrelated inode or hardlink is never a destination for map publication.
	fd, err := unix.Openat(home, "WORKSPACE.md", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "WORKSPACE.md")
	defer file.Close()
	if err = workspaceCheck(fd, r.Objects["WORKSPACE.md"]); err != nil {
		return err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 {
		return errors.New("workspace map is not an exclusively owned regular file")
	}
	prior, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil {
		return err
	}
	if len(prior) > 1024*1024 || fmt.Sprintf("%x", sha256.Sum256(prior)) != r.MapHash {
		return errors.New("workspace map content changed; user edits preserved")
	}
	for alias, expected := range r.Links {
		actual, e := workspaceReadLink(repos, alias)
		if e != nil {
			return e
		}
		if actual != expected {
			return errors.New("workspace shortcut identity changed")
		}
	}
	currentAliases := map[string]bool{}
	for _, c := range w.Checkouts {
		currentAliases[c.Alias] = true
	}
	for alias, expected := range r.Links {
		if currentAliases[alias] {
			continue
		}
		if err = workspaceRetire(repos, r.Home, alias, expected); err != nil {
			return err
		}
		delete(r.Links, alias)
		if err = s.saveWorkspace(w.ID, r); err != nil {
			return err
		}
	}
	for _, c := range w.Checkouts {
		if !safeName.MatchString(c.Alias) || c.Alias == "root" {
			return errors.New("unsafe workspace alias")
		}
		if link, ok := r.Links[c.Alias]; ok {
			if link.Target != c.Path {
				return errors.New("workspace alias target changed")
			}
			continue
		}
		if len(r.Links) >= 1024 {
			return errors.New("workspace shortcut limit (1024)")
		}
		if err = unix.Symlinkat(c.Path, repos, c.Alias); err != nil {
			return fmt.Errorf("workspace shortcut conflict %s: %w", c.Alias, err)
		}
		link, e := workspaceReadLink(repos, c.Alias)
		if e != nil {
			return e
		}
		if link.Target != c.Path {
			return errors.New("workspace shortcut changed during publication")
		}
		r.Links[c.Alias] = link
		if err = s.saveWorkspace(w.ID, r); err != nil {
			return err
		}
	}
	data := struct {
		Session   string     `json:"session"`
		Cwd       string     `json:"cwd"`
		Scratch   string     `json:"scratch"`
		Checkouts []Checkout `json:"checkouts"`
	}{w.Name, w.Cwd, filepath.Join(r.Home, "scratch"), w.Checkouts}
	encoded, err := json.MarshalIndent(data, "    ", "  ")
	if err != nil {
		return err
	}
	content := "# Workspace data\n\n    " + string(encoded) + "\n"
	// Write only the already-open verified object, never reopen the pathname.
	if _, err = file.Seek(0, 0); err != nil {
		return err
	}
	if err = file.Truncate(0); err != nil {
		return err
	}
	if _, err = file.WriteString(content); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	r.MapHash = fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	return s.saveWorkspace(w.ID, r)
}
