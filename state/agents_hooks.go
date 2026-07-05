// install-hooks: wire wt-hook into each installed agent's own config format.
// Replaces the hand-coded per-agent blocks that used to live in install.sh
// (jq merges for Claude/Gemini, a TOML append for Codex, a symlink for
// opencode). Templates stay on disk under --template-dir so the opencode plugin
// can remain a live symlink into the checkout.
//
// Installs are surgical and versioned: wt owns its own hook entries (identified
// by hookMarker) and rewrites them from the current template every run, so a
// format change never leaves stale or duplicate entries behind. A small state
// file records the installed version per agent so we can report and detect
// out-of-date hooks (`agents hooks-status`).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TOML managed-block delimiters. Everything between them is owned by wt and
// replaced wholesale on install (so old formats are cleaned up).
const (
	tomlBlockBegin = "# >>> wt hooks"
	tomlBlockEnd   = "# <<< wt hooks"
)

// hookState maps agent name -> installed hook version. Persisted in the state
// dir so it never pollutes the agents' own config files.
type hookState map[string]int

func hookStatePath(stateDirOverride string) string {
	dir := stateDirOverride
	if dir == "" {
		dir = stateDir()
	}
	return filepath.Join(dir, "agent-hooks.json")
}

func readHookState(stateDirOverride string) hookState {
	st := hookState{}
	b, err := os.ReadFile(hookStatePath(stateDirOverride))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	return st
}

func writeHookState(stateDirOverride string, st hookState) error {
	path := hookStatePath(stateDirOverride)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// cmdAgentsInstallHooks: wt-state agents install-hooks --template-dir DIR [--home DIR] [--state-dir DIR]
func cmdAgentsInstallHooks(args []string) {
	fs := flag.NewFlagSet("agents install-hooks", flag.ExitOnError)
	templateDir := fs.String("template-dir", "", "directory holding the hook templates (config/)")
	home := fs.String("home", "", "target home dir (default $HOME)")
	stateDirFlag := fs.String("state-dir", "", "dir for the hook-version state file (default WT_STATUS_DIR)")
	fs.Parse(args)

	if *templateDir == "" {
		fatalf("install-hooks: --template-dir is required")
	}
	h := homeDir(*home)
	state := readHookState(*stateDirFlag)

	for _, a := range registry {
		if !a.isInstalled() {
			fmt.Printf("  %s: not installed, skipping hooks\n", a.Name)
			continue
		}
		target, err := installAgentHooks(a, *templateDir, h)
		if err != nil {
			// Non-fatal: one agent's failure shouldn't abort the others.
			fmt.Fprintf(os.Stderr, "  %s: hook install failed: %v\n", a.Name, err)
			continue
		}
		fmt.Printf("  %s: %s → %s\n", a.Name, versionVerb(state[a.Name], a.Hook.Version), target)
		state[a.Name] = a.Hook.Version
	}

	if err := writeHookState(*stateDirFlag, state); err != nil {
		fmt.Fprintf(os.Stderr, "  warning: could not record hook versions: %v\n", err)
	}
}

// versionVerb describes an install relative to what was there before.
func versionVerb(prev, cur int) string {
	switch {
	case prev == 0:
		return fmt.Sprintf("installed hooks (v%d)", cur)
	case prev == cur:
		return fmt.Sprintf("hooks up to date (v%d)", cur)
	case prev < cur:
		return fmt.Sprintf("updated hooks (v%d → v%d)", prev, cur)
	default:
		return fmt.Sprintf("hooks reinstalled (v%d)", cur)
	}
}

// installAgentHooks applies one agent's HookSpec, returning the target path it
// wrote/linked.
func installAgentHooks(a Agent, templateDir, home string) (string, error) {
	spec := a.Hook
	if spec.Template == "" {
		return "(no hooks)", nil
	}
	src := filepath.Join(templateDir, spec.Template)
	target := filepath.Join(home, spec.Target)

	switch spec.Format {
	case hookJSONMerge:
		return target, installJSONManaged(src, target)
	case hookTOMLAppend:
		return target, installTOMLManaged(src, target, spec.Version)
	case hookSymlinkPlugin:
		return target, installSymlinkPlugin(src, target)
	default:
		return "", fmt.Errorf("unknown hook format %q", spec.Format)
	}
}

// installJSONManaged rewrites wt's hook entries in a JSON settings file: it
// strips every wt-owned entry from every event (cleaning up stale/renamed
// events and duplicates), then appends the template's entries. Non-wt hooks and
// all other settings are preserved untouched.
func installJSONManaged(src, target string) error {
	tmplAny, err := readJSON(src)
	if err != nil {
		return fmt.Errorf("read template: %w", err)
	}
	tmpl, ok := tmplAny.(map[string]any)
	if !ok {
		return fmt.Errorf("template %s is not a JSON object", src)
	}
	tmplHooks, _ := tmpl["hooks"].(map[string]any)

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	root := map[string]any{}
	if b, err := os.ReadFile(target); err == nil {
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			return fmt.Errorf("parse %s: %w", target, err)
		}
		if root, ok = v.(map[string]any); !ok {
			return fmt.Errorf("%s must be a JSON object", target)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	hooks, ok := root["hooks"].(map[string]any)
	if root["hooks"] != nil && !ok {
		return fmt.Errorf("%s: \"hooks\" must be a JSON object", target)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}

	// 1. Drop every wt-owned entry from every event; delete emptied events.
	for event, v := range hooks {
		arr, ok := v.([]any)
		if !ok {
			continue
		}
		kept := arr[:0:0]
		for _, entry := range arr {
			if !entryIsWtOwned(entry) {
				kept = append(kept, entry)
			}
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}

	// 2. Append the template's current entries.
	for event, v := range tmplHooks {
		tarr, ok := v.([]any)
		if !ok {
			continue
		}
		existing, _ := hooks[event].([]any)
		hooks[event] = append(existing, tarr...)
	}

	root["hooks"] = hooks
	return writeJSON(target, root)
}

// entryIsWtOwned reports whether a Claude/Gemini hook entry
// ({matcher?, hooks:[{type,command}]}) contains a command referencing wt-hook.
func entryIsWtOwned(entry any) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := m["hooks"].([]any)
	if !ok {
		return false
	}
	for _, h := range hooks {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := hm["command"].(string); ok && strings.Contains(cmd, hookMarker) {
			return true
		}
	}
	return false
}

// installTOMLManaged writes the template as a delimited, versioned block. An
// existing wt block (old or new) is replaced in place; otherwise the block is
// appended. Content outside the delimiters is left alone.
func installTOMLManaged(src, target string, version int) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read template: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	block := fmt.Sprintf("%s (v%d) — managed by wt, do not edit\n%s%s\n",
		tomlBlockBegin, version, ensureTrailingNL(string(body)), tomlBlockEnd)

	existing, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return os.WriteFile(target, []byte(block), 0o644)
	} else if err != nil {
		return err
	}

	s := string(existing)
	if begin := strings.Index(s, tomlBlockBegin); begin >= 0 {
		endIdx := strings.Index(s[begin:], tomlBlockEnd)
		if endIdx >= 0 {
			end := begin + endIdx + len(tomlBlockEnd)
			// Swallow a trailing newline after the end marker to avoid drift.
			if end < len(s) && s[end] == '\n' {
				end++
			}
			replaced := s[:begin] + block + s[end:]
			return os.WriteFile(target, []byte(replaced), 0o644)
		}
	}

	// No existing block: append, separated by a blank line.
	out := ensureTrailingNL(s)
	if !strings.HasSuffix(out, "\n\n") {
		out += "\n"
	}
	return os.WriteFile(target, []byte(out+block), 0o644)
}

