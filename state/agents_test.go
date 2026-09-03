package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLookupAgentKnownAndFallback(t *testing.T) {
	c := lookupAgent("claude")
	if c.Binary != "claude" || c.Launch != launchClaudeIDE {
		t.Fatalf("claude lookup = %+v", c)
	}
	p := lookupAgent("pi")
	if p.Binary != "pi" || p.Launch != launchDirect || p.Resume.IDFlag != "--session" {
		t.Fatalf("pi lookup = %+v", p)
	}
	if lookupAgent("opencode").Hook.Format != hookSymlinkPlugin {
		t.Fatal("opencode hook format changed; keep the legacy public value")
	}
	// Unknown names fall back to a direct-launch agent named after the binary.
	u := lookupAgent("frobnicate")
	if u.Name != "frobnicate" || u.Binary != "frobnicate" || u.Launch != launchDirect {
		t.Fatalf("fallback lookup = %+v", u)
	}
}

// A minimal Claude-style hook template with a wt-owned command.
const jsonHookTemplate = `{"hooks":{
	"PreToolUse":[{"matcher":"","hooks":[{"type":"command","command":"$HOME/bin/wt-hook pre-tool"}]}],
	"Stop":[{"matcher":"","hooks":[{"type":"command","command":"$HOME/bin/wt-hook stop"}]}]
}}`

// countWtEntries returns how many wt-owned entries exist under an event.
func countWtEntries(t *testing.T, target, event string) int {
	t.Helper()
	root := readAny(t, target).(map[string]any)
	hooks, _ := root["hooks"].(map[string]any)
	arr, _ := hooks[event].([]any)
	n := 0
	for _, e := range arr {
		if entryIsWtOwned(e) {
			n++
		}
	}
	return n
}

func TestInstallJSONManagedCreateAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "tmpl.json")
	writeFile(t, tmpl, jsonHookTemplate)
	target := filepath.Join(dir, "settings.json")

	// Create when absent.
	if err := installJSONManaged(tmpl, target); err != nil {
		t.Fatal(err)
	}
	if got := countWtEntries(t, target, "PreToolUse"); got != 1 {
		t.Fatalf("create: PreToolUse wt entries = %d, want 1", got)
	}

	// Re-run must not duplicate our entries.
	if err := installJSONManaged(tmpl, target); err != nil {
		t.Fatal(err)
	}
	if got := countWtEntries(t, target, "Stop"); got != 1 {
		t.Fatalf("idempotent: Stop wt entries = %d, want 1", got)
	}
}

func TestInstallJSONManagedPreservesForeignAndCleansLegacy(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "tmpl.json")
	writeFile(t, tmpl, jsonHookTemplate)
	target := filepath.Join(dir, "settings.json")

	// Existing file: a foreign hook we must keep, our own on a foreign key,
	// and a legacy wt entry under an event no longer in the template.
	writeFile(t, target, `{
		"model": "x",
		"hooks": {
			"PreToolUse": [{"matcher":"","hooks":[{"type":"command","command":"/usr/bin/other-tool"}]}],
			"PostToolUse": [{"matcher":"","hooks":[{"type":"command","command":"$HOME/bin/wt-hook post-tool legacy"}]}]
		}
	}`)

	if err := installJSONManaged(tmpl, target); err != nil {
		t.Fatal(err)
	}
	root := readAny(t, target).(map[string]any)

	// Foreign top-level key preserved.
	if root["model"] != "x" {
		t.Fatalf("model not preserved: %v", root)
	}
	// Legacy wt entry under PostToolUse removed (and the emptied event dropped).
	if _, has := root["hooks"].(map[string]any)["PostToolUse"]; has {
		t.Fatalf("legacy PostToolUse not cleaned: %v", root["hooks"])
	}
	// Foreign PreToolUse hook kept alongside exactly one fresh wt entry.
	pre := root["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(pre) != 2 {
		t.Fatalf("PreToolUse should have foreign + wt entry, got %d: %v", len(pre), pre)
	}
	if got := countWtEntries(t, target, "PreToolUse"); got != 1 {
		t.Fatalf("PreToolUse wt entries = %d, want 1", got)
	}
}

func TestInstallJSONManagedRejectsNonObject(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "tmpl.json")
	writeFile(t, tmpl, jsonHookTemplate)
	target := filepath.Join(dir, "settings.json")
	writeFile(t, target, `[1,2,3]`) // not an object
	if err := installJSONManaged(tmpl, target); err == nil {
		t.Fatalf("expected error on non-object settings file")
	}
}

func TestInstallTOMLManagedBlockReplace(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "notify.toml")
	writeFile(t, tmpl, "[notify]\ncommand = \"$HOME/bin/wt-hook stop\"\n")
	target := filepath.Join(dir, "config.toml")
	writeFile(t, target, "[model]\nname = \"x\"\n")

	if err := installTOMLManaged(tmpl, target, 1); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, target)
	if !contains(first, "[notify]") || !contains(first, "[model]") || !contains(first, tomlBlockBegin) {
		t.Fatalf("block install lost content: %q", first)
	}

	// Re-running at the same version replaces the block in place (no growth).
	if err := installTOMLManaged(tmpl, target, 1); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != first {
		t.Fatalf("re-install not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first, got)
	}
	if n := countSubstr(readFile(t, target), tomlBlockBegin); n != 1 {
		t.Fatalf("expected exactly one managed block, got %d", n)
	}

	// A version bump rewrites the block (new version marker present).
	if err := installTOMLManaged(tmpl, target, 2); err != nil {
		t.Fatal(err)
	}
	if !contains(readFile(t, target), "(v2)") {
		t.Fatalf("version bump not reflected: %q", readFile(t, target))
	}
	if n := countSubstr(readFile(t, target), tomlBlockBegin); n != 1 {
		t.Fatalf("version bump left %d blocks, want 1", n)
	}
}

