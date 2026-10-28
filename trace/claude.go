package main

// The Claude Code adapter. It reads ~/.claude/projects/<dir>/<session>.jsonl,
// and the built-in subagents saved beside it in <session>/subagents/.
//
// What the transcript gives us, per entry type:
//   api-request        a model request was sent (id, timestamp, model)
//   assistant          one block of the reply; requestRef links it to its request, usage has tokens
//   user               your message (origin.kind=human), a task notification, or tool results
//   system             turn_duration ends a turn; compact_boundary is a compaction; scheduled_task_fire a wakeup
//   queue-operation    a background command finished (<tool-use-id> in its content)

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ccEntry struct {
	Type             string                  `json:"type"`
	Subtype          string                  `json:"subtype"`
	UUID             string                  `json:"uuid"`
	ID               string                  `json:"id"`
	Timestamp        time.Time               `json:"timestamp"`
	SessionID        string                  `json:"sessionId"`
	Cwd              string                  `json:"cwd"`
	GitBranch        string                  `json:"gitBranch"`
	Version          string                  `json:"version"`
	IsMeta           bool                    `json:"isMeta"`
	IsCompactSummary bool                    `json:"isCompactSummary"`
	IsAPIError       bool                    `json:"isApiErrorMessage"`
	Origin           *struct{ Kind string }  `json:"origin"`
	RequestRef       string                  `json:"requestRef"`
	QuerySource      string                  `json:"querySource"`
	Params           *struct{ Model string } `json:"params"`
	Message          *ccMessage              `json:"message"`
	ToolUseResult    json.RawMessage         `json:"toolUseResult"`
	DurationMs       int64                   `json:"durationMs"`
	CompactMetadata  *struct {
		Trigger    string
		PreTokens  int64 `json:"preTokens"`
		PostTokens int64 `json:"postTokens"`
		DurationMs int64 `json:"durationMs"`
	} `json:"compactMetadata"`
	Content     json.RawMessage `json:"content"`
	Operation   string          `json:"operation"`
	CustomTitle string          `json:"customTitle"`
	AgentName   string          `json:"agentName"`
}

