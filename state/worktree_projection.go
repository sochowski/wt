package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type RepositoryProjection struct {
	Alias   string `json:"alias"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Source  string `json:"source,omitempty"`
	BaseRef string `json:"base_ref,omitempty"`
	BaseSHA string `json:"base_sha,omitempty"`
}
type RootProjection struct {
	Worktree
	Label        string                 `json:"label"`
	Repositories []RepositoryProjection `json:"repositories"`
	AgentCount   int                    `json:"agent_count"`
	Status       string                 `json:"status"`
	PRState      string                 `json:"pr_state"`
	Running      bool                   `json:"running"`
	Summary      string                 `json:"summary"`
	SearchText   string                 `json:"search_text"`
}

// Display encoding is separate from identity/JSON. Neither terminal controls nor
// tabs/newlines in paths or peer messages can become fzf records or escape codes.
func displayText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) || r == 0x2028 || r == 0x2029 {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func displayMultiline(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = displayText(lines[i])
	}
	return strings.Join(lines, "\n")
}
func boundedText(text string, n int) string {
	r := []rune(text)
	if len(r) > n {
		return string(r[:n])
	}
	return text
}
func (s *Store) rootProjection(name string) (RootProjection, error) {
	p := RootProjection{Repositories: []RepositoryProjection{}}
	w, err := s.Worktree(name)
	if err != nil {
		return p, err
	}
	p.Worktree = w
	p.Label = w.Name
	var label string
	err = s.db.QueryRow(`SELECT label FROM root_labels WHERE root_id=?`, w.ID).Scan(&label)
	if err == nil {
		p.Label = label
	} else if err != sql.ErrNoRows {
		return p, err
	}
	legacy, _, err := s.Get(w.Name)
	if err != nil {
		return p, err
	}
	p.Status = legacy.Status
	p.PRState = legacy.PRState
	p.Summary = boundedText(legacy.Message, 512)
	search := []string{w.ID, w.Name, p.Label, w.Cwd, legacy.Repo, legacy.Branch, legacy.WtPath, p.Status, p.Summary}
	for _, c := range w.Checkouts {
		r := RepositoryProjection{Alias: c.Alias, Name: filepath.Base(filepath.Dir(c.Path)), Path: c.Path}
		// Borrowed attachments retain their actual current branch and ordinary repo
		// basename. Missing paths still contribute all durable metadata to search.
		if _, err := os.Stat(c.Path); err == nil {
			r.Name = filepath.Base(c.Path)
			r.Branch, _ = setupGit(c.Path, "symbolic-ref", "--quiet", "--short", "HEAD")
		}
		rows, e := s.db.Query(`SELECT plan FROM checkout_setups WHERE root_id=? ORDER BY rowid`, w.ID)
		if e != nil {
			return p, e
		}
		for rows.Next() {
			var text string
			if e = rows.Scan(&text); e != nil {
				rows.Close()
				return p, e
			}
			var plan SetupPlan
			if e = json.Unmarshal([]byte(text), &plan); e != nil {
				rows.Close()
				return p, e
			}
			for _, repo := range plan.Repos {
				if repo.CheckoutID == c.ID {
					r.Name = filepath.Base(repo.Source)
					r.Source = repo.Source
					r.BaseRef = repo.SelectedRef
					r.BaseSHA = repo.SHA
					if r.Branch == "" {
						r.Branch = plan.Name
					}
				}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return p, e
		}
		p.Repositories = append(p.Repositories, r)
		search = append(search, r.Alias, r.Name, r.Path, r.Branch, r.Source, r.BaseRef)
	}
	for _, a := range w.Agents {
		if !a.Stopped {
			p.AgentCount++
			if a.Status == "working" || a.Status == "waiting" && p.Status != "working" {
				p.Status = a.Status
			}
		}
		search = append(search, a.Name, a.Profile, a.Cwd, a.Status)
	}
	rows, err := s.db.Query(`SELECT substr(body,1,512) FROM inbox WHERE root_id=? AND request=1 ORDER BY rowid DESC LIMIT 32`, w.ID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			rows.Close()
			return p, err
		}
		search = append(search, text)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	p.SearchText = strings.Join(search, " ")
	return p, nil
}
func projectionMatches(p RootProjection, query string) bool {
	haystack := strings.ToLower(p.SearchText)
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}
func projectionLiveRoots() map[string]bool {
	live := map[string]bool{}
	out, err := tmux("list-sessions", "-F", "#{session_name}")
	if err == nil {
		for _, name := range strings.Split(out, "\n") {
			live[name] = true
		}
	}
	return live
}
func projectionRuntime(p *RootProjection, live map[string]bool) {
	p.Running = live[p.Name]
	if !p.Running {
		p.Status = "offline"
	}
	p.SearchText += " " + p.Status
}
func (s *Store) rootProjections(query string) ([]RootProjection, error) {
	sessions, err := s.List("all", "updated")
	if err != nil {
		return nil, err
	}
	out := []RootProjection{}
	live := projectionLiveRoots()
	for _, session := range sessions {
		p, e := s.rootProjection(session.Name)
		if e != nil {
			return nil, e
		}
		projectionRuntime(&p, live)
		if projectionMatches(p, query) {
			out = append(out, p)
		}
	}
	return out, nil
}
func projectionRow(p RootProjection) string {
	repos := []string{}
	for _, r := range p.Repositories {
		repos = append(repos, r.Alias+"@"+r.Branch)
	}
	if len(repos) > 3 {
		repos = append(repos[:3], fmt.Sprintf("+%d repos", len(repos)-3))
	}
	if len(repos) == 0 {
		repos = []string{"no repo"}
	}
	badge := map[string]string{"merged": "⬤", "open": "◆", "draft": "◇", "closed": "⊘"}[p.PRState]
	status := p.Status
	if badge != "" {
		status += " " + badge
	}
	return displayText(fmt.Sprintf("%s | %s | %d agents | %s | %s", p.Label, strings.Join(repos, ", "), p.AgentCount, status, boundedText(p.Summary, 120)))
}

// Session switching intentionally avoids the rich projection: no checkout,
// agent, inbox, Git or per-key runtime inspection. IDs are transport only.
func (s *Store) sessionNameRows(query, scope string) ([]string, error) {
	rows, err := s.db.Query(`SELECT r.id,r.name,coalesce(l.label,r.name) FROM roots r JOIN sessions s ON s.name=r.name LEFT JOIN root_labels l ON l.root_id=r.id ORDER BY s.updated_at DESC,r.name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var live map[string]bool
	if scope == "live" {
		live = projectionLiveRoots()
	}
	out := []string{}
	for rows.Next() {
		var id, name, label string
		if err = rows.Scan(&id, &name, &label); err != nil {
			return nil, err
		}
		if live != nil && !live[name] {
			continue
		}
		text := label
		if name != label {
			text += " (" + name + ")"
		}
		if !projectionMatches(RootProjection{SearchText: text}, query) {
			continue
		}
		out = append(out, id+"\t"+displayText(text))
	}
	return out, rows.Err()
}
func projectionCommand(s *Store, args []string) error {
	if len(args) == 0 {
		return errors.New("projection rows|list|show|preview|label")
	}
	op := args[0]
	args = args[1:]
	if op == "names" {
		query, scope := "", "all"
		if len(args) > 0 {
			query = args[0]
		}
		if len(args) > 1 {
			scope = args[1]
		}
		rows, err := s.sessionNameRows(query, scope)
		if err != nil {
			return err
		}
		for _, row := range rows {
			fmt.Println(row)
		}
		return nil
	}
	if op == "label" {
		if os.Getenv("WT_AGENT_ID") != "" || len(args) != 2 {
			return errors.New("human-only: wt label ROOT DISPLAY_LABEL")
		}
		label := strings.TrimSpace(args[1])
		if label == "" || len([]rune(label)) > 120 || displayText(label) != label {
			return errors.New("label must be 1..120 printable characters")
		}
		w, err := s.Worktree(args[0])
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`INSERT INTO root_labels(root_id,label) VALUES(?,?) ON CONFLICT(root_id) DO UPDATE SET label=excluded.label`, w.ID, label)
		return err
	}
	if op == "show" || op == "preview" {
		if len(args) != 1 {
			return errors.New("root ID required")
		}
		p, err := s.rootProjection(args[0])
		if err != nil {
			return err
		}
		projectionRuntime(&p, projectionLiveRoots())
		if op == "show" {
			printJSON(p)
			return nil
		}
		fmt.Println(projectionRow(p))
		fmt.Printf("Identity: %s (%s)\nWorking directory: %s\n", p.ID, displayText(p.Name), displayText(p.Cwd))
		for _, r := range p.Repositories {
			fmt.Printf("\n%s · %s · branch %s\n  %s\n  source %s\n  initial base %s %s\n", displayText(r.Alias), displayText(r.Name), displayText(r.Branch), displayText(r.Path), displayText(r.Source), displayText(r.BaseRef), r.BaseSHA)
		}
		for _, a := range p.Agents {
			fmt.Printf("\nPeer %s · %s · %s · %s\n", displayText(a.Name), a.Profile, a.Status, displayText(a.Cwd))
		}
		fmt.Printf("\nSearch detail (bounded task summaries):\n%s\n", displayText(p.SearchText))
		return nil
	}
	query := ""
	if len(args) > 0 {
		query = args[0]
	}
	projections, err := s.rootProjections(query)
	if err != nil {
		return err
	}
	switch op {
	case "rows":
		for _, p := range projections {
			if len(args) > 1 && args[1] == "live" && !p.Running {
				continue
			}
			fmt.Printf("%s\t%s\n", p.ID, projectionRow(p))
		}
	case "list":
		for _, p := range projections {
			fmt.Println(projectionRow(p))
		}
	default:
		return errors.New("unknown projection operation")
	}
	return nil
}
