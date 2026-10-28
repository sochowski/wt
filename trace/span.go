package main

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Span kinds, stored in the trace.kind attribute.
const (
	KindConversation = "conversation"
	KindTurn         = "turn"       // from what started it (your message, a notification) to the final reply
	KindWait         = "wait"       // between turns: on you, on a background task, or on a timer
	KindModel        = "model"      // one model request, from sending it to the last block of the reply
	KindTool         = "tool"       // one tool call, from the call to its result
	KindBackground   = "background" // a background command, from launch to its completion notice
	KindCompaction   = "compaction"
)

// A Span is one timed piece of a conversation. Every adapter produces these,
// and everything downstream (OTLP, Perfetto, the report) reads only these.
type Span struct {
	TraceID, ID, Parent string
	Name                string
	Start, End          time.Time
	Error               bool
	Attrs               map[string]any
}

func (s *Span) Kind() string        { return str(s.Attrs["trace.kind"]) }
func (s *Span) Dur() time.Duration  { return s.End.Sub(s.Start) }
func (s *Span) Str(k string) string { return str(s.Attrs[k]) }
func (s *Span) Int(k string) int64  { return num(s.Attrs[k]) }
func (s *Span) Set(k string, v any) { s.Attrs[k] = v }
func (s *Span) Bool(k string) bool  { b, _ := s.Attrs[k].(bool); return b }

// A Conversation is one transcript: its root span first, then the rest in
// start order.
type Conversation struct {
	Harness string // claude, codex, pi, opencode
	ID      string // the harness's own session id
	Source  string // the transcript it came from
	Spans   []*Span
}

func (c *Conversation) Root() *Span { return c.Spans[0] }

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// Deterministic ids, so converting a transcript again gives the same spans,
// and a subagent's root can point at its parent's tool span by computing it.
func traceID(session string) string     { return hashHex("trace:"+session, 16) }
func spanID(session, key string) string { return hashHex(session+"\x00"+key, 8) }

func hashHex(s string, n int) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:n])
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
