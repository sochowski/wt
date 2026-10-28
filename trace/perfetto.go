package main

// Perfetto export: Chrome's trace event format, which ui.perfetto.dev opens
// in the browser without uploading it. Each conversation is a process; its
// turns, model calls, tool calls and background commands are threads, with
// spans that overlap (parallel tool calls) spread over extra threads.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

type chromeEvent struct {
	Name  string         `json:"name"`
	Cat   string         `json:"cat,omitempty"`
	Ph    string         `json:"ph"`
	Ts    float64        `json:"ts"`
	Dur   float64        `json:"dur,omitempty"`
	Pid   int            `json:"pid"`
	Tid   int            `json:"tid"`
	Cname string         `json:"cname,omitempty"`
	Args  map[string]any `json:"args,omitempty"`
}

// Lanes, top to bottom; each kind gets as many threads as it needs.
var lanes = []struct {
	name  string
	kinds []string
}{
	{"turns", []string{KindTurn, KindWait}},
	{"model", []string{KindModel, KindCompaction}},
	{"tools", []string{KindTool}},
	{"background", []string{KindBackground}},
}

func writePerfetto(path string, convs []*Conversation) error {
	var ev []chromeEvent
	for i, c := range convs {
		pid := i + 1
		ev = append(ev,
			chromeEvent{Name: "process_name", Ph: "M", Pid: pid, Args: map[string]any{"name": label(c)}},
			chromeEvent{Name: "process_sort_index", Ph: "M", Pid: pid, Args: map[string]any{"sort_index": i}},
		)
		tid := 0
		for _, l := range lanes {
			var spans []*Span
			for _, s := range c.Spans[1:] {
				for _, k := range l.kinds {
					if s.Kind() == k {
						spans = append(spans, s)
					}
				}
			}
			for n, lane := range assignLanes(spans) {
				tid++
				name := l.name
				if n > 0 {
					name += " " + fmt.Sprint(n+1)
				}
				ev = append(ev,
					chromeEvent{Name: "thread_name", Ph: "M", Pid: pid, Tid: tid, Args: map[string]any{"name": name}},
					chromeEvent{Name: "thread_sort_index", Ph: "M", Pid: pid, Tid: tid, Args: map[string]any{"sort_index": tid}},
				)
				for _, s := range lane {
					ev = append(ev, chromeEvent{
						Name: s.Name, Cat: s.Kind(), Ph: "X", Pid: pid, Tid: tid,
						Ts: micros(s.Start), Dur: max(micros(s.End)-micros(s.Start), 1),
						Cname: color(s), Args: eventArgs(s),
					})
				}
			}
		}
	}
	b, err := json.Marshal(map[string]any{"traceEvents": ev, "displayTimeUnit": "ms"})
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// assignLanes packs spans into as few non-overlapping rows as it can.
func assignLanes(spans []*Span) [][]*Span {
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	var out [][]*Span
	var ends []time.Time
	for _, s := range spans {
		placed := false
		for i := range out {
			if !s.Start.Before(ends[i]) {
				out[i] = append(out[i], s)
				ends[i] = s.End
				placed = true
				break
			}
		}
		if !placed {
			out = append(out, []*Span{s})
			ends = append(ends, s.End)
		}
	}
	return out
}

func label(c *Conversation) string {
	r := c.Root()
	name := r.Str("agent.name")
	if name == "" {
		name = r.Str("wt.workspace")
	}
	id := c.ID
	if len(id) > 8 {
		id = id[:8]
	}
	if name == "" {
		return id
	}
	return name + " (" + id + ")"
}

// Chrome's reserved colour names; Perfetto honours some and colours the rest
// by name, so the kinds stay apart either way.
func color(s *Span) string {
	switch s.Kind() {
	case KindWait:
		if s.Str("wait.kind") == "human" {
			return "terrible"
		}
		return "grey"
	case KindModel:
		return "good"
	case KindCompaction:
		return "bad"
	case KindBackground:
		return "yellow"
	}
	if s.Error {
		return "bad"
	}
	return ""
}

func eventArgs(s *Span) map[string]any {
	a := make(map[string]any, len(s.Attrs)+2)
	for k, v := range s.Attrs {
		a[k] = v
	}
	a["duration"] = s.Dur().Round(time.Millisecond).String()
	if s.Error {
		a["error"] = true
	}
	return a
}

func micros(t time.Time) float64 { return float64(t.UnixNano()) / 1e3 }