type ccMessage struct {
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      *struct {
		Input      int64 `json:"input_tokens"`
		Output     int64 `json:"output_tokens"`
		CacheRead  int64 `json:"cache_read_input_tokens"`
		CacheWrite int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

type ccBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Text      string          `json:"text"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type ccSubagentMeta struct {
	AgentType   string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
}

// claudeSources lists every transcript under the projects directory,
// subagents included.
func claudeSources() []string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".claude")
	}
	dir = filepath.Join(dir, "projects")
	a, _ := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	b, _ := filepath.Glob(filepath.Join(dir, "*", "*", "subagents", "*.jsonl"))
	return append(a, b...)
}

var (
	reWorkspace = regexp.MustCompile(`/worktrees/\.sessions/([^/]+)`)
	reBrief     = regexp.MustCompile(`(/[^\s'"` + "`" + `]+)/\.wt/agents/([A-Za-z0-9_.-]+)/brief[^\s/]*\.md`)
	reToolUseID = regexp.MustCompile(`<tool-use-id>([^<]+)</tool-use-id>`)
	reTaskID    = regexp.MustCompile(`<task-id>([^<]+)</task-id>`)
)

type ccParser struct {
	c       *Conversation
	session string
	trace   string
	root    *Span
	text    bool // keep the first 200 characters of prompts and commands

	turn        *Span
	turns       int
	lastTurnEnd time.Time
	lastText    string // the last thing the agent said, for "what it asked you"
	lastStop    string
	last        time.Time // the last activity of any kind

	requests map[string]time.Time // api-request id → when it was sent
	models   map[string]*Span     // api-request id → its model span
	tools    map[string]*Span     // tool_use id → its span, until the result
	bg       map[string]*Span     // tool_use id → background command still running
	tasks    map[string]string    // background task id → its tool_use id
	acts     []time.Time          // every moment of activity, to end what never finished
	open     map[string]bool      // requests not yet answered
	subagent bool
}

func parseClaude(path string, text bool) (*Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	session := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	p := &ccParser{
		session:  session,
		text:     text,
		requests: map[string]time.Time{},
		models:   map[string]*Span{},
		tools:    map[string]*Span{},
		bg:       map[string]*Span{},
		tasks:    map[string]string{},
		open:     map[string]bool{},
	}
	p.c = &Conversation{Harness: "claude", ID: session, Source: path}
	p.trace = traceID(session)
	p.root = &Span{Name: session, Attrs: map[string]any{
		"trace.kind": KindConversation, "trace.harness": "claude", "session.id": session,
	}}

	// A built-in subagent: <parent>/subagents/agent-<id>.jsonl, with a meta file
	// naming the tool call that launched it. It joins its parent's trace, under
	// that tool call.
	if filepath.Base(filepath.Dir(path)) == "subagents" {
		p.subagent = true
		parent := filepath.Base(filepath.Dir(filepath.Dir(path)))
		p.trace = traceID(parent)
		p.root.Set("trace.parent_session", parent)
		var meta ccSubagentMeta
		if b, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
			json.Unmarshal(b, &meta)
		}
		if meta.ToolUseID != "" {
			p.root.Parent = spanID(parent, "tool:"+meta.ToolUseID)
			p.root.Set("trace.parent_tool_use", meta.ToolUseID)
		}
		name := strings.TrimSpace(meta.AgentType + ": " + meta.Description)
		p.root.Set("agent.name", strings.Trim(name, ": "))
	}
	p.root.TraceID, p.root.ID = p.trace, spanID(session, "root")
	p.c.Spans = []*Span{p.root}

	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e ccEntry
			if json.Unmarshal(line, &e) == nil {
				p.entry(&e)
			}
		}
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
	}
	p.finish()
	return p.c, nil
}

func (p *ccParser) span(key, name, kind string, start time.Time, parent *Span) *Span {
	s := &Span{TraceID: p.trace, ID: spanID(p.session, key), Name: name, Start: start, End: start,
		Attrs: map[string]any{"trace.kind": kind}}
	if parent != nil {
		s.Parent = parent.ID
	}
	p.c.Spans = append(p.c.Spans, s)
	return s
}

func (p *ccParser) entry(e *ccEntry) {
	if !e.Timestamp.IsZero() && e.Type != "queue-operation" {
		p.acts = append(p.acts, e.Timestamp)
	}
	switch e.Type {
	case "custom-title":
		if e.CustomTitle != "" && !p.subagent {
			p.root.Set("agent.name", e.CustomTitle)
		}
		return
	case "agent-name":
		if e.AgentName != "" && !p.subagent {
			p.root.Set("agent.name", e.AgentName)
		}
		return
	}
	if e.Timestamp.IsZero() {
		return
	}
	t := e.Timestamp
	if p.root.Start.IsZero() || t.Before(p.root.Start) {
		p.root.Start = t
	}
	if t.After(p.root.End) {
		p.root.End = t
	}
	if e.Cwd != "" && p.root.Str("process.cwd") == "" {
		p.root.Set("process.cwd", e.Cwd)
		if m := reWorkspace.FindStringSubmatch(e.Cwd); m != nil {
			p.root.Set("wt.workspace", m[1])
		}
	}
	if e.Version != "" {
		p.root.Set("harness.version", e.Version)
	}
	if e.GitBranch != "" && p.root.Str("vcs.branch") == "" {
		p.root.Set("vcs.branch", e.GitBranch)
	}

	switch e.Type {
	case "api-request":
		p.requests[e.ID] = t
		p.open[e.ID] = true
		p.ensureTurn(t)

	case "assistant":
		p.assistant(e, t)

	case "user":
		p.user(e, t)

	case "system":
		switch e.Subtype {
		case "turn_duration":
			if p.turn != nil {
				p.turn.Set("harness.turn_ms", e.DurationMs)
				p.closeTurn(t)
			}
			p.last = t
		case "compact_boundary":
			if m := e.CompactMetadata; m != nil {
				s := p.span("compact:"+e.UUID, "compaction", KindCompaction, t.Add(-time.Duration(m.DurationMs)*time.Millisecond), p.turnOrRoot())
				s.End = t
				s.Set("compaction.trigger", m.Trigger)
				s.Set("compaction.pre_tokens", m.PreTokens)
				s.Set("compaction.post_tokens", m.PostTokens)
			}
			p.last = t
		case "scheduled_task_fire":
			p.start(t, "timer", jsonString(e.Content))
		}

	case "queue-operation":
		if e.Operation == "enqueue" {
			p.endBackground(jsonString(e.Content), t)
		}
	}
}

