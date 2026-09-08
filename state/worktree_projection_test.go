package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootProjectionSharedSearchAllRepositoriesPeersTasksAndLabels(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	s := worktreeTestStore(t)
	w := testRoot(t, s, "billing-migration")
	for i := 0; i < 5; i++ {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("repository-%d : ' special", i))
		_, err := s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?)`, newID(), w.ID, fmt.Sprintf("alias-%d", i), path)
		if err != nil {
			t.Fatal(err)
		}
	}
	id, err := s.addAgent(w, "review-api", "", "", w.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	if _, err = s.send(w, newID(), "human", id, "Investigate quasar-unique task: preserve\tcontrols\n\x1b[31m"); err != nil {
		t.Fatal(err)
	}
	if err = projectionCommand(s, []string{"label", w.ID, "Billing migration review"}); err != nil {
		t.Fatal(err)
	}
	p, err := s.rootProjection(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projectionRow(p), "+2 repos") || strings.Contains(projectionRow(p), "alias-4") {
		t.Fatal("not visually abbreviated")
	}
	for _, query := range []string{"billing migration review", "alias-4", "repository-4 : ' special", "review-api", "quasar-unique"} {
		rows, e := s.rootProjections(query)
		if e != nil || len(rows) != 1 || rows[0].ID != w.ID {
			t.Fatalf("search %q: %+v %v", query, rows, e)
		}
	}
	after, _ := s.Worktree(w.ID)
	if after.Name != w.Name || after.ID != w.ID || after.Agents[0].ID != w.Agents[0].ID {
		t.Fatal("label renamed identity")
	}
	if p.AgentCount != 2 {
		t.Fatal(p.AgentCount)
	}
	encoded := displayText(p.SearchText)
	if strings.ContainsAny(encoded, "\t\n\x1b") {
		t.Fatal("unsafe transport")
	}
}
func TestReadableScratchNamesAndBlankCancellation(t *testing.T) {
	s := worktreeTestStore(t)
	// Offline creation should not contact any real tmux server.
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	first, err := s.createNamedRoot("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.createNamedRoot("", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "scratch" || second.Name != "scratch-2" {
		t.Fatalf("%s %s", first.Name, second.Name)
	}
	if err = worktreeCommand(s, []string{"new", "", "--offline"}); err == nil {
		t.Fatal("blank cancellation created a session")
	}
	if _, err = s.createNamedRoot(first.Name, t.TempDir()); err == nil {
		t.Fatal("collision reused root")
	}
}

func TestAmbiguousRootReferencesFailClosedAtActionBoundary(t *testing.T) {
	s := worktreeTestStore(t)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	first := testRoot(t, s, "ordinary-name")
	if _, err := s.createNamedRoot(first.ID, t.TempDir()); err == nil || !strings.Contains(err.Error(), "root ID") {
		t.Fatal("new name collided with opaque ID")
	}
	// Model a preexisting collision through the legacy name-keyed API. Never
	// migrate/rename it; every shared resolver/action must fail closed instead.
	if _, err := s.Set(first.ID, map[string]string{"agent": "pi", "workspace_path": t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	var secondID string
	if err := s.db.QueryRow(`SELECT id FROM roots WHERE name=?`, first.ID).Scan(&secondID); err != nil {
		t.Fatal(err)
	}
	second, err := s.Worktree(secondID)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"projection", "show", first.ID}, {"projection", "preview", first.ID}, {"projection", "label", first.ID, "wrong label"}, {"restore", first.ID}, {"forget", first.ID, ""}, {"agents", "open", first.ID, "main"}} {
		if err := worktreeCommand(s, args); err == nil || !strings.Contains(err.Error(), "ambiguous root reference") {
			t.Fatalf("unsafe ambiguous action %v: %v", args, err)
		}
	}
	for _, pair := range [][2]string{{first.Name, first.ID}, {second.ID, second.ID}} {
		w, err := s.Worktree(pair[0])
		if err != nil || w.ID != pair[1] {
			t.Fatalf("ordinary name/ID lookup regressed: %v %v", pair, err)
		}
	}
	for _, name := range []string{first.Name, second.Name} {
		if _, exists, err := s.Get(name); err != nil || !exists {
			t.Fatal("wrong root removed")
		}
	}
	var labels int
	if err := s.db.QueryRow(`SELECT count(*) FROM root_labels`).Scan(&labels); err != nil || labels != 0 {
		t.Fatal("wrong root relabeled")
	}
}

func TestSessionNameRowsExcludeRichMetadataAndOpaqueIdentity(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "billing-migration")
	s.Set(w.Name, map[string]string{"message": "unrelated-quasar", "status": "working"})
	s.db.Exec(`INSERT INTO checkouts(id,root_id,alias,path) VALUES(?,?,?,?)`, newID(), w.ID, "secret-repository", "/missing/repository")
	if err := projectionCommand(s, []string{"label", w.ID, "Billing review"}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"secret-repository", "working", "unrelated-quasar", w.ID, "missing"} {
		rows, err := s.sessionNameRows(query, "all")
		if err != nil || len(rows) != 0 {
			t.Fatalf("noise query %q: %v %v", query, rows, err)
		}
	}
	for _, query := range []string{"billing-migration", "Billing review"} {
		rows, err := s.sessionNameRows(query, "all")
		if err != nil || len(rows) != 1 || rows[0] != w.ID+"\tBilling review (billing-migration)" {
			t.Fatal(rows, err)
		}
	}
}
