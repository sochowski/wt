package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Store) scratchName() (string, error) {
	name := "scratch"
	for n := 2; ; n++ {
		_, exists, err := s.Get(name)
		if err != nil {
			return "", err
		}
		if !exists {
			return name, nil
		}
		name = fmt.Sprintf("scratch-%d", n)
	}
}
func (s *Store) createNamedRoot(name, cwd string) (Worktree, error) {
	var w Worktree
	if os.Getenv("WT_AGENT_ID") != "" {
		return w, errors.New("root creation is human-only")
	}
	if name != "" && !safeName.MatchString(name) {
		return w, errors.New("invalid session name")
	}
	var path string
	var err error
	if cwd != "" {
		path, err = canonicalDir(cwd)
		if err != nil {
			return w, err
		}
	}
	lock, err := lockFile(dbPath() + ".create.lock")
	if err != nil {
		return w, err
	}
	defer lock.Close()
	if name == "" {
		name, err = s.scratchName()
		if err != nil {
			return w, err
		}
	}
	if _, exists, err := s.Get(name); err != nil {
		return w, err
	} else if exists {
		return w, errors.New("session already exists")
	}
	var identityCollision int
	if err = s.db.QueryRow(`SELECT count(*) FROM roots WHERE id=?`, name).Scan(&identityCollision); err != nil {
		return w, err
	}
	if identityCollision != 0 {
		return w, errors.New("session name collides with an existing root ID; choose a different name")
	}
	// A runtime collision is never an invitation to adopt it, even while offline.
	if _, err := tmux("has-session", "-t", "="+name); err == nil {
		return w, errors.New("tmux session name already exists")
	}
	var workspace workspaceRecord
	if cwd == "" {
		workspace, err = s.provisionWorkspace(name)
		if err != nil {
			return w, err
		}
		path = workspace.Home
	}
	if _, err = s.Set(name, map[string]string{"kind": "workspace", "workspace_path": path, "agent": "pi", "status": "idle"}); err != nil {
		return w, err
	}
	if _, err = s.db.Exec(`UPDATE roots SET agent_first=1 WHERE name=?`, name); err != nil {
		return w, err
	}
	w, err = s.Worktree(name)
	if err != nil {
		return w, err
	}
	if cwd == "" {
		if err = s.saveWorkspace(w.ID, workspace); err != nil {
			return w, err
		}
		if err = s.refreshWorkspace(w); err != nil {
			return w, err
		}
	}
	return s.Worktree(name)
}

// fzf carries only numeric/opaque tokens, never shell-evaluated source paths.
// Esc is distinct from an empty accepted query or an explicit no-repo choice.
func setupChoose(header string, rows []string, multi bool) ([]int, error) {
	args := []string{"--layout=reverse", "--delimiter=\t", "--with-nth=2..", "--bind=esc:abort"}
	if strings.Contains(header, "\n") {
		file, err := os.CreateTemp("", "wt-setup-review-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(file.Name())
		_, err = file.WriteString(displayMultiline(header))
		file.Close()
		if err != nil {
			return nil, err
		}
		args = append(args, "--header=Review · Enter selects action · Shift-Up/Down scroll details · Esc cancels", "--preview=cat -- "+shellQuote(file.Name()), "--preview-window=right,70%,wrap", "--bind=shift-up:preview-up,shift-down:preview-down")
	} else {
		args = append(args, "--header="+displayText(header))
	}
	if multi {
		args = append(args, "--multi", "--bind=tab:toggle,shift-tab:toggle")
	}
	args = append(args, fzfVimArgs(false, multi)...)
	cmd := exec.Command("fzf", args...)
	var input strings.Builder
	for i, row := range rows {
		fmt.Fprintf(&input, "%d\t%s\n", i, displayText(row))
	}
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	selected := []int{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, err := strconv.Atoi(strings.SplitN(line, "\t", 2)[0])
		if err != nil || id < 0 || id >= len(rows) {
			return nil, errors.New("invalid menu selection")
		}
		selected = append(selected, id)
	}
	return selected, nil
}
func setupInput(header, value string) (string, error) {
	args := []string{"--phony", "--print-query", "--query=" + value, "--header=" + displayMultiline(header) + " · Text input (not a Vim editor)", "--bind=enter:accept,esc:abort", "--layout=reverse"}
	args = append(args, fzfVimArgs(true, false)...)
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader("\n")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.SplitN(string(out), "\n", 2)[0], nil
}
func discoverSetupRepos() []string {
	roots := strings.Split(os.Getenv("WT_REPO_DIRS"), ":")
	if os.Getenv("WT_REPO_DIRS") == "" {
		roots = nil
		for _, d := range []string{"gt", "code", "projects", "src", "dev", "work", "workspace", "repos"} {
			roots = append(roots, filepath.Join(os.Getenv("HOME"), d))
		}
	}
	seen := map[string]bool{}
	out := []string{}
	add := func(path string) {
		if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			return
		}
		top, _, err := setupIdentity(path)
		if err == nil && !seen[top] {
			seen[top] = true
			out = append(out, top)
		}
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		add(root)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(root, entry.Name())
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				add(path)
			}
		}
	}
	return out
}

