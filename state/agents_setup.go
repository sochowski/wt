// session-setup: prepare a fresh worktree for an agent. Runs the universal MCP
// seeding (.mcp.json from a profile) plus the agent's declared Setup steps
// (Claude allow-list merge, opencode config generation). Consolidates what used
// to be bin/wt's configure_mcp_profile + apply_allowed_tools + the embedded
// Python. Prints the resolved opencode config path (empty if none) on stdout;
// diagnostics go to stderr.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// cmdAgentSessionSetup: wt-state agent <name> session-setup --dir WT --session S --config-dir C
func cmdAgentSessionSetup(a Agent, args []string) {
	fs := flag.NewFlagSet("agent session-setup", flag.ExitOnError)
	dir := fs.String("dir", "", "worktree path to set up")
	session := fs.String("session", "", "tmux session name")
	configDir := fs.String("config-dir", os.Getenv("WT_CONFIG_DIR"), "wt config dir")
	profile := fs.String("profile", "default", "MCP profile name")
	fs.Parse(args)

	if *dir == "" {
		fatalf("session-setup: --dir is required")
	}

	// Universal: seed .mcp.json from the profile so MCP-aware agents pick it up.
	mcpFile := ensureMCPProfile(*dir, *configDir, *profile)

	opencodeConfig := ""
	for _, step := range a.Setup {
		switch step {
		case setupAllowedTools:
			if err := applyAllowedTools(*dir, *configDir); err != nil {
				fmt.Fprintf(os.Stderr, "session-setup: allowed-tools: %v\n", err)
			}
		case setupOpencodeMCP:
			if mcpFile == "" || *session == "" || *configDir == "" {
				continue
			}
			out := filepath.Join(*configDir, "opencode-mcp", *session+".json")
			written, err := writeOpencodeConfig(mcpFile, out)
			if err != nil {
				fmt.Fprintf(os.Stderr, "session-setup: opencode-mcp: %v\n", err)
			} else if written != "" {
				opencodeConfig = written
			}
		}
	}

	// The opencode config path is the one value bash consumes from stdout.
	fmt.Println(opencodeConfig)
}

// ensureMCPProfile seeds <dir>/.mcp.json from the named profile if the worktree
// doesn't already have one. Returns the .mcp.json path (existing or seeded), or
// "" if there's nothing to seed and none exists.
func ensureMCPProfile(dir, configDir, profile string) string {
	target := filepath.Join(dir, ".mcp.json")
	if _, err := os.Stat(target); err == nil {
		return target
	}
	if configDir == "" {
		return ""
	}
	src := filepath.Join(configDir, "mcp-profiles", profile+".json")
	b, err := os.ReadFile(src)
	if err != nil {
		return ""
	}
	if err := os.WriteFile(target, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "session-setup: seed .mcp.json: %v\n", err)
		return ""
	}
	return target
}

// applyAllowedTools merges wt's allow-list into <dir>/.claude/settings.local.json
// under permissions.allow, preserving any other keys. No-op when the allow-list
// file is absent. Ports bin/wt's apply_allowed_tools (removing its python3 use).
func applyAllowedTools(dir, configDir string) error {
	if configDir == "" {
		return nil
	}
	toolsFile := filepath.Join(configDir, "claude-allowed-tools.json")
	raw, err := os.ReadFile(toolsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var tools any
	if err := json.Unmarshal(raw, &tools); err != nil {
		return fmt.Errorf("parse %s: %w", toolsFile, err)
	}

	settingsDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		return err
	}
	settingsFile := filepath.Join(settingsDir, "settings.local.json")

	settings := map[string]any{}
	if existing, err := os.ReadFile(settingsFile); err == nil {
		if err := json.Unmarshal(existing, &settings); err != nil {
			return fmt.Errorf("parse %s: %w", settingsFile, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	perms["allow"] = tools
	settings["permissions"] = perms

	return writeJSON(settingsFile, settings)
}
