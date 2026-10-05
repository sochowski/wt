package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDurableBootstrapExactRestoreAndNativeCheckpointFence(t *testing.T) {
	source, err := filepath.Abs("../config")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(source, "..", "runtime", "pi-durable", "node_modules")); err != nil {
		t.Skip("run npm --prefix runtime/pi-durable ci --ignore-scripts first")
	}
	s := worktreeTestStore(t)
	t.Setenv("WT_SOURCE_CONFIG", source)
	t.Setenv("WT_STATUS_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	agentDir := filepath.Join(t.TempDir(), "pi")
	if err = os.MkdirAll(agentDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"anthropic","defaultModel":"claude-haiku-4-5-20251001"}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	w := testRoot(t, s, "durable")
	id := w.Agents[0].ID
	if err = s.initializeDurable(w, id); err != nil {
		t.Fatal(err)
	}
	w, _ = s.Worktree(w.ID)
	a, _ := w.agent(id)
	if a.NativeID != "" || a.Adapter.Backend != "durable" || !a.Adapter.Durable.Initialized || a.Stopped {
		t.Fatalf("wrong adapter %+v", a)
	}
	args, err := strictPiArgs(a)
	if err != nil {
		t.Fatal(err)
	}
	if args[0] != "node" || strings.Contains(strings.Join(args, " "), "--session-dir") {
		t.Fatal(args)
	}
	if err = s.initializeDurable(w, id); err == nil {
		t.Fatal("rebootstrap allowed")
	}
	s.db.Exec(`UPDATE agent_sessions SET runtime='live' WHERE id=?`, id)
	if err = s.updateAgent(w.ID, id, "live", "working", "", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.updateAgent(w.ID, id, "live", "idle", "native", &PiSnapshot{Version: 1}); err == nil {
		t.Fatal("native checkpoint changed backend")
	}
	a.Stopped = true // exact validation is independent of native ID on human resume.
	if _, err = strictPiArgs(a); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(a.Adapter.Durable.Store); err != nil {
		t.Fatal(err)
	}
	if _, err = strictPiArgs(a); err == nil {
		t.Fatal("missing durable store selected replacement")
	}
}

func TestDurableInboxReservationReconciliationDoesNotSpendBudgetTwice(t *testing.T) {
	s := worktreeTestStore(t)
	w := testRoot(t, s, "durable-inbox")
	id := w.Agents[0].ID
	s.db.Exec(`UPDATE agent_sessions SET adapter='{"version":1,"backend":"durable"}',runtime='one' WHERE id=?`, id)
	m, err := s.send(w, "operation", "human", id, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.claimDurableInbox(w.ID, id, "one", m.ID); err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE roots SET wake_enabled=0 WHERE id=?`, w.ID)
	s.db.Exec(`UPDATE agent_sessions SET runtime='two' WHERE id=?`, id)
	if err = s.recoverInbox(w.ID, id, "two"); err != nil {
		t.Fatal(err)
	}
	if err = s.claimDurableInbox(w.ID, id, "two", m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.claimDurableInbox(w.ID, id, "two", m.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Worktree(w.ID)
	if got.WakeBudget != 63 {
		t.Fatalf("budget spent twice: %d", got.WakeBudget)
	}
	if err = s.claimDurableInbox(w.ID, id, "one", m.ID); err == nil {
		t.Fatal("stale claim allowed")
	}
	if err = s.receipt(w.ID, id, "two", m.ID, "ack"); err != nil {
		t.Fatal(err)
	}
	if err = s.claimDurableInbox(w.ID, id, "two", m.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Worktree(w.ID)
	if got.WakeBudget != 63 {
		t.Fatal("delivered retry spent permit")
	}
	native := testRoot(t, s, "native-inbox")
	n := native.Agents[0].ID
	s.db.Exec(`UPDATE agent_sessions SET runtime='native' WHERE id=?`, n)
	if err = s.claimDurableInbox(native.ID, n, "native", "operation"); err == nil {
		t.Fatal("native accepted durable reconciliation")
	}
}

func TestCanonicalDurableStoreLockAcrossWTDatabases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.sqlite.wt-lock")
	lock, err := lockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if second, err := lockFile(path); err == nil {
		second.Close()
		t.Fatal("second owner admitted")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(filepath.Dir(path), alias); err != nil {
		t.Fatal(err)
	}
	if second, err := lockFile(filepath.Join(alias, filepath.Base(path))); err == nil {
		second.Close()
		t.Fatal("alias owner admitted")
	}
}

func TestDurableHumanSelfStopExactCapabilityAndNoPeerCascade(t *testing.T) {
	s, w, _ := durableJobFixture(t)
	a := w.Agents[0]
	key := newID() + newID()
	if _, err := s.db.Exec(`INSERT INTO durable_controls VALUES(?,?,?)`, a.ID, a.Runtime, delegationDigest([]byte(key))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_AGENT_ID", a.ID)
	t.Setenv("WT_ROOT_ID", w.ID)
	t.Setenv("WT_RUNTIME_ID", a.Runtime)
	request := map[string]string{"root": w.ID, "agent": a.ID, "runtime": a.Runtime, "capability": key}
	for _, field := range []string{"root", "agent", "runtime", "capability"} {
		invalid := map[string]string{}
		for k, v := range request {
			invalid[k] = v
		}
		invalid[field] = "wrong"
		if err := s.durableHumanStop(strings.NewReader(jsonText(invalid))); err == nil {
			t.Fatalf("invalid %s admitted", field)
		}
	}
	// Human/pin state must not be downgraded by the control path.
	s.db.Exec(`UPDATE views SET pinned=1,manager='human' WHERE root_id=?`, w.ID)
	peer, err := s.addAgent(w, "independent", a.ID, "", w.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE agent_sessions SET runtime='peer-runtime' WHERE id=?`, peer)
	m, err := s.send(w, "stop-uncertain", a.ID, a.ID, "pending")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.claimDurableInbox(w.ID, a.ID, a.Runtime, m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.durableHumanStop(strings.NewReader(jsonText(request))); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Worktree(w.ID)
	stopped, _ := got.agent(a.ID)
	other, _ := got.agent(peer)
	if !stopped.Stopped || stopped.Runtime != "" || other.Runtime != "peer-runtime" || other.Stopped || !got.Views[0].Pinned || got.Views[0].Manager != "human" {
		t.Fatal("stop/protection/peer state changed incorrectly")
	}
	var state string
	s.db.QueryRow(`SELECT state FROM inbox WHERE id=?`, m.ID).Scan(&state)
	if state != "uncertain" {
		t.Fatal(state)
	}
	if err = s.durableHumanStop(strings.NewReader(jsonText(request))); err == nil {
		t.Fatal("reused stopped capability")
	}
}

func durableDefaultSettings(t *testing.T) {
	t.Helper()
	source, err := filepath.Abs("../config")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_SOURCE_CONFIG", source)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WT_STATUS_DIR", t.TempDir())
	t.Setenv("WT_BASE_DIR", t.TempDir())
	t.Setenv("WT_AGENT_ID", "")
	t.Setenv("WT_ROOT_ID", "")
	t.Setenv("WT_RUNTIME_ID", "")
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	if err = os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"openai-codex","defaultModel":"gpt-6.1-sol"}`), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestModernNewDurableSourceDefaultAndExplicitNativeNoMigration(t *testing.T) {
	s := worktreeTestStore(t)
	durableDefaultSettings(t)
	if err := worktreeCommand(s, []string{"new", "default-durable", "--cwd", t.TempDir(), "--offline"}); err != nil {
		t.Fatal(err)
	}
	w, _ := s.Worktree("default-durable")
	a := w.Agents[0]
	if a.Adapter.Backend != "durable" || !a.Adapter.Durable.Initialized || a.NativeID != "" {
		t.Fatalf("default adapter %+v", a)
	}
	if defaultPiBackend(w) != "durable" {
		t.Fatal("modern peer default")
	}
	if err := worktreeCommand(s, []string{"new", "explicit-native", "--cwd", t.TempDir(), "--offline", "--backend", "native"}); err != nil {
		t.Fatal(err)
	}
	native, _ := s.Worktree("explicit-native")
	before := jsonText(native.Agents[0].Adapter)
	// Neither read/restore args nor a newer source default migrate stored adapters.
	if _, err := strictPiArgs(native.Agents[0]); err != nil {
		t.Fatal(err)
	}
	native, _ = s.Worktree(native.ID)
	if jsonText(native.Agents[0].Adapter) != before || native.Agents[0].Adapter.Durable != nil {
		t.Fatal("native migrated")
	}
	legacy := testRoot(t, s, "legacy-non-pi")
	if defaultPiBackend(legacy) != "native" {
		t.Fatal("legacy default widened")
	}
}
func TestModernNewMissingBootstrapAndPrimaryConfigRemainStoppedWithoutFallback(t *testing.T) {
	for _, failure := range []string{"entrypoint", "settings"} {
		t.Run(failure, func(t *testing.T) {
			s := worktreeTestStore(t)
			durableDefaultSettings(t)
			if failure == "entrypoint" {
				t.Setenv("WT_SOURCE_CONFIG", t.TempDir())
			} else {
				t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
			}
			if err := worktreeCommand(s, []string{"new", "incomplete", "--cwd", t.TempDir(), "--offline"}); err == nil {
				t.Fatal("bootstrap failure accepted")
			}
			w, err := s.Worktree("incomplete")
			if err != nil {
				t.Fatal(err)
			}
			a := w.Agents[0]
			if !a.Stopped || a.Runtime != "" || a.NativeID != "" || a.Adapter.Backend != "durable" || a.Adapter.Durable.Initialized {
				t.Fatal("bootstrap fallback")
			}
			if _, err := strictPiArgs(a); err == nil {
				t.Fatal("incomplete launch accepted")
			}
		})
	}
}
func TestFreshMenuDurableDefaultExplicitNativeAndExistingSetupResumeUnchanged(t *testing.T) {
	s := worktreeTestStore(t)
	durableDefaultSettings(t)
	bin := t.TempDir()
	fzf := `#!/bin/sh
for arg in "$@"; do
 case "$arg" in --print-query) printf '%s\n' "$WT_MENU_FIXTURE_NAME"; exit 0;; --header=Repositories*) printf 'none:0\n'; exit 0;; esac