func (p *ccParser) assistant(e *ccEntry, t time.Time) {
	m := e.Message
	if m == nil {
		return
	}
	p.ensureTurn(t)
	ref := e.RequestRef
	if ref == "" {
		ref = "uuid:" + e.UUID
	}
	s := p.models[ref]
	if s == nil {
		start, ok := p.requests[ref]
		if !ok {
			start = p.last // no request entry: the call started after the last thing we saw
		}
		s = p.span("model:"+ref, "model", KindModel, start, p.turn)
		if !ok {
			s.Set("trace.timing", "inferred")
		}
		p.models[ref] = s
	}
	s.End = t
	delete(p.open, ref)
	if m.Model != "" {
		s.Set("gen_ai.request.model", m.Model)
	}
	if u := m.Usage; u != nil {
		s.Set("gen_ai.usage.input_tokens", u.Input+u.CacheRead+u.CacheWrite)
		s.Set("gen_ai.usage.output_tokens", u.Output)
		s.Set("gen_ai.usage.cache_read_input_tokens", u.CacheRead)
		s.Set("gen_ai.usage.cache_creation_input_tokens", u.CacheWrite)
	}
	if m.StopReason != "" {
		s.Set("gen_ai.response.finish_reasons", m.StopReason)
		p.lastStop = m.StopReason
	}
	if e.IsAPIError {
		s.Error = true
	}
	var blocks []ccBlock
	json.Unmarshal(m.Content, &blocks)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				p.lastText = b.Text
			}
		case "tool_use":
			ts := p.span("tool:"+b.ID, b.Name, KindTool, t, p.turn)
			ts.Set("gen_ai.tool.name", b.Name)
			ts.Set("gen_ai.tool.call.id", b.ID)
			p.describeTool(ts, b.Name, b.Input)
			p.tools[b.ID] = ts
			if b.Name == "TaskStop" || b.Name == "KillShell" { // the agent killed a background command
				var in struct {
					TaskID  string `json:"task_id"`
					ShellID string `json:"shell_id"`
				}
				json.Unmarshal(b.Input, &in)
				for _, id := range []string{in.TaskID, in.ShellID} {
					if tu := p.tasks[id]; tu != "" && p.bg[tu] != nil {
						p.bg[tu].End = t
						p.bg[tu].Set("tool.stopped", true)
						delete(p.bg, tu)
					}
				}
			}
		}
	}
	p.last = t
}

// endBackground ends the background command a completion notice names.
func (p *ccParser) endBackground(content string, t time.Time) {
	id := ""
	if m := reToolUseID.FindStringSubmatch(content); m != nil {
		id = m[1]
	} else if m := reTaskID.FindStringSubmatch(content); m != nil {
		id = p.tasks[m[1]]
	}
	if s := p.bg[id]; s != nil {
		s.End = t
		delete(p.bg, id)
	}
}

