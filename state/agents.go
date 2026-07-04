// Agent registry — the single source of truth for the CLI agents wt drives.
//
// Everything wt needs to know about an agent lives in one struct entry below:
// its binary, how its status hooks are installed, how it launches, and any
// per-session setup it needs. bash callers (wt, wt-agent-launch, wt-hook,
// install.sh) query this via the `agent`/`agents` subcommands instead of
// hardcoding per-agent case statements. Adding an agent = one entry here plus
// its hook template under config/.
//
// Display note: an agent's Name is its display string everywhere. There is no
// separate label — status messages, notifications and pickers all use Name.
package main

import "os/exec"

// Hook install formats. Each agent wires wt-hook into its own config in its own
// way; the format tells install-hooks which mechanism to use for the template.
const (
	hookJSONMerge     = "json-merge"     // deep-merge template into a JSON settings file
	hookTOMLAppend    = "toml-append"    // append a TOML block if the marker is absent
	hookSymlinkPlugin = "symlink-plugin" // symlink the template into a plugins dir
)

// Launch styles. How wt-agent-launch should start the binary.
const (
	launchDirect    = "direct"     // exec the binary as-is
	launchClaudeIDE = "claude-ide" // reconcile Neovim IDE locks, add --ide when one exists
	launchOpencode  = "opencode"   // resolve + export OPENCODE_CONFIG, then exec
)

// Per-session setup steps run against a fresh worktree before the agent starts.
const (
	setupAllowedTools = "allowed-tools" // merge wt's allow-list into .claude/settings.local.json
	setupOpencodeMCP  = "opencode-mcp"  // translate .mcp.json into an opencode config
)

// HookSpec declares how an agent's status hooks are installed into $HOME.
type HookSpec struct {
	Format   string // one of hook* above
	Template string // filename under the template dir (config/)
	Target   string // path relative to home, e.g. ".claude/settings.json"
	Marker   string // toml-append idempotency probe (substring to look for)
}

// Agent is one CLI agent wt can drive.
type Agent struct {
	Name   string
	Binary string
	Hook   HookSpec
	Launch string   // one of launch* above
	Setup  []string // setup* steps, in order
}

// registry is the whole roster. Order is the canonical display order.
var registry = []Agent{
	{
		Name:   "claude",
		Binary: "claude",
		Hook:   HookSpec{Format: hookJSONMerge, Template: "claude-hooks.json", Target: ".claude/settings.json"},
		Launch: launchClaudeIDE,
		Setup:  []string{setupAllowedTools},
	},
	{
		Name:   "codex",
		Binary: "codex",
		Hook:   HookSpec{Format: hookTOMLAppend, Template: "codex-notify.toml", Target: ".codex/config.toml", Marker: "wt-hook"},
		Launch: launchDirect,
	},
	{
		Name:   "gemini",
		Binary: "gemini",
		Hook:   HookSpec{Format: hookJSONMerge, Template: "hooks-gemini.json", Target: ".gemini/settings.json"},
		Launch: launchDirect,
	},
	{
		Name:   "opencode",
		Binary: "opencode",
		Hook:   HookSpec{Format: hookSymlinkPlugin, Template: "opencode-wt-plugin.js", Target: ".config/opencode/plugins/wt-status.js"},
		Launch: launchOpencode,
		Setup:  []string{setupOpencodeMCP},
	},
}

// lookupAgent returns the registry entry for name. Unknown names resolve to a
// synthetic direct-launch agent whose binary is the name itself, mirroring the
// old bash fallbacks (`agent_binary` echoed "$1"; wt-agent-launch exec'd it).
func lookupAgent(name string) Agent {
	for _, a := range registry {
		if a.Name == name {
			return a
		}
	}
	return Agent{Name: name, Binary: name, Launch: launchDirect}
}

// isInstalled reports whether an agent's binary is on PATH.
func (a Agent) isInstalled() bool {
	_, err := exec.LookPath(a.Binary)
	return err == nil
}

// availableAgents returns registry entries whose binary is on PATH, in
// canonical order.
func availableAgents() []Agent {
	var out []Agent
	for _, a := range registry {
		if a.isInstalled() {
			out = append(out, a)
		}
	}
	return out
}

// agentNames returns the names of the given agents, in the order given.
func agentNames(agents []Agent) []string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.Name
	}
	return out
}
