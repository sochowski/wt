package main

// Setup creates isolated checkouts, never adopts a pre-existing branch/path.
// Provenance deliberately does not confer deletion authority: attachments retain
// the existing borrowed lifecycle. Each Git boundary is journaled before use.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type SetupRepo struct {
	Source      string `json:"source"`
	Alias       string `json:"alias"`
	Remote      string `json:"remote"`
	Base        string `json:"base"` // empty means remote HEAD, resolved at confirmed execution
	Cached      bool   `json:"cached"`
	Identity    string `json:"identity"`
	Key         string `json:"key"`
	Destination string `json:"destination"`
	SelectedRef string `json:"selected_ref"`
	SHA         string `json:"sha"`
	CheckoutID  string `json:"checkout_id"`
	State       string `json:"state"`
	Problem     string `json:"problem"`
}
type SetupPlan struct {
	ID     string      `json:"id"`
	RootID string      `json:"root_id"`
	Name   string      `json:"name"`
	Repos  []SetupRepo `json:"repos"`
}

// Git is noninteractive and bounded. Inherited Git routing variables must not
// redirect an explicit -C source to the caller's index or working tree.
func setupGit(path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// The explicit flag also fails closed on Git versions that do not support
	// disabling lazy fetch, rather than silently ignoring an environment knob.
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-lazy-fetch", "-C", path}, args...)...)
	// A hung transport helper must not keep inherited output pipes open forever
	// after the Git process has been cancelled.
	cmd.WaitDelay = 2 * time.Second
	for _, e := range os.Environ() {
		key := strings.SplitN(e, "=", 2)[0]
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE", "GIT_TERMINAL_PROMPT", "GIT_NO_LAZY_FETCH":
			continue
		}
		cmd.Env = append(cmd.Env, e)
	}
	// Object inspection and checkout must never trigger an implicit promisor
	// fetch. Only the explicitly confirmed fetch command may use transport.
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSuffix(string(out), "\n")
	if err != nil {
		if len(text) > 4096 {
			text = text[:4096]
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}
func setupIdentity(source string) (string, string, error) {
	path, err := canonicalDir(source)
	if err != nil {
		return "", "", err
	}
	top, err := setupGit(path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("source must be a working Git repository (%s): %w", path, err)
	}
	top, err = canonicalDir(top)
	if err != nil {
		return "", "", err
	}
	common, err := setupGit(top, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	common, err = canonicalDir(common)
	return top, common, err
}
func repoSlug(name string) string {
	var b strings.Builder
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		out = "repo"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// Resolve existing ancestors without creating them; prevents symlink aliases in
// the reviewed destination and makes special characters ordinary path bytes.
func setupAbsolute(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err = os.Lstat(path); err == nil {
		return filepath.EvalSymlinks(path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent, err := setupAbsolute(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
func setupCollision(r SetupRepo, name string) error {
	resolved, err := setupAbsolute(filepath.Dir(r.Destination))
	if err != nil {
		return err
	}
	if resolved != filepath.Dir(r.Destination) {
		return errors.New("destination ancestor changed; review a new plan")
	}
	if _, err := os.Lstat(r.Destination); err == nil {
		return fmt.Errorf("destination already exists: %s", r.Destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := setupGit(r.Source, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		return fmt.Errorf("branch already exists: %s", name)
	}
	return nil
}
func (s *Store) previewSetup(p SetupPlan) (SetupPlan, error) {
	if !safeName.MatchString(p.Name) {
		return p, errors.New("session name must be 1..80 letters/digits/underscore/hyphen")
	}
	if len(p.Repos) > 32 {
		return p, errors.New("setup limit: 32 repositories")
	}
	if len(p.Repos) > 0 {
		if _, err := setupGit(".", "check-ref-format", "--branch", p.Name); err != nil {
			return p, fmt.Errorf("validate session name as Git branch name: %w", err)
		}
	}
	aliases, identities := map[string]bool{}, map[string]bool{}
	if p.RootID != "" {
		w, e := s.Worktree(p.RootID)
		if e != nil {
			return p, e
		}
		if w.Name != p.Name {
			return p, errors.New("setup must use existing session name")
		}
		p.RootID = w.ID
		for _, c := range w.Checkouts {
			aliases[c.Alias] = true
		}
	}
	base := os.Getenv("WT_BASE_DIR")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), "worktrees")
	}
	base, err := setupAbsolute(base)
	if err != nil {
		return p, err
	}
	counts := map[string]int{}
	for i := range p.Repos {
		r := &p.Repos[i]
		r.Source, r.Identity, err = setupIdentity(r.Source)
		if err != nil {
			return p, err
		}
		if identities[r.Identity] {
			return p, errors.New("same repository selected twice")
		}
		identities[r.Identity] = true
		hash := sha256.Sum256([]byte(r.Identity))
		r.Key = repoSlug(filepath.Base(r.Source)) + "-" + hex.EncodeToString(hash[:6])
		counts[repoSlug(filepath.Base(r.Source))]++
		if r.Alias != "" {
			if !safeName.MatchString(r.Alias) || r.Alias == "root" || aliases[r.Alias] {
				return p, fmt.Errorf("invalid, reserved or duplicate alias: %s", r.Alias)
			}
			aliases[r.Alias] = true
		}
	}
	for i := range p.Repos {
		r := &p.Repos[i]
		if r.Alias == "" {
			alias := repoSlug(filepath.Base(r.Source))
			if counts[alias] > 1 || aliases[alias] || alias == "root" {
				parent := repoSlug(filepath.Base(filepath.Dir(r.Source)))
				if len(parent) > 20 {
					parent = parent[:20]
				}
				alias += "-" + parent + "-" + r.Key[len(r.Key)-6:]
			}
			if aliases[alias] {
				alias = r.Key
			}
			if aliases[alias] {
				return p, fmt.Errorf("alias collision: choose an explicit alias for %s", r.Source)
			}
			r.Alias = alias
			aliases[alias] = true
		}
		r.Destination = filepath.Join(base, r.Key, p.Name)
		if err = setupCollision(*r, p.Name); err != nil {
			return p, err
		}
		if r.Remote == "" {
			remotes, _ := setupGit(r.Source, "remote")
			for _, remote := range strings.Fields(remotes) {
				if remote == "origin" {
					r.Remote = "origin"
				}
			}
			if r.Remote == "" && len(strings.Fields(remotes)) == 1 {
				r.Remote = strings.TrimSpace(remotes)
			}
		}
		if r.Remote != "" {
			if strings.HasPrefix(r.Remote, "-") {
				return p, errors.New("invalid remote")
			}
			if _, err = setupGit(r.Source, "remote", "get-url", r.Remote); err != nil {
				return p, err
			}
		}
		if r.Base != "" {
			if strings.HasPrefix(r.Base, "-") || strings.ContainsAny(r.Base, "\x00\r\n") {
				return p, errors.New("invalid base")
			}
			if !r.Cached {
				if _, err = setupGit(r.Source, "check-ref-format", "refs/heads/"+r.Base); err != nil {
					return p, errors.New("fetched base override must be a remote branch name")
				}
			}
		}
		if r.Remote == "" && (!r.Cached || r.Base == "") {
			return p, errors.New("no selected remote: choose a remote, or explicitly accept a cached base override")
		}
		r.SelectedRef = setupCachedRef(*r)
		r.SHA = ""
		if r.SelectedRef != "" {
			r.SHA, _ = setupGit(r.Source, "rev-parse", "--verify", r.SelectedRef+"^{commit}")
		}
		if r.Cached && r.SHA == "" {
			return p, errors.New("no usable cached commit; choose an explicit base or fetch")
		}
		r.State = "not_attempted"
		r.Problem = ""
		r.CheckoutID = ""
	}
	p.ID = ""
	return p, nil
}
func setupCachedRef(r SetupRepo) string {
	if r.Base != "" {
		if r.Cached {
			return r.Base
		}
		return "refs/remotes/" + r.Remote + "/" + r.Base
	}
	ref, _ := setupGit(r.Source, "symbolic-ref", "--quiet", "refs/remotes/"+r.Remote+"/HEAD")
	return ref
}
func (s *Store) saveSetup(p SetupPlan) error {
	result, err := s.db.Exec(`UPDATE checkout_setups SET plan=? WHERE id=? AND root_id=?`, jsonText(p), p.ID, p.RootID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return errors.New("setup journal disappeared; stop and reconcile recorded resources")
	}
	return err
}
func (s *Store) beginSetup(p SetupPlan) (SetupPlan, error) {
	if p.RootID == "" {
		return p, errors.New("confirmed setup needs a root")
	}
	accepted := append([]SetupRepo(nil), p.Repos...)
	p, err := s.previewSetup(p)
	if err != nil {
		return p, err
	}
	for i := range p.Repos {
		before, after := accepted[i], p.Repos[i]
		// Raw noninteractive begin requests are validated here. A request that
		// carries preview provenance must still describe exactly that plan.
		if before.Identity != "" || before.Key != "" || before.Destination != "" {
			if before.Source != after.Source || before.Identity != after.Identity || before.Key != after.Key || before.Destination != after.Destination {
				return p, errors.New("reviewed source or destination changed; review a new plan")
			}
		}
		if p.Repos[i].Cached && accepted[i].SHA != "" {
			sha, e := setupGit(p.Repos[i].Source, "rev-parse", "--verify", accepted[i].SHA+"^{commit}")
			if e != nil || sha != accepted[i].SHA {
				return p, errors.New("accepted cached SHA is no longer usable")
			}
			p.Repos[i].SHA = sha
		}
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM checkout_setups WHERE root_id=?`, p.RootID).Scan(&count); err != nil {
		return p, err
	}
	if count >= 32 {
		return p, errors.New("setup journal limit: 32 batches per root; inspect existing setup records")
	}
	p.ID = newID()
	for i := range p.Repos {
		p.Repos[i].CheckoutID = newID()
	}
	_, err = s.db.Exec(`INSERT INTO checkout_setups(id,root_id,plan) VALUES(?,?,?)`, p.ID, p.RootID, jsonText(p))
	return p, err
}
func (s *Store) loadSetup(id string) (SetupPlan, error) {
	var p SetupPlan
	var text string
	err := s.db.QueryRow(`SELECT plan FROM checkout_setups WHERE id=?`, id).Scan(&text)
	if err == nil {
		err = json.Unmarshal([]byte(text), &p)
	}
	return p, err
}
func setupMarker(r SetupRepo) (string, error) {
	dir, err := setupGit(r.Destination, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wt-setup"), nil
}
func verifySetupResource(p SetupPlan, r SetupRepo) error {
	top, identity, err := setupIdentity(r.Destination)
	if err != nil {
		return err
	}
	if top != r.Destination || identity != r.Identity {
		return errors.New("checkout source identity/path mismatch")
	}
	registered, err := setupGit(r.Source, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return err
	}
	found := false
	for _, record := range strings.Split(registered, "\x00\x00") {
		fields := strings.Split(record, "\x00")
		if len(fields) > 0 && fields[0] == "worktree "+r.Destination {
			found = true
		}
	}
	if !found {
		return errors.New("checkout not registered at recorded destination; manual reconciliation required")
	}
	marker, err := setupMarker(r)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(marker)
	if err != nil || string(b) != p.ID+":"+r.CheckoutID {
		return errors.New("unverified checkout: setup marker missing/mismatched; manual reconciliation required")
	}
	branch, err := setupGit(r.Destination, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || branch != "refs/heads/"+p.Name {
		return errors.New("checkout branch mismatch; manual reconciliation required")
	}
	if r.State != "attached" {
		sha, e := setupGit(r.Destination, "rev-parse", "HEAD")
		if e != nil || sha != r.SHA {
			return errors.New("unattached checkout base changed; manual reconciliation required")
		}
	}
	return nil
}

// runSetup stops at the first failure. A crash in 'creating' never causes a
// second worktree add. Only our token and Git identity can reconcile completion.
func (s *Store) runSetup(p SetupPlan, progress func(SetupRepo)) (SetupPlan, error) {
	w, err := s.Worktree(p.RootID)
	if err != nil {
		return p, err
	}
	if err = s.refreshWorkspace(w); err != nil {
		return p, err
	}
	// Revalidate the whole remaining batch before any fetch, also on resume.
	for i := range p.Repos {
		r := &p.Repos[i]
		if r.State != "not_attempted" && r.State != "fetching" {
			continue
		}
		_, identity, e := setupIdentity(r.Source)
		if e == nil && identity != r.Identity {
			e = errors.New("source identity changed")
		}
		if e == nil {
			e = setupCollision(*r, p.Name)
		}
		if e != nil {
			r.Problem = e.Error()
			err := s.saveSetup(p)
			return p, err
		}
	}
	for i := range p.Repos {
		r := &p.Repos[i]
		if r.State == "skipped" {
			continue
		}
		fail := func(err error) (SetupPlan, error) {
			r.Problem = err.Error()
			e := s.saveSetup(p)
			if progress != nil {
				progress(*r)
			}
			return p, e
		}
		if progress != nil {
			progress(*r)
		}
		if r.State == "attached" {
			if err := verifySetupResource(p, *r); err != nil {
				return fail(err)
			}
			var path string
			if err := s.db.QueryRow(`SELECT path FROM checkouts WHERE id=? AND root_id=? AND alias=?`, r.CheckoutID, p.RootID, r.Alias).Scan(&path); err != nil || path != r.Destination {
				return fail(errors.New("recorded attachment was removed or changed; manual reconciliation required"))
			}
			r.Problem = ""
			if err := s.saveSetup(p); err != nil {
				return p, err
			}
			continue
		}
		source, identity, err := setupIdentity(r.Source)
		if err != nil {
			return fail(err)
		}
		if source != r.Source || identity != r.Identity {
			return fail(errors.New("source identity changed"))
		}
		if r.State == "creating" || r.State == "created" {
			if err = verifySetupResource(p, *r); err != nil {
				return fail(err)
			}
		} else {
			if err = setupCollision(*r, p.Name); err != nil {
				return fail(err)
			}
			r.Problem = ""
			r.State = "fetching"
			if err = s.saveSetup(p); err != nil {
				return p, err
			}
			ref := r.SelectedRef
			if !r.Cached {
				branch := r.Base
				if branch == "" {
					out, e := setupGit(r.Source, "ls-remote", "--symref", r.Remote, "HEAD")
					if e != nil {
						return fail(e)
					}
					for _, line := range strings.Split(out, "\n") {
						fields := strings.Fields(line)
						if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" && strings.HasPrefix(fields[1], "refs/heads/") {
							branch = strings.TrimPrefix(fields[1], "refs/heads/")
						}
					}
					if branch == "" {
						return fail(errors.New("remote HEAD is not a branch; choose a base override"))
					}
				}
				ref = "refs/remotes/" + r.Remote + "/" + branch
				if target, e := setupGit(r.Source, "symbolic-ref", "--quiet", ref); e == nil {
					return fail(fmt.Errorf("refusing symbolic fetch destination %s -> %s", ref, target))
				}
				if _, err = setupGit(r.Source, "fetch", "--no-tags", "--no-recurse-submodules", "--refmap=", r.Remote, "+refs/heads/"+branch+":"+ref); err != nil {
					return fail(err)
				}
			}
			r.SelectedRef = ref
			commit := ref
			if r.Cached {
				commit = r.SHA
				if commit == "" {
					return fail(errors.New("no explicitly accepted cached SHA"))
				}
			}
			r.SHA, err = setupGit(r.Source, "rev-parse", "--verify", commit+"^{commit}")
			if err != nil {
				return fail(err)
			}
			if err = setupCollision(*r, p.Name); err != nil {
				return fail(err)
			}
			r.State = "creating"
			if err = s.saveSetup(p); err != nil {
				return p, err
			}
			if err = os.MkdirAll(filepath.Dir(r.Destination), 0755); err != nil {
				return fail(err)
			}
			if _, err = setupGit(r.Source, "-c", "core.hooksPath=/dev/null", "worktree", "add", "-b", p.Name, "--", r.Destination, r.SHA); err != nil {
				return fail(err)
			}
			marker, e := setupMarker(*r)
			if e != nil {
				return fail(e)
			}
			if err = os.WriteFile(marker, []byte(p.ID+":"+r.CheckoutID), 0600); err != nil {
				return fail(err)
			}
		}
		r.State = "created"
		r.Problem = ""
		if err = s.saveSetup(p); err != nil {
			return p, err
		}
		_, err = s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING`, r.CheckoutID, p.RootID, r.Alias, r.Destination)
		if err != nil {
			return fail(err)
		}
		var alias, path, root string
		if err = s.db.QueryRow(`SELECT root_id,alias,path FROM checkouts WHERE id=?`, r.CheckoutID).Scan(&root, &alias, &path); err != nil {
			return fail(err)
		}
		if root != p.RootID || alias != r.Alias || path != r.Destination {
			return fail(errors.New("attachment identity mismatch"))
		}
		r.State = "attached"
		if err = s.saveSetup(p); err != nil {
			return p, err
		}
		w, err = s.Worktree(p.RootID)
		if err != nil {
			return p, err
		}
		if err = s.refreshWorkspace(w); err != nil {
			return p, err
		}
		if progress != nil {
			progress(*r)
		}
	}
	return p, nil
}
func setupCommand(s *Store, args []string) error {
	if os.Getenv("WT_AGENT_ID") != "" {
		return errors.New("checkout setup is human-only")
	}
	if len(args) == 0 {
		return errors.New("setup preview|begin (JSON stdin), run|show|list|retry|cached|skip ID [INDEX]")
	}
	op := args[0]
	if op == "preview" || op == "begin" {
		var p SetupPlan
		d := json.NewDecoder(io.LimitReader(os.Stdin, 128*1024))
		d.DisallowUnknownFields()
		if err := d.Decode(&p); err != nil {
			return err
		}
		var err error
		if op == "preview" {
			p, err = s.previewSetup(p)
		} else {
			w, e := s.Worktree(p.RootID)
			if e != nil {
				return e
			}
			lock, e := s.rootLock(w)
			if e != nil {
				return e
			}
			defer lock.Close()
			p, err = s.beginSetup(p)
		}
		if err == nil {
			printJSON(p)
		}
		return err
	}
	if len(args) < 2 {
		return errors.New("setup ID/root required")
	}
	if op == "list" {
		w, err := s.Worktree(args[1])
		if err != nil {
			return err
		}
		rows, err := s.db.Query(`SELECT plan FROM checkout_setups WHERE root_id=? ORDER BY rowid`, w.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		out := []json.RawMessage{}
		for rows.Next() {
			var text string
			if err = rows.Scan(&text); err != nil {
				return err
			}
			out = append(out, json.RawMessage(text))
		}
		printJSON(out)
		return rows.Err()
	}
	p, err := s.loadSetup(args[1])
	if err != nil {
		return err
	}
	if op == "show" {
		printJSON(p)
		return nil
	}
	w, err := s.Worktree(p.RootID)
	if err != nil {
		return err
	}
	lock, err := s.rootLock(w)
	if err != nil {
		return err
	}
	defer lock.Close()
	p, err = s.loadSetup(p.ID)
	if err != nil {
		return err
	}
	if op == "run" {
		p, err = s.runSetup(p, func(r SetupRepo) {
			fmt.Fprintf(os.Stderr, "%s: %s %s\n", displayText(r.Alias), r.State, displayText(r.Problem))
		})
		printJSON(p)
		return err
	}
	if len(args) != 3 {
		return errors.New("setup action needs zero-based repository index")
	}
	var i int
	if _, err = fmt.Sscan(args[2], &i); err != nil || i < 0 || i >= len(p.Repos) {
		return errors.New("invalid repository index")
	}
	r := &p.Repos[i]
	switch op {
	case "skip":
		if r.State == "attached" || r.State == "created" || r.State == "creating" {
			return errors.New("created/uncertain resources cannot be skipped; reconcile or proceed with subset")
		}
		r.State = "skipped"
		r.Problem = ""
	case "cached":
		if r.State == "creating" || r.State == "created" || r.State == "attached" {
			return errors.New("cannot change a created base")
		}
		ref := setupCachedRef(*r)
		sha, e := setupGit(r.Source, "rev-parse", "--verify", ref+"^{commit}")
		if e != nil {
			return errors.New("no usable cached base")
		}
		r.Cached = true
		r.Base = sha
		r.SHA = sha
		r.SelectedRef = ref
		r.State = "not_attempted"
		r.Problem = ""
	case "retry":
		if r.State == "creating" || r.State == "created" || r.State == "attached" {
			return errors.New("use run to reconcile recorded resources; no blind retry")
		}
		r.State = "not_attempted"
		r.Problem = ""
	default:
		return errors.New("unknown setup action")
	}
	if err = s.saveSetup(p); err == nil {
		printJSON(p)
	}
	return err
}