func TestInstallTOMLManagedTopLevelKeyBeforeTables(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "notify.toml")
	writeFile(t, tmpl, "notify = [\"$HOME/bin/wt-hook\", \"stop\"]\n")
	target := filepath.Join(dir, "config.toml")
	writeFile(t, target, "model = \"gpt\"\n\n[features]\njs_repl = false\n")

	if err := installTOMLManaged(tmpl, target, 1); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, target)
	if !contains(got, "notify = [\"$HOME/bin/wt-hook\", \"stop\"]") {
		t.Fatalf("managed notify missing: %q", got)
	}
	if strings.Index(got, tomlBlockBegin) > strings.Index(got, "[features]") {
		t.Fatalf("managed top-level block was inserted inside [features]: %q", got)
	}
}

func TestInstallTOMLManagedTopLevelKeyConflictSkipsHook(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "notify.toml")
	writeFile(t, tmpl, "notify = [\"$HOME/bin/wt-hook\", \"stop\"]\n")
	target := filepath.Join(dir, "config.toml")
	writeFile(t, target, "notify = [\"/usr/bin/true\"]\n\n[features]\njs_repl = false\n")

	if err := installTOMLManaged(tmpl, target, 1); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, target)
	if contains(got, "wt-hook stop") {
		t.Fatalf("conflicting user notify should not be duplicated: %q", got)
	}
	if !contains(got, tomlBlockBegin) || strings.Index(got, tomlBlockBegin) > strings.Index(got, "[features]") {
		t.Fatalf("empty managed block should remain at top level: %q", got)
	}
}

func TestInstallSymlinkResourceReplacesAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "plugin.js")
	writeFile(t, src, "// plugin")
	target := filepath.Join(dir, "plugins", "wt-status.js")

	// Pre-existing real file gets backed up.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, target, "old")
	if err := installSymlinkResource(src, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target + ".bak"); err != nil {
		t.Fatalf("expected .bak backup: %v", err)
	}
	if got, _ := os.Readlink(target); got == "" {
		t.Fatalf("target is not a symlink")
	}
	// Re-running replaces the symlink cleanly (no second backup).
	if err := installSymlinkResource(src, target); err != nil {
		t.Fatal(err)
	}
}

func TestInstallSymlinkResourceSupportsDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "pi-wt")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "index.js"), "export default () => {}\n")
	target := filepath.Join(dir, ".pi", "agent", "extensions", "wt")

	if err := installSymlinkResource(src, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("target is not a directory symlink: %v", err)
	}
	if got != src {
		t.Fatalf("target points to %q, want %q", got, src)
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

func TestResumeArgs(t *testing.T) {
	claude := lookupAgent("claude")
	opencode := lookupAgent("opencode")
	pi := lookupAgent("pi")
	gemini := lookupAgent("gemini")
	home := t.TempDir()

	// opencode has no ProjectsDir to probe, so its id passes through as-is.
	if got := resumeArgs(opencode, "ses_abc", home); !reflect.DeepEqual(got, []string{"--session", "ses_abc"}) {
		t.Fatalf("opencode id: %v", got)
	}

	// Claude id with NO transcript on disk is a phantom (SessionStart mints ids
	// for sessions that may never materialize) -> not used; with no cwd session
	// either, fresh launch.
	if got := resumeArgs(claude, "sess-123", home); got != nil {
		t.Fatalf("claude phantom id: want nil, got %v", got)
	}

	// Rung 3: no id and no prior cwd session -> fresh launch (nil).
	if got := resumeArgs(claude, "", home); got != nil {
		t.Fatalf("claude no-id/no-session: want nil, got %v", got)
	}
	// opencode has no cwd fallback, so no id means fresh.
	if got := resumeArgs(opencode, "", home); got != nil {
		t.Fatalf("opencode no-id: want nil, got %v", got)
	}
	// Pi accepts an exact native session id via --session. Like opencode, its
	// session store is opaque to wt, so a captured id passes through as-is.
	if got := resumeArgs(pi, "0199-pi", home); !reflect.DeepEqual(got, []string{"--session", "0199-pi"}) {
		t.Fatalf("pi id: %v", got)
	}
	if got := resumeArgs(pi, "", home); got != nil {
		t.Fatalf("pi no-id: want nil, got %v", got)
	}
	// gemini has no ResumeSpec, so it never resumes even with an id.
	if got := resumeArgs(gemini, "anything", home); got != nil {
		t.Fatalf("gemini: want nil, got %v", got)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Join(home, ".claude", "projects", mangleProjectPath(cwd))
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Rung 2: phantom id but another session exists for the cwd -> --continue
	// (recovers the real conversation instead of failing on the phantom).
	writeFile(t, filepath.Join(projDir, "abc.jsonl"), "{}")
	if got := resumeArgs(claude, "sess-123", home); !reflect.DeepEqual(got, []string{"--continue"}) {
		t.Fatalf("claude phantom-id fallback: %v", got)
	}
	// No id at all, cwd session exists -> --continue.
	if got := resumeArgs(claude, "", home); !reflect.DeepEqual(got, []string{"--continue"}) {
		t.Fatalf("claude cwd-continue: %v", got)
	}

	// Rung 1: the id's transcript exists -> exact-id resume.
	writeFile(t, filepath.Join(projDir, "sess-123.jsonl"), "{}")
	if got := resumeArgs(claude, "sess-123", home); !reflect.DeepEqual(got, []string{"--resume", "sess-123"}) {
		t.Fatalf("claude id: %v", got)
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

func countSubstr(s, sub string) int {
	return strings.Count(s, sub)
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