// quietAfter is the last activity after t before the conversation went quiet
// for over two hours.
func (p *ccParser) quietAfter(t time.Time) time.Time {
	end := t
	for _, a := range p.acts {
		if a.Before(t) {
			continue
		}
		if a.Sub(end) > 2*time.Hour {
			break
		}
		end = a
	}
	return end
}

// describeTool records what a tool call did, in a form the report can match:
// the command, the file, the pattern.
func (p *ccParser) describeTool(s *Span, name string, input json.RawMessage) {
	var in map[string]any
	json.Unmarshal(input, &in)
	what := ""
	for _, k := range []string{"command", "file_path", "pattern", "description", "query", "url", "prompt", "skill"} {
		if v := str(in[k]); v != "" {
			what = v
			break
		}
	}
	if what == "" && len(input) > 2 {
		what = string(input)
	}
	if cmd := str(in["command"]); name == "Bash" && cmd != "" {
		// Named from the whole command, which trace.command only keeps the
		// start of. Just program names, so kept even without text.
		s.Set("tool.command_name", commandName(cmd))
		if reTests.MatchString(cmd) {
			s.Set("tool.test_run", true)
		}
		if rePolling.MatchString(cmd) {
			s.Set("tool.polling", true)
		}
	}
	if b, _ := in["run_in_background"].(bool); b {
		s.Set("tool.background", true)
	}
	if fp := str(in["file_path"]); fp != "" && p.text {
		s.Set("tool.file_path", fp)
	}
	if p.text && what != "" {
		s.Set("trace.command", clip(what, 200))
		s.Name = name + ": " + clip(firstLine(what), 60)
	}
}

func (p *ccParser) user(e *ccEntry, t time.Time) {
	if e.Message == nil || e.IsCompactSummary {
		return
	}
	var text string
	var blocks []ccBlock
	if json.Unmarshal(e.Message.Content, &text) != nil {
		json.Unmarshal(e.Message.Content, &blocks)
	}
	results := false
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			results = true
			p.toolResult(e, &b, t)
		case "text":
			text += b.Text
		}
	}
	if results || e.IsMeta || strings.TrimSpace(text) == "" {
		if results {
			p.last = t
		}
		return
	}
	if strings.HasPrefix(text, "[Request interrupted") {
		if p.turn != nil {
			p.turn.Set("turn.interrupted", true)
			p.closeTurn(t)
		}
		return
	}
	if strings.HasPrefix(text, "<local-command-") {
		return
	}
	kind := "human"
	switch {
	case p.subagent:
		kind = "parent"
	case e.Origin != nil && e.Origin.Kind != "" && e.Origin.Kind != "human":
		kind = e.Origin.Kind // task-notification, …
	}
	if kind == "task-notification" {
		p.endBackground(text, t)
	}
	if kind == "human" && p.root.Str("wt.parent_workspace") == "" {
		if m := reBrief.FindStringSubmatch(text); m != nil {
			p.root.Set("wt.parent_workspace", filepath.Base(m[1]))
			p.root.Set("agent.role", m[2])
		}
	}
	p.start(t, kind, text)
}

func (p *ccParser) toolResult(e *ccEntry, b *ccBlock, t time.Time) {
	s := p.tools[b.ToolUseID]
	if s == nil {
		return
	}
	delete(p.tools, b.ToolUseID)
	s.End = t
	if b.IsError {
		s.Error = true
	}
	s.Set("tool.output_bytes", int64(len(b.Content)))
	var r struct {
		Interrupted      bool   `json:"interrupted"`
		BackgroundTaskID string `json:"backgroundTaskId"`
		TimedOutAfterMs  int64  `json:"timedOutAfterMs"` // hit its timeout and was moved to the background
	}
	json.Unmarshal(e.ToolUseResult, &r)
	if r.Interrupted {
		s.Set("tool.interrupted", true)
	}
	if r.TimedOutAfterMs > 0 {
		s.Set("tool.timed_out_ms", r.TimedOutAfterMs)
	}
	if r.BackgroundTaskID != "" {
		bg := p.span("bg:"+b.ToolUseID, "background: "+strings.TrimPrefix(s.Name, "Bash: "), KindBackground, s.Start, p.root)
		bg.End = t
		bg.Set("gen_ai.tool.call.id", b.ToolUseID)
		if c := s.Str("trace.command"); c != "" {
			bg.Set("trace.command", c)
		}
		p.bg[b.ToolUseID] = bg
		p.tasks[r.BackgroundTaskID] = b.ToolUseID
	}
}

