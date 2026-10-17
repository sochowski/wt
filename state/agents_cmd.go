// CLI dispatch for the agent registry: `wt-state agents ...` (roster-wide) and
// `wt-state agent <name> ...` (one agent). These commands never touch the
// SQLite store, so main dispatches them before opening the DB.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// cmdAgents handles the roster-wide subcommands: `list`, `install-hooks`.
func cmdAgents(args []string) {
	if len(args) == 0 {
		fatalf("agents: missing subcommand (list | install-hooks)")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		cmdAgentsList(rest)
	case "install-hooks":
		cmdAgentsInstallHooks(rest)
	case "hooks-status":
		cmdAgentsHooksStatus(rest)
	default:
		fatalf("agents: unknown subcommand %q (try: list, install-hooks, hooks-status)", sub)
	}
}

// cmdAgent handles per-agent subcommands: metadata (default), `launch-plan`,
// `session-setup`. Usage: wt-state agent <name> [subcommand] [flags].
func cmdAgent(args []string) {
	name, rest := popName(args, "agent")
	agent := lookupAgent(name)

	// Peek at the next token: a bare `agent <name>` (or one followed only by
	// flags) is a metadata query; a leading word selects a subcommand.
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		sub, subArgs := rest[0], rest[1:]
		switch sub {
		case "launch-plan":
			cmdAgentLaunchPlan(agent, subArgs)
		case "session-setup":
			cmdAgentSessionSetup(agent, subArgs)
		default:
			fatalf("agent: unknown subcommand %q (try: launch-plan, session-setup)", sub)
		}
		return
	}
	cmdAgentMeta(agent, rest)
}

// cmdAgentsList: wt-state agents list [--available] [--json]
//
// Without --json it prints one name per line (registry order) for easy `read`
// loops; --available filters to installed binaries.
func cmdAgentsList(args []string) {
	fs := flag.NewFlagSet("agents list", flag.ExitOnError)
	available := fs.Bool("available", false, "only agents whose binary is on PATH")
	asJSON := fs.Bool("json", false, "output JSON array instead of lines")
	fs.Parse(args)

	agents := registry
	if *available {
		agents = availableAgents()
	}
	names := agentNames(agents)
	if *asJSON {
		if names == nil {
			names = []string{}
		}
		printJSON(names)
		return
	}
	for _, n := range names {
		fmt.Println(n)
	}
}

// cmdAgentMeta: wt-state agent <name> [--field binary|name] [--json]
//
// Default output is the agent's metadata as JSON. --field prints one raw value.
func cmdAgentMeta(a Agent, args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	field := fs.String("field", "", "print a single raw value: name|binary|launch|installed")
	_ = fs.Bool("json", false, "output JSON (default)")
	fs.Parse(args)

	if *field != "" {
		switch *field {
		case "name":
			fmt.Println(a.Name)
		case "binary":
			fmt.Println(a.Binary)
		case "launch":
			fmt.Println(a.Launch)
		case "installed":
			fmt.Println(boolStr(a.isInstalled()))
		default:
			fatalf("agent: unknown field %q (try: name, binary, launch, installed)", *field)
		}
		return
	}
	printJSON(map[string]any{
		"name":      a.Name,
		"binary":    a.Binary,
		"launch":    a.Launch,
		"installed": a.isInstalled(),
		"hook":      a.Hook,
		"setup":     a.Setup,
	})
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// homeDir resolves the target home for install/setup operations. An explicit
// flag wins so install.sh and the staging harness can point at a fake $HOME.
func homeDir(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}
