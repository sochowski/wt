// MCP config translation for opencode. Ports the Python heredoc that used to
// live inside bin/wt (write_opencode_mcp_config): read an .mcp.json (either
// opencode's own `mcp` shape or the standard `mcpServers` shape) and emit an
// opencode config. Removes the python3 dependency from the worktree-setup path.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// convertMCPConfig reads source (an .mcp.json), translates it to an opencode
// config object, and reports whether there were any servers to convert. It
// returns ok=false when the source has no MCP servers (nothing to write).
func convertMCPConfig(source string) (map[string]any, bool, error) {
	b, err := os.ReadFile(source)
	if err != nil {
		return nil, false, err
	}
	var config map[string]any
	if err := json.Unmarshal(b, &config); err != nil {
		return nil, false, err
	}

	var mcp map[string]any
	if existing, ok := config["mcp"].(map[string]any); ok {
		mcp = existing
	} else {
		servers, _ := config["mcpServers"].(map[string]any)
		mcp = make(map[string]any, len(servers))
		for name, srv := range servers {
			if sm, ok := srv.(map[string]any); ok {
				mcp[name] = convertServer(sm)
			}
		}
	}

	if len(mcp) == 0 {
		return nil, false, nil
	}
	return map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"mcp":     mcp,
	}, true, nil
}

// convertServer maps one standard MCP server entry to opencode's shape,
// mirroring the original Python's branch order exactly.
func convertServer(server map[string]any) map[string]any {
	serverType, _ := server["type"].(string)

	if url, ok := server["url"]; ok {
		converted := map[string]any{"type": "remote", "url": url}
		for _, key := range []string{"enabled", "headers", "oauth", "timeout"} {
			if v, ok := server[key]; ok {
				converted[key] = v
			}
		}
		return converted
	}

	if cmd, ok := server["command"]; ok {
		converted := map[string]any{
			"type":    "local",
			"command": asCommand(cmd, server["args"]),
		}
		env, hasEnv := server["environment"]
		if !hasEnv {
			env, hasEnv = server["env"]
		}
		if hasEnv {
			converted["environment"] = env
		}
		for _, key := range []string{"enabled", "timeout"} {
			if v, ok := server[key]; ok {
				converted[key] = v
			}
		}
		return converted
	}

	// No url/command: normalise the type in place, like the Python fallback.
	out := make(map[string]any, len(server))
	for k, v := range server {
		out[k] = v
	}
	switch serverType {
	case "http", "sse":
		out["type"] = "remote"
	case "stdio":
		out["type"] = "local"
		if env, ok := out["env"]; ok {
			if _, has := out["environment"]; !has {
				out["environment"] = env
				delete(out, "env")
			}
		}
	}
	return out
}

// asCommand normalises a command (string or list) plus optional args into a
// single argv list.
func asCommand(value, args any) []any {
	var command []any
	switch v := value.(type) {
	case []any:
		command = append(command, v...)
	default:
		command = append(command, v)
	}
	if al, ok := args.([]any); ok {
		command = append(command, al...)
	}
	return command
}

// writeOpencodeConfig converts source and writes the opencode config to output.
// It returns the output path when written, or "" when there was nothing to
// convert (source missing or no servers).
func writeOpencodeConfig(source, output string) (string, error) {
	if _, err := os.Stat(source); err != nil {
		return "", nil // no .mcp.json to convert
	}
	cfg, ok, err := convertMCPConfig(source)
	if err != nil || !ok {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return "", err
	}
	if err := writeJSON(output, cfg); err != nil {
		return "", err
	}
	return output, nil
}