done
IFS= read -r line
printf '%s\n' "$line"
`
	if err := os.WriteFile(filepath.Join(bin, "fzf"), []byte(fzf), 0700); err != nil {
		t.Fatal(err)
	}
	// Deliberately unavailable private projection, never a real server/native CLI.
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, backend := range []string{"durable", "native"} {
		name := "menu-" + backend
		t.Setenv("WT_MENU_FIXTURE_NAME", name)
		args := []string{"--offline"}
		if backend == "native" {
			args = append(args, "--backend", "native")
		}
		if err := setupMenu(s, args); err == nil {
			t.Fatal("unavailable tmux projection accepted")
		}
		w, err := s.Worktree(name)
		if err != nil {
			t.Fatal(err)
		}
		a := w.Agents[0]
		if backend == "durable" {
			if a.Adapter.Backend != "durable" || !a.Adapter.Durable.Initialized {
				t.Fatal("menu default not durable")
			}
		} else if a.Adapter.Durable != nil {
			t.Fatal("explicit native migrated")
		}
		before := jsonText(a)
		if err = setupMenu(s, []string{"--root", w.ID, "--offline"}); err != nil {
			t.Fatal(err)
		}
		p, err := s.beginSetup(SetupPlan{Name: w.Name, RootID: w.ID, Repos: []SetupRepo{}})
		if err != nil {
			t.Fatal(err)
		}
		if err = setupMenu(s, []string{"--resume", p.ID}); err != nil {
			t.Fatal(err)
		}
		unchanged, _ := s.Worktree(w.ID)
		if jsonText(unchanged.Agents[0]) != before {
			t.Fatal("adding/resume relaunched or migrated existing conversation")
		}
		if err = setupMenu(s, []string{"--root", w.ID, "--backend", "durable"}); err == nil {
			t.Fatal("existing backend override admitted")
		}
	}
	t.Setenv("WT_MENU_FIXTURE_NAME", "menu-incomplete")
	t.Setenv("WT_SOURCE_CONFIG", t.TempDir())
	if err := setupMenu(s, []string{"--offline"}); err == nil {
		t.Fatal("missing menu bootstrap accepted")
	}
	w, err := s.Worktree("menu-incomplete")
	if err != nil {
		t.Fatal(err)
	}
	if !w.Agents[0].Stopped || w.Agents[0].Adapter.Backend != "durable" || w.Agents[0].Adapter.Durable.Initialized {
		t.Fatal("menu silently fell back")
	}
}

func TestMenuBootstrapFailurePreservesSetupJournalAndResumeNeverMigratesOrRelaunches(t *testing.T) {
	s := worktreeTestStore(t)
	source, _ := setupFixture(t, "main")
	durableDefaultSettings(t)
	t.Setenv("WT_REPO_DIRS", filepath.Dir(source))
	t.Setenv("WT_SOURCE_CONFIG", t.TempDir())
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	picker := `#!/bin/sh
for arg in "$@"; do
 case "$arg" in --print-query) printf 'partial-durable\n';exit 0;; --header=Repositories*) printf 'review:1\n';head -n 1;exit 0;; esac