func (p *ccParser) turnOrRoot() *Span {
	if p.turn != nil {
		return p.turn
	}
	return p.root
}

// ensureTurn opens a turn if activity shows up outside one, e.g. the first
// request of a subagent, or a transcript that starts mid-conversation.
func (p *ccParser) ensureTurn(t time.Time) {
	if p.turn == nil {
		p.start(t, "unknown", "")
	}
}

// start begins a turn: the wait before it, then the turn itself. Claude Code
// writes a message into the transcript only when it hands it to the model, so
// a message always starts a new turn, even one typed while the agent worked.
// If the open turn's last reply didn't end it, the agent had stalled (an API
// error, a hung tool) and the message is what got it going again.
func (p *ccParser) start(t time.Time, kind, text string) {
	stalled := false
	if p.turn != nil {
		if p.lastStop != "end_turn" {
			p.turn.Set("turn.stalled", true)
			stalled = true
		}
		p.closeTurn(p.last)
	}
	if !p.lastTurnEnd.IsZero() && t.After(p.lastTurnEnd) {
		w := p.span("wait:"+t.Format(time.RFC3339Nano), "", KindWait, p.lastTurnEnd, p.root)
		w.End = t
		switch kind {
		case "human":
			w.Name = "waiting on you"
			if p.text && p.lastText != "" {
				w.Set("trace.asked", clip(lastParagraph(p.lastText), 200))
			}
		case "timer":
			w.Name = "waiting: timer"
		default:
			w.Name = "waiting: " + kind
		}
		w.Set("wait.kind", kind)
		if stalled {
			w.Set("wait.after_stall", true)
		}
	}
	p.turns++
	p.turn = p.span("turn:"+t.Format(time.RFC3339Nano), "", KindTurn, t, p.root)
	p.turn.Set("turn.number", int64(p.turns))
	p.turn.Set("turn.trigger", kind)
	p.turn.Name = "turn " + strconv.Itoa(p.turns)
	if p.text && text != "" {
		p.turn.Set("trace.prompt", clip(text, 200))
		p.turn.Name += ": " + clip(firstLine(text), 60)
	}
	p.lastStop = ""
	p.last = t
}

func (p *ccParser) closeTurn(t time.Time) {
	if t.Before(p.turn.Start) {
		t = p.turn.Start
	}
	p.turn.End = t
	p.lastTurnEnd = t
	p.turn = nil
}

func (p *ccParser) finish() {
	if p.turn != nil {
		p.turn.Set("turn.unfinished", true)
		p.closeTurn(p.last)
	}
	for _, s := range p.tools {
		s.End = p.last
		s.Set("tool.unfinished", true)
	}
	// A background command with no completion notice was killed with the
	// session (a crash, a restart). End it where the conversation went quiet.
	for _, s := range p.bg {
		s.End = p.quietAfter(s.Start)
		s.Set("tool.unfinished", true)
	}
	if n := p.root.Str("agent.name"); n != "" {
		p.root.Name = n
	}
	rest := p.c.Spans[1:]
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].Start.Before(rest[j].Start) })
}

func jsonString(raw json.RawMessage) string {
	var s string
	json.Unmarshal(raw, &s)
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastParagraph(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n\n"); i >= 0 {
		return s[i+2:]
	}
	return s
}
