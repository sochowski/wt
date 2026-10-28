package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseClaude(t *testing.T) {
	c, err := parseClaude(filepath.Join("testdata", "claude.jsonl"), true)
	if err != nil {
		t.Fatal(err)
	}
	r := c.Root()
	for k, want := range map[string]string{
		"agent.name": "fixture", "wt.workspace": "demo-child", "wt.parent_workspace": "demo", "agent.role": "child",
	} {
		if got := r.Str(k); got != want {
			t.Errorf("root %s = %q, want %q", k, got, want)
		}
	}

	byKind := map[string][]*Span{}
	for _, s := range c.Spans[1:] {
		byKind[s.Kind()] = append(byKind[s.Kind()], s)
		if s.TraceID != r.TraceID {
			t.Errorf("%s: trace id %s, want the root's", s.Name, s.TraceID)
		}
	}
	count := func(kind string, n int) {
		if len(byKind[kind]) != n {
			t.Errorf("%d %s spans, want %d", len(byKind[kind]), kind, n)
		}
	}
	count(KindTurn, 2)
	count(KindModel, 3)
	count(KindTool, 2)
	count(KindBackground, 1)
	count(KindWait, 1)

	dur := func(s *Span, want time.Duration) {
		t.Helper()
		if s.Dur() != want {
			t.Errorf("%s: %s, want %s", s.Name, s.Dur(), want)
		}
	}
	turn := byKind[KindTurn][0]
	dur(turn, 70*time.Second) // agrees with turn_duration
	dur(byKind[KindModel][0], 4*time.Second)
	if byKind[KindModel][0].Int("gen_ai.usage.input_tokens") != 106 {
		t.Errorf("input tokens should include cache reads and writes")
	}
	test := byKind[KindTool][0]
	dur(test, 60*time.Second)
	if !test.Error || test.Str("trace.command") != "uv run pytest -q" {
		t.Errorf("test tool: error=%v command=%q", test.Error, test.Str("trace.command"))
	}
	dur(byKind[KindBackground][0], 2*time.Minute+55*time.Second) // launch to completion notice

	w := byKind[KindWait][0]
	dur(w, 3*time.Minute+50*time.Second)
	if w.Str("wait.kind") != "human" || w.Str("trace.asked") != "Should I push?" {
		t.Errorf("wait: kind=%q asked=%q", w.Str("wait.kind"), w.Str("trace.asked"))
	}

	// OTLP round trip keeps everything the report and Perfetto read.
	back := fromOTLP(toOTLP(c))
	if len(back.Spans) != len(c.Spans) || back.Spans[3].Int("gen_ai.usage.output_tokens") != c.Spans[3].Int("gen_ai.usage.output_tokens") ||
		!back.Spans[0].Start.Equal(c.Spans[0].Start) || back.ID != c.ID {
		t.Errorf("OTLP round trip lost data")
	}
}

func TestNoText(t *testing.T) {
	c, _ := parseClaude(filepath.Join("testdata", "claude.jsonl"), false)
	for _, s := range c.Spans {
		for _, k := range []string{"trace.prompt", "trace.command", "trace.asked", "tool.file_path"} {
			if s.Str(k) != "" {
				t.Errorf("%s kept %s with -no-text", s.Name, k)
			}
		}
	}
}

func TestCommandName(t *testing.T) {
	for cmd, want := range map[string]string{
		"cd backend && uv run pytest -q":                                      "uv run pytest",
		"timeout 590 flock /tmp/x.lock make test a":                           "flock … make test",
		"FOO=1 BAR=2 git diff --stat":                                         "git diff",
		"set -a; source ../.env; uv run mypy app":                             "uv run mypy",
		"sed -i 's/a/b/' x.py; flock /tmp/l make test app/tests":              "flock … make test",
		"grep -n foo x.py; uv run --no-sync python - <<'EOF'\nimport os\nEOF": "uv run python",
		"for t in a b; do make check-$t; done":                                "make check-$t",
		"grep -rn foo app | head":                                             "grep",
		`until grep -qE "passed|failed" out; do sleep 5; done`:                "sleep",
		"python3 - <<'EOF'\nx='a; b'\nEOF\ncd backend && uv run pytest -q":    "uv run pytest",
		`PYTEST_ADDOPTS="-p no:cacheprovider" uv run pytest -q`:               "uv run pytest",
	} {
		if got := commandName(cmd); got != want {
			t.Errorf("commandName(%q) = %q, want %q", cmd, got, want)
		}
	}
}