done
head -n 1
`
	os.WriteFile(filepath.Join(bin, "fzf"), []byte(picker), 0700)
	os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 1\n"), 0700)
	if err := setupMenu(s, []string{"--offline"}); err == nil || !strings.Contains(err.Error(), "incomplete durable bootstrap") {
		t.Fatalf("unexpected bootstrap outcome: %v", err)
	}
	w, err := s.Worktree("partial-durable")
	if err != nil {
		t.Fatal(err)
	}
	before := jsonText(w.Agents[0])
	if !w.Agents[0].Stopped || w.Agents[0].Adapter.Backend != "durable" || len(w.Checkouts) != 0 {
		t.Fatal("failure created Git checkout or native replacement")
	}
	var raw string
	if err = s.db.QueryRow(`SELECT plan FROM checkout_setups WHERE root_id=?`, w.ID).Scan(&raw); err != nil {
		t.Fatal("setup intent lost", err)
	}
	var plan SetupPlan
	if err = json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Repos) != 1 || plan.Repos[0].State != "not_attempted" {
		t.Fatal("journal progressed before bootstrap", plan)
	}
	if err = setupMenu(s, []string{"--resume", plan.ID}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Worktree(w.ID)
	if jsonText(after.Agents[0]) != before || len(after.Checkouts) != 1 {
		t.Fatal("resume migrated/relaunched stopped conversation or lost prepared checkout")
	}
}
