package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const durableMcpProfile = "mcp-boundary-v1"
const durableMcpDefinition = "wt-durable-mcp-v1"
const durableMcpDependencies = durableDependencies + ";pi-mcp-adapter=5.1.0;mcp-client=2.2.0;mcp-core=2.2.0;wt-mcp-bridge=1"

type durableMcpAdmission struct {
	Path   string
	Digest string
	Config json.RawMessage
	Tools  []string
}

// Human-selected, immutable file/ceiling. No ambient config, auth, process,
// native backend, read-only escalation, or automatic server discovery admission.
func admitDurableMcp(profile, backend string, readOnly bool, path, tools string) (durableMcpAdmission, error) {
	var out durableMcpAdmission
	if profile != durableMcpProfile {
		if path != "" || tools != "" {
			return out, errors.New("MCP flags require explicit mcp-boundary-v1 profile")
		}
		return out, nil
	}
	if backend != "durable" || readOnly || os.Getenv("WT_AGENT_ID") != "" || path == "" || tools == "" {
		return out, errors.New("MCP profile requires human fresh-store admission, config and explicit tool ceiling")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return out, err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return out, err
	}
	if canonical != absolute {
		return out, errors.New("MCP config must use its exact canonical file path")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
		return out, errors.New("MCP config must be a bounded regular JSON file")
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return out, err
	}
	// Full server semantics are validated by the actual runtime before effects.
	var config struct {
		Servers []struct {
			Name  string   `json:"name"`
			URL   string   `json:"url"`
			Tools []string `json:"tools"`
		} `json:"servers"`
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&config); err != nil {
		return out, errors.New("MCP config has unknown or invalid fields")
	}
	if dec.Decode(new(any)) != io.EOF || len(config.Servers) < 1 || len(config.Servers) > 4 {
		return out, errors.New("MCP config requires one to four explicit servers")
	}
	serverNames := map[string]bool{}
	for _, server := range config.Servers {
		endpoint, err := url.ParseRequestURI(server.URL)
		if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`).MatchString(server.Name) || serverNames[server.Name] || err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || len(server.Tools) < 1 || len(server.Tools) > 16 {
			return out, errors.New("MCP server URL/name/options are unsupported")
		}
		serverNames[server.Name] = true
		selected := map[string]bool{}
		for _, tool := range server.Tools {
			if tool == "" || len(tool) > 128 || selected[tool] || strings.ContainsRune(tool, '\x00') {
				return out, errors.New("MCP selected tools must be bounded and unique")
			}
			selected[tool] = true
		}
	}
	seen := map[string]bool{}
	for _, name := range strings.Split(tools, ",") {
		if name == "" || name != strings.TrimSpace(name) || len(name) > 128 || seen[name] {
			return out, errors.New("MCP tool ceiling must be nonempty, unique exact tool names")
		}
		for _, c := range name {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				return out, errors.New("MCP tool names must be ASCII identifier names")
			}
		}
		seen[name] = true
		out.Tools = append(out.Tools, name)
	}
	if len(out.Tools) > 64 {
		return out, errors.New("MCP tool ceiling exceeds 64 names")
	}
	sum := sha256.Sum256(data)
	out.Path = canonical
	out.Digest = hex.EncodeToString(sum[:])
	out.Config = append(json.RawMessage(nil), data...)
	return out, nil
}
