package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMcpFreshHumanAdmissionPreservesConfigAndExactCeiling(t *testing.T) {
	t.Setenv("WT_AGENT_ID", "")
	dir, err := canonicalDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mcp.json")
	data := []byte(`{"servers":[{"name":"private","url":"http://127.0.0.1:1/mcp","tools":["echo"]}]}`)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := admitDurableMcp(durableMcpProfile, "durable", false, path, "private_echo")
	if err != nil {
		t.Fatal(err)
	}
	if a.Path != path || len(a.Digest) != 64 || string(a.Config) != string(data) || len(a.Tools) != 1 || a.Tools[0] != "private_echo" {
		t.Fatal(a)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(data) {
		t.Fatal("config changed")
	}
	for _, profile := range []string{"", durablePluginProfile} {
		if _, err := admitDurableMcp(profile, "durable", false, path, "private_echo"); err == nil {
			t.Fatal("MCP flags adopted by old profile")
		}
	}
	if _, err := admitDurableMcp(durableMcpProfile, "native", false, path, "private_echo"); err == nil {
		t.Fatal("native MCP admitted")
	}
	if _, err := admitDurableMcp(durableMcpProfile, "durable", true, path, "private_echo"); err == nil {
		t.Fatal("reader acquired MCP effects")
	}
	t.Setenv("WT_AGENT_ID", "model")
	if _, err := admitDurableMcp(durableMcpProfile, "durable", false, path, "private_echo"); err == nil {
		t.Fatal("model selected MCP admission")
	}
}
func TestMcpInvalidOptionsRejectBeforeRootCreation(t *testing.T) {
	s := worktreeTestStore(t)
	t.Setenv("WT_AGENT_ID", "")
	t.Setenv("WT_BASE_DIR", filepath.Join(t.TempDir(), "worktrees"))
	dir, _ := canonicalDir(t.TempDir())
	path := filepath.Join(dir, "mcp.json")
	for _, text := range []string{
		`{"servers":[]}`, `{"servers":[{"name":"private","url":"http://user:secret@localhost/mcp","tools":["echo"]}]}`,
		`{"servers":[{"name":"private","url":"http://localhost/mcp","tools":["echo"],"command":"unapproved"}]}`,
		`{"servers":[{"name":"private","url":"file:///tmp/x","tools":["echo"]}]}`,
	} {
		os.WriteFile(path, []byte(text), 0600)
		if err := worktreeCommand(s, []string{"new", "bad-mcp", "--offline", "--durable-profile", durableMcpProfile, "--mcp-config", path, "--mcp-tools", "private_echo"}); err == nil {
			t.Fatal("invalid MCP root created")
		}
		var count int
		s.db.QueryRow(`SELECT count(*) FROM roots WHERE name='bad-mcp'`).Scan(&count)
		if count != 0 {
			t.Fatal("invalid config spent root admission")
		}
	}
}
func TestMcpSnapshotCompatibilityIsExplicit(t *testing.T) {
	data, _ := json.Marshal(DurableSnapshot{Profile: durableMcpProfile, McpTools: []string{"private_echo"}})
	var got DurableSnapshot
	if err := json.Unmarshal(data, &got); err != nil || got.McpTools[0] != "private_echo" {
		t.Fatal(err)
	}
	legacy, _ := json.Marshal(DurableSnapshot{Definition: durableDefinition, Dependencies: durableDependencies})
	var fields map[string]any
	json.Unmarshal(legacy, &fields)
	for _, key := range []string{"mcp_config", "mcp_config_file", "mcp_config_digest", "mcp_tools", "mcp_source", "mcp_contract"} {
		if _, exists := fields[key]; exists {
			t.Fatal("ordinary snapshot gained MCP fields")
		}
	}
}
