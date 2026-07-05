// launch-plan: compute how to start an agent, emitting either shell to eval
// (--sh, used by wt-agent-launch) or JSON (--json, for inspection/tests). Any
// filesystem reconciliation an agent needs before launch (Claude's Neovim IDE
// lock files) happens here as a side effect, so the bash launcher stays a thin
// exec wrapper.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// LaunchPlan is the resolved recipe for starting an agent.
type LaunchPlan struct {
	Binary string            `json:"binary"`
	Args   []string          `json:"args"`
	Env    map[string]string `json:"env"`
}

// cmdAgentLaunchPlan: wt-state agent <name> launch-plan [flags]
func cmdAgentLaunchPlan(a Agent, args []string) {
	fs := flag.NewFlagSet("agent launch-plan", flag.ExitOnError)
	session := fs.String("session", "", "tmux session name (for config fallbacks)")
	opencodeConfig := fs.String("opencode-config", "", "resolved OPENCODE_CONFIG candidate")
	configDir := fs.String("config-dir", os.Getenv("WT_CONFIG_DIR"), "wt config dir")
	home := fs.String("home", "", "target home dir (default $HOME)")
	asSh := fs.Bool("sh", false, "emit shell to eval")
	_ = fs.Bool("json", false, "emit JSON (default)")
	fs.Parse(args)

	plan := LaunchPlan{Binary: a.Binary, Args: []string{}, Env: map[string]string{}}

	switch a.Launch {
	case launchClaudeIDE:
		if ideLockCount(homeDir(*home)) > 0 {
			plan.Args = append(plan.Args, "--ide")
		}
	case launchOpencode:
		if cfg := resolveOpencodeConfig(*opencodeConfig, *configDir, *session); cfg != "" {
			plan.Env["OPENCODE_CONFIG"] = cfg
		}
	}

	if *asSh {
		emitLaunchSh(plan)
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(plan)
}

// resolveOpencodeConfig mirrors wt-agent-launch's precedence: an already-set
// OPENCODE_CONFIG wins; then the caller-supplied candidate (tmux/DB value);
// then the per-session default file if it exists.
func resolveOpencodeConfig(candidate, configDir, session string) string {
	if v := os.Getenv("OPENCODE_CONFIG"); v != "" {
		return "" // already set in the environment; don't override
	}
	if candidate != "" {
		return candidate
	}
	if configDir != "" && session != "" {
		def := filepath.Join(configDir, "opencode-mcp", session+".json")
		if _, err := os.Stat(def); err == nil {
			return def
		}
	}
	return ""
}

var lockPID = regexp.MustCompile(`"pid":\s*(\d+)`)

// ideLockCount reconciles Claude's Neovim IDE lock files under
// ~/.claude/ide and returns how many live locks remain. It drops locks whose
// process is dead and restores any locks orphaned by a prior hide/restore, so
// `claude` only gets --ide when a real Neovim is listening.
func ideLockCount(home string) int {
	ideDir := filepath.Join(home, ".claude", "ide")
	entries, err := os.ReadDir(ideDir)
	if err != nil {
		return 0
	}

	// Restore locks orphaned under .hidden by an older hide/restore mechanism.
	hidden := filepath.Join(ideDir, ".hidden")
	if hs, err := os.ReadDir(hidden); err == nil {
		for _, e := range hs {
			if strings.HasSuffix(e.Name(), ".lock") {
				_ = os.Rename(filepath.Join(hidden, e.Name()), filepath.Join(ideDir, e.Name()))
			}
		}
		_ = os.Remove(hidden)
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}
		path := filepath.Join(ideDir, e.Name())
		if pid := lockFilePID(path); pid > 0 && !processAlive(pid) {
			_ = os.Remove(path)
			continue
		}
		count++
	}
	return count
}

func lockFilePID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	m := lockPID.FindSubmatch(b)
	if m == nil {
		return -1 // unreadable pid: treat as live, matching the old grep-based guard
	}
	pid, _ := strconv.Atoi(string(m[1]))
	return pid
}

func processAlive(pid int) bool {
	// signal 0 probes existence without delivering anything, like `kill -0`.
	return syscall.Kill(pid, 0) == nil
}

// emitLaunchSh prints the plan as shell for the launcher to eval: a binary, a
// bash array of args, and export lines. All values are single-quote escaped.
func emitLaunchSh(p LaunchPlan) {
	fmt.Printf("WT_LAUNCH_BINARY=%s\n", shQuote(p.Binary))
	quoted := make([]string, len(p.Args))
	for i, a := range p.Args {
		quoted[i] = shQuote(a)
	}
	fmt.Printf("WT_LAUNCH_ARGS=(%s)\n", strings.Join(quoted, " "))
	for k, v := range p.Env {
		fmt.Printf("export %s=%s\n", k, shQuote(v))
	}
}

// shQuote wraps s in single quotes, escaping embedded single quotes, so it is
// safe to eval in bash.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
