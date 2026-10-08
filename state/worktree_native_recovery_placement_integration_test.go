package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A dedicated private server tests real observation semantics. No default
// socket, user configuration, live WT root or real agent is accessed.
func TestColdNativePlacementActualPrivateTmuxObservations(t *testing.T) {
	if os.Getenv("WT_NATIVE_TMUX_TEST") != "1" {
		t.Skip("explicit private tmux probe required")
	}
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	private, err := os.MkdirTemp("/private/tmp", "wt-cold-pane-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(private, "sock")
	t.Cleanup(func() { exec.Command(binary, "-S", socket, "kill-server").Run(); os.RemoveAll(private) })
	home := filepath.Join(private, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	probe := func(args ...string) (string, error) {
		cmd := exec.Command(binary, append([]string{"-S", socket, "-f", "/dev/null"}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "TMUX=", "TMUX_PANE=")
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	pane, err := probe("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", "private-cold-placement", "sleep 120")
	if err != nil {
		t.Fatal(err, pane)
	}
	if _, err = probe("set-option", "-p", "-t", pane, "remain-on-exit", "on"); err != nil {
		t.Fatal(err)
	}
	if _, err = probe("set-option", "-p", "-t", pane, "@wt-view", "original-private-view"); err != nil {
		t.Fatal(err)
	}
	job := DelegationJob{ChildID: "private-child", ParentID: "private-parent"}
	view := View{ID: "original-private-view", Kind: "agent", Target: job.ChildID, Manager: job.ParentID}
	if err = validateColdNativePlacement(job, view, pane, probe); err == nil {
		t.Fatal("actual live private process was replaceable")
	}
	// This is the test's own private process, not an operator repair shell.
	if _, err = probe("respawn-pane", "-k", "-t", pane, "/usr/bin/true"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		dead, err := probe("display-message", "-p", "-t", pane, "#{pane_dead}")
		if err == nil && dead == "1" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err = validateColdNativePlacement(job, view, pane, probe); err != nil {
		t.Fatal("actual dead unfocused private pane refused", err)
	}
	observed, err := probe("display-message", "-p", "-t", pane, "#{pane_id} #{@wt-view}")
	if err != nil || observed != pane+" "+view.ID {
		t.Fatal("observation changed original pane/view", err, observed)
	}
	t.Logf("private socket observation preserves %s / %s", pane, view.ID)
}
