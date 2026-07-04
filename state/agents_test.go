package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLookupAgentKnownAndFallback(t *testing.T) {
	c := lookupAgent("claude")
	if c.Binary != "claude" || c.Launch != launchClaudeIDE {
		t.Fatalf("claude lookup = %+v", c)
	}
	// Unknown names fall back to a direct-launch agent named after the binary.
	u := lookupAgent("frobnicate")
	if u.Name != "frobnicate" || u.Binary != "frobnicate" || u.Launch != launchDirect {
		t.Fatalf("fallback lookup = %+v", u)
	}
}

func TestInstallJSONMergeCreateAndMerge(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "tmpl.json")
	writeFile(t, tmpl, `{"hooks":{"Stop":[1]},"a":1}`)
	target := filepath.Join(dir, "settings.json")

	// Create when absent.
	if _, err := installJSONMerge(tmpl, target); err != nil {
		t.Fatal(err)
	}
	got := readAny(t, target)
	want := map[string]any{"hooks": map[string]any{"Stop": []any{float64(1)}}, "a": float64(1)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("create: got %v want %v", got, want)
	}

	// Merge: preserve existing keys, template wins on conflicts (arrays replaced).
	writeFile(t, target, `{"a":99,"keep":true,"hooks":{"Start":[9]}}`)
	if _, err := installJSONMerge(tmpl, target); err != nil {
		t.Fatal(err)
	}
	got = readAny(t, target)
	want = map[string]any{
		"a":    float64(1),
		"keep": true,
		"hooks": map[string]any{
			"Start": []any{float64(9)},
			"Stop":  []any{float64(1)},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merge: got %v want %v", got, want)
	}
}

func TestInstallTOMLAppendIdempotent(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "notify.toml")
	writeFile(t, tmpl, "[notify]\ncommand = \"$HOME/bin/wt-hook stop\"\n")
	target := filepath.Join(dir, "config.toml")
	writeFile(t, target, "[model]\nname = \"x\"\n")

	if _, err := installTOMLAppend(tmpl, target, "wt-hook"); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, target)
	if !contains(first, "[notify]") || !contains(first, "[model]") {
		t.Fatalf("append lost content: %q", first)
	}
	// Second run is a no-op because the marker is present.
	if _, err := installTOMLAppend(tmpl, target, "wt-hook"); err != nil {
		t.Fatal(err)
	}
	if readFile(t, target) != first {
		t.Fatalf("second append not idempotent")
	}
}

func TestInstallSymlinkPluginReplacesAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "plugin.js")
	writeFile(t, src, "// plugin")
	target := filepath.Join(dir, "plugins", "wt-status.js")

	// Pre-existing real file gets backed up.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, target, "old")
	if _, err := installSymlinkPlugin(src, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target + ".bak"); err != nil {
		t.Fatalf("expected .bak backup: %v", err)
	}
	if got, _ := os.Readlink(target); got == "" {
		t.Fatalf("target is not a symlink")
	}
	// Re-running replaces the symlink cleanly (no second backup).
	if _, err := installSymlinkPlugin(src, target); err != nil {
		t.Fatal(err)
	}
}

func TestConvertMCPServers(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".mcp.json")
	writeFile(t, src, `{"mcpServers":{
		"local":{"command":"node","args":["x.js"],"env":{"K":"V"}},
		"remote":{"url":"https://example.com","headers":{"A":"B"}},
		"typed":{"type":"stdio","env":{"E":"1"}}
	}}`)

	cfg, ok, err := convertMCPConfig(src)
	if err != nil || !ok {
		t.Fatalf("convert: ok=%v err=%v", ok, err)
	}
	mcp := cfg["mcp"].(map[string]any)

	local := mcp["local"].(map[string]any)
	if local["type"] != "local" {
		t.Fatalf("local type = %v", local["type"])
	}
	if !reflect.DeepEqual(local["command"], []any{"node", "x.js"}) {
		t.Fatalf("local command = %v", local["command"])
	}
	if !reflect.DeepEqual(local["environment"], map[string]any{"K": "V"}) {
		t.Fatalf("local environment = %v", local["environment"])
	}

	remote := mcp["remote"].(map[string]any)
	if remote["type"] != "remote" || remote["url"] != "https://example.com" {
		t.Fatalf("remote = %v", remote)
	}

	typed := mcp["typed"].(map[string]any)
	if typed["type"] != "local" || typed["environment"] == nil {
		t.Fatalf("typed = %v", typed)
	}
	if _, has := typed["env"]; has {
		t.Fatalf("typed still has env: %v", typed)
	}
}

func TestConvertMCPEmpty(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".mcp.json")
	writeFile(t, src, `{"mcpServers":{}}`)
	if _, ok, err := convertMCPConfig(src); ok || err != nil {
		t.Fatalf("expected empty: ok=%v err=%v", ok, err)
	}
}

func TestApplyAllowedToolsMergePreservesKeys(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(configDir, "claude-allowed-tools.json"), `["Bash","Read"]`)

	wt := filepath.Join(dir, "wt")
	settings := filepath.Join(wt, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settings, `{"model":"x","permissions":{"deny":["Foo"]}}`)

	if err := applyAllowedTools(wt, configDir); err != nil {
		t.Fatal(err)
	}
	got := readAny(t, settings).(map[string]any)
	if got["model"] != "x" {
		t.Fatalf("model not preserved: %v", got)
	}
	perms := got["permissions"].(map[string]any)
	if !reflect.DeepEqual(perms["allow"], []any{"Bash", "Read"}) {
		t.Fatalf("allow = %v", perms["allow"])
	}
	if !reflect.DeepEqual(perms["deny"], []any{"Foo"}) {
		t.Fatalf("deny not preserved: %v", perms["deny"])
	}
}

func TestResolveOpencodeConfigPrecedence(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG", "")
	// Candidate wins over the default file.
	if got := resolveOpencodeConfig("/cand", "/cfg", "sess"); got != "/cand" {
		t.Fatalf("candidate: %q", got)
	}
	// Default file only when it exists.
	dir := t.TempDir()
	if got := resolveOpencodeConfig("", dir, "sess"); got != "" {
		t.Fatalf("missing default should be empty, got %q", got)
	}
	def := filepath.Join(dir, "opencode-mcp", "sess.json")
	if err := os.MkdirAll(filepath.Dir(def), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, def, "{}")
	if got := resolveOpencodeConfig("", dir, "sess"); got != def {
		t.Fatalf("default file: %q", got)
	}
	// An already-set env means we don't override.
	t.Setenv("OPENCODE_CONFIG", "/already")
	if got := resolveOpencodeConfig("/cand", dir, "sess"); got != "" {
		t.Fatalf("env-set should yield empty, got %q", got)
	}
}

func TestIDELockCount(t *testing.T) {
	home := t.TempDir()
	ide := filepath.Join(home, ".claude", "ide")
	if err := os.MkdirAll(ide, 0o755); err != nil {
		t.Fatal(err)
	}
	// A live lock (our own pid) survives; a dead-pid lock is pruned.
	writeFile(t, filepath.Join(ide, "live.lock"), `{"pid":`+itoa(os.Getpid())+`}`)
	writeFile(t, filepath.Join(ide, "dead.lock"), `{"pid":2147480000}`)

	if n := ideLockCount(home); n != 1 {
		t.Fatalf("lock count = %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(ide, "dead.lock")); !os.IsNotExist(err) {
		t.Fatalf("dead lock not pruned")
	}
}

// --- helpers ---

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readAny(t *testing.T, path string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(readFile(t, path)), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