func ensureTrailingNL(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// readJSON / writeJSON are shared JSON file helpers used across the agent
// commands (hook install, MCP conversion, allow-list merge).
func readJSON(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return v, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// installSymlinkPlugin points target at src. An existing symlink is replaced; a
// real file is backed up to .bak first.
func installSymlinkPlugin(src, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return err
	}

	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(target); err != nil {
				return err
			}
		} else {
			if err := os.Rename(target, target+".bak"); err != nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	return os.Symlink(abs, target)
}

// cmdAgentsHooksStatus: wt-state agents hooks-status [--state-dir DIR] [--json]
//
// Reports installed vs expected hook version per installed agent, so wt (or the
// user) can tell when hooks are out of date and need a reinstall.
func cmdAgentsHooksStatus(args []string) {
	fs := flag.NewFlagSet("agents hooks-status", flag.ExitOnError)
	stateDirFlag := fs.String("state-dir", "", "dir for the hook-version state file (default WT_STATUS_DIR)")
	asJSON := fs.Bool("json", false, "output JSON")
	fs.Parse(args)

	state := readHookState(*stateDirFlag)
	type row struct {
		Agent     string `json:"agent"`
		Installed int    `json:"installed"` // 0 = not installed
		Expected  int    `json:"expected"`
		Status    string `json:"status"` // installed | outdated | not-installed
	}
	var rows []row
	for _, a := range registry {
		if !a.isInstalled() {
			continue
		}
		inst := state[a.Name]
		status := "installed"
		switch {
		case inst == 0:
			status = "not-installed"
		case inst < a.Hook.Version:
			status = "outdated"
		}
		rows = append(rows, row{a.Name, inst, a.Hook.Version, status})
	}

	if *asJSON {
		if rows == nil {
			rows = []row{}
		}
		printJSON(rows)
		return
	}
	for _, r := range rows {
		fmt.Printf("%-10s %s (installed v%d, expected v%d)\n", r.Agent, r.Status, r.Installed, r.Expected)
	}
}