// Only repository rows enter fzf's one live selection set. The explicit actions
// serialize its count as well: fzf otherwise returns the cursor when zero marked.
func setupRepoPicker(sources []string, selected map[string]bool) ([]int, string, error) {
	args := []string{"--layout=reverse", "--sync", "--multi", "--delimiter=\t", "--with-nth=2..", "--bind=esc:abort,tab:toggle,shift-tab:toggle", "--header=Repositories · Tab marks (no move) · Enter reviews marks, including zero\nCtrl-O: Add manual path · Ctrl-X: Start without repositories"}
	args = append(args, fzfVimArgs(false, true)...)
	if len(fzfVimArgs(false, true)) > 0 {
		args = append(args, "--header=Repositories · NORMAL Space/Tab marks (no move) · / or i filters · Enter reviews marks, including zero\nCtrl-O: Add manual path · Ctrl-X: Start without repositories")
	}
	for _, action := range []struct{ key, name string }{{"enter", "review"}, {"ctrl-o", "manual"}, {"ctrl-x", "none"}} {
		args = append(args, "--bind="+action.key+":transform:printf 'print("+action.name+":%s)+accept' \"$FZF_SELECT_COUNT\"")
	}
	var input strings.Builder
	initial := []string{}
	for i, source := range sources {
		fmt.Fprintf(&input, "%d\t%s\n", i, displayText(source))
		if selected[source] {
			initial = append(initial, fmt.Sprintf("pos(%d)+select", i+1))
		}
	}
	initial = append(initial, "first")
	args = append(args, "--bind=start:+"+strings.Join(initial, "+"))
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(input.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		// fzf exits 1 on an empty match set even when print(action) succeeded.
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || len(out) == 0 {
			return nil, "", err
		}
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	action, countText, ok := strings.Cut(lines[0], ":")
	count, e := strconv.Atoi(countText)
	if !ok || e != nil || count < 0 || count > len(sources) || (action != "review" && action != "manual" && action != "none") {
		return nil, "", errors.New("invalid repository action")
	}
	picked := []int{}
	if action == "none" || count == 0 {
		return picked, action, nil
	}
	if len(lines)-1 != count {
		return nil, "", errors.New("repository mark count mismatch")
	}
	seen := map[int]bool{}
	for _, line := range lines[1:] {
		id, e := strconv.Atoi(strings.SplitN(line, "\t", 2)[0])
		if e != nil || id < 0 || id >= len(sources) || seen[id] {
			return nil, "", errors.New("invalid repository mark")
		}
		seen[id] = true
		picked = append(picked, id)
	}
	return picked, action, nil
}
func setupSelectRepos(prior []SetupRepo, offline bool) ([]SetupRepo, error) {
	sources := discoverSetupRepos()
	selected := map[string]bool{}
	previous := map[string]SetupRepo{}
	for _, r := range prior {
		previous[r.Source] = r
		selected[r.Source] = true
		found := false
		for _, source := range sources {
			if source == r.Source {
				found = true
			}
		}
		if !found {
			sources = append(sources, r.Source)
		}
	}
	for {
		picked, action, err := setupRepoPicker(sources, selected)
		if err != nil {
			return nil, err
		}
		selected = map[string]bool{}
		for _, i := range picked {
			selected[sources[i]] = true
		}
		if action == "manual" {
			path, e := setupInput("Full working repository path · Enter accept · Esc cancels (in NORMAL when Vim enabled)", "")
			if e != nil {
				if setupMenuExit(e) == nil {
					continue
				}
				return nil, e
			}
			top, _, e := setupIdentity(path)
			if e != nil {
				fmt.Fprintln(os.Stderr, displayText(e.Error()))
				continue
			}
			found := false
			for _, source := range sources {
				if source == top {
					found = true
				}
			}
			if !found {
				sources = append(sources, top)
			}
			selected[top] = true
			continue
		}
		out := []SetupRepo{}
		for _, source := range sources {
			if selected[source] {
				r, ok := previous[source]
				if !ok {
					r = SetupRepo{Source: source, Cached: offline}
				}
				out = append(out, r)
			}
		}
		return out, nil
	}
}
func setupReviewText(p SetupPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session: %s · NEW isolated checkouts only\n", p.Name)
	if p.RootID == "" {
		if base, err := workspaceBase(); err == nil {
			fmt.Fprintf(&b, "New session home: %s\n", filepath.Join(base, ".sessions", p.Name))
		}
	}
	if len(p.Repos) == 0 {
		b.WriteString("No repositories · normal repo-free agent session\n")
	}
	for _, r := range p.Repos {
		ref := r.Base
		if ref == "" {
			ref = r.Remote + "/HEAD (resolved on Create; cached hint: " + r.SelectedRef + ")"
		}
		policy := "FETCH latest remote branch on Create"
		if r.Cached {
			policy = "Explicit cached commit; NOT freshness-verified"
		}
		fmt.Fprintf(&b, "%s · source %s\n  key %s · branch %s\n  base %s · cached SHA %s\n  destination %s\n  %s\n", r.Alias, r.Source, r.Key, p.Name, ref, r.SHA, r.Destination, policy)
	}
	b.WriteString("Fetch updates shared objects/remote refs only. Originals stay untouched. No automatic cleanup.")
	return b.String()
}
func setupMenu(s *Store, args []string) error {
	if os.Getenv("WT_AGENT_ID") != "" {
		return errors.New("session setup is human-only")
	}
	fs := flag.NewFlagSet("setup-menu", flag.ContinueOnError)
	root := fs.String("root", "", "add to existing session")
	focus := fs.Bool("switch", false, "focus initial agent after setup")
	offline := fs.Bool("offline", false, "explicit cached-only preparation (no network)")
	resume := fs.String("resume", "", "resume durable setup ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected menu arguments")
	}
	var p SetupPlan
	var w Worktree
	var err error
	adding := *root != "" || *resume != ""
	if *resume != "" {
		p, err = s.loadSetup(*resume)
		if err != nil {
			return err
		}
		w, err = s.Worktree(p.RootID)
		if err != nil {
			return err
		}
	} else {
		if adding {
			w, err = s.Worktree(*root)
			if err != nil {
				return err
			}
			p.Name = w.Name
			p.RootID = w.ID
		} else {
			suggestion, e := s.scratchName()
			if e != nil {
				return e
			}
			p.Name, err = setupInput("Name this task (letters/digits/hyphen/underscore) · Enter accept · Esc cancels", suggestion)
			if err != nil {
				return setupMenuExit(err)
			}
			if !safeName.MatchString(p.Name) {
				return errors.New("invalid session name")
			}
			if _, exists, e := s.Get(p.Name); e != nil {
				return e
			} else if exists {
				return errors.New("session name already exists")
			}
		}
		p.Repos, err = setupSelectRepos(nil, *offline)
		if err != nil {
			return setupMenuExit(err)
		}
		for i := range p.Repos {
			p.Repos[i].Cached = *offline
		}
		for {
			preview, e := s.previewSetup(p)
			header := setupReviewText(preview)
			if e != nil {
				header = "Cannot create: " + e.Error() + "\n" + header
			} else {
				p = preview
			}
			choices := []string{"Create", "Edit repository base / remote / alias / fetch policy", "Back to repository selection", "Cancel"}
			choice, e2 := setupChoose(header, choices, false)
			if e2 != nil {
				return setupMenuExit(e2)
			}
			if choice[0] == 3 {
				return nil
			}
			if choice[0] == 2 {
				p.Repos, err = setupSelectRepos(p.Repos, *offline)
				if err != nil {
					return setupMenuExit(err)
				}
				continue
			}
			if choice[0] == 1 {
				rows := []string{}
				for _, r := range p.Repos {
					rows = append(rows, r.Source)
				}
				if len(rows) == 0 {
					continue
				}
				selected, e := setupChoose("Select a repository to edit (optional, not a per-repo wizard)", rows, false)
				if e != nil {
					return setupMenuExit(e)
				}
				r := &p.Repos[selected[0]]
				fields, e := setupChoose("Edit "+r.Source, []string{"Base override (remote branch when fetching; commit/ref when cached)", "Remote name", "Alias", "Toggle fetch / explicitly cached"}, false)
				if e != nil {
					return setupMenuExit(e)
				}
				switch fields[0] {
				case 0:
					r.Base, e = setupInput("Base override · empty means remote default · no network now", r.Base)
				case 1:
					r.Remote, e = setupInput("Remote name", r.Remote)
				case 2:
					r.Alias, e = setupInput("Unique repository alias", r.Alias)
				case 3:
					r.Cached = !r.Cached
				}
				if e != nil {
					return setupMenuExit(e)
				}
				continue
			}
			if e != nil {
				continue
			}
			break
		}
		// No root or Git mutation occurs above this explicit confirmation boundary.
		if !adding {
			w, err = s.createNamedRoot(p.Name, "")
			if err != nil {
				return err
			}
			p.RootID = w.ID
		}
		if len(p.Repos) > 0 {
			lock, e := s.rootLock(w)
			if e != nil {
				return e
			}
			p, err = s.beginSetup(p)
			lock.Close()
			if err != nil {
				return fmt.Errorf("root %s preserved, no checkout created: %w", w.Name, err)
			}
		}
	}
	if len(p.Repos) > 0 {
		prepare := true
		for {
			lock, e := s.rootLock(w)
			if e != nil {
				return e
			}
			p, err = s.loadSetup(p.ID)
			if err == nil && prepare {
				p, err = s.runSetup(p, func(r SetupRepo) {
					fmt.Fprintf(os.Stderr, "%s: %s %s\n", displayText(r.Alias), r.State, displayText(r.Problem))
				})
			}
			lock.Close()
			prepare = true
			if err != nil {
				return err
			}
			failed := -1
			var summary strings.Builder
			fmt.Fprintf(&summary, "Setup %s · resources are preserved\n", p.ID)
			for i, r := range p.Repos {
				fmt.Fprintf(&summary, "%s: %s · %s · %s\n", r.Alias, r.State, r.Destination, r.Problem)
				if r.Problem != "" && failed < 0 {
					failed = i
				}
			}
			fmt.Fprintln(os.Stderr, displayMultiline(summary.String()))
			if failed < 0 {
				skipped := false
				for _, r := range p.Repos {
					if r.State == "skipped" {
						skipped = true
					}
				}
				if skipped {
					choice, e := setupChoose(summary.String(), []string{"Proceed with successful subset", "Cancel; preserve resources"}, false)
					if e != nil {
						return setupMenuExit(e)
					}
					if choice[0] != 0 {
						return nil
					}
				}
				break
			}
			choices := []string{"Retry / reconcile verified recorded resources", "Explicitly accept cached base for failed repository", "Skip failed repository (only before creation)", "Proceed with successful subset", "Cancel; preserve all resources and leave new root offline"}
			selected, e := setupChoose(summary.String(), choices, false)
			if e != nil {
				return setupMenuExit(e)
			}
			if selected[0] == 4 {
				return nil
			}
			if selected[0] == 3 {
				break
			}
			if selected[0] == 0 {
				continue
			}
			action := "cached"
			if selected[0] == 2 {
				action = "skip"
			}
			// Reuse the same WT-owned action API as noninteractive recovery.
			if e = setupCommand(s, []string{action, p.ID, strconv.Itoa(failed)}); e != nil {
				fmt.Fprintln(os.Stderr, displayText(e.Error()))
				// A rejected recovery choice did not authorize another fetch.
				// Reload the journal and show choices without running setup.
				prepare = false
			}
		}
	}
	if !adding {
		lock, e := s.rootLock(w)
		if e != nil {
			return e
		}
		err = s.restore(w)
		lock.Close()
		if err != nil {
			return err
		}
		w, err = s.Worktree(w.ID)
		if err != nil {
			return err
		}
		if *focus {
			return openView(w, w.Views[0])
		}
	}
	fmt.Fprintf(os.Stderr, "Session %s ready; %d selected repositories. Existing conversations unchanged.\n", w.Name, len(p.Repos))
	return nil
}

// Only a real user abort is cancellation; dependency/terminal failures surface.
func setupMenuExit(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 130 {
		return nil
	}
	return err
}
