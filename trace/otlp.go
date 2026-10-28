package main

// Spans are stored as OTLP JSON: the encoding of OpenTelemetry's trace
// protocol, one file per conversation. An OTLP backend (Braintrust, Jaeger,
// Tempo) accepts the file's body as is at its /v1/traces endpoint.

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"time"
)

type otlpFile struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource struct {
		Attributes []otlpKV `json:"attributes"`
	} `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpScopeSpans struct {
	Scope struct {
		Name string `json:"name"`
	} `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpSpan struct {
	TraceID      string   `json:"traceId"`
	SpanID       string   `json:"spanId"`
	ParentSpanID string   `json:"parentSpanId,omitempty"`
	Name         string   `json:"name"`
	Kind         int      `json:"kind"`
	Start        string   `json:"startTimeUnixNano"`
	End          string   `json:"endTimeUnixNano"`
	Attributes   []otlpKV `json:"attributes"`
	Status       *struct {
		Code int `json:"code"`
	} `json:"status,omitempty"`
}

type otlpKV struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	String *string  `json:"stringValue,omitempty"`
	Int    *string  `json:"intValue,omitempty"` // int64 is a string in OTLP JSON
	Double *float64 `json:"doubleValue,omitempty"`
	Bool   *bool    `json:"boolValue,omitempty"`
}

const (
	otlpKindInternal = 1
	otlpKindClient   = 3
	otlpStatusError  = 2
)

func toOTLP(c *Conversation) *otlpFile {
	var rs otlpResourceSpans
	rs.Resource.Attributes = kvs(map[string]any{
		"service.name": "wt-trace/" + c.Harness, "trace.source": c.Source,
	})
	var ss otlpScopeSpans
	ss.Scope.Name = "wt-trace"
	for _, s := range c.Spans {
		o := otlpSpan{
			TraceID: s.TraceID, SpanID: s.ID, ParentSpanID: s.Parent, Name: s.Name, Kind: otlpKindInternal,
			Start: nanos(s.Start), End: nanos(s.End), Attributes: kvs(s.Attrs),
		}
		if s.Kind() == KindModel {
			o.Kind = otlpKindClient
		}
		if s.Error {
			o.Status = &struct {
				Code int `json:"code"`
			}{otlpStatusError}
		}
		ss.Spans = append(ss.Spans, o)
	}
	rs.ScopeSpans = []otlpScopeSpans{ss}
	return &otlpFile{ResourceSpans: []otlpResourceSpans{rs}}
}

func fromOTLP(f *otlpFile) *Conversation {
	c := &Conversation{}
	for _, rs := range f.ResourceSpans {
		res := attrs(rs.Resource.Attributes)
		c.Source = str(res["trace.source"])
		for _, ss := range rs.ScopeSpans {
			for _, o := range ss.Spans {
				s := &Span{TraceID: o.TraceID, ID: o.SpanID, Parent: o.ParentSpanID, Name: o.Name,
					Start: unnanos(o.Start), End: unnanos(o.End), Attrs: attrs(o.Attributes)}
				s.Error = o.Status != nil && o.Status.Code == otlpStatusError
				c.Spans = append(c.Spans, s)
			}
		}
	}
	if len(c.Spans) > 0 {
		c.Harness, c.ID = c.Root().Str("trace.harness"), c.Root().Str("session.id")
	}
	return c
}

func writeOTLP(path string, c *Conversation) error {
	b, err := json.Marshal(toOTLP(c))
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readOTLP(path string) (*Conversation, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f otlpFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return fromOTLP(&f), nil
}

func kvs(m map[string]any) []otlpKV {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]otlpKV, 0, len(m))
	for _, k := range keys {
		var v otlpValue
		switch x := m[k].(type) {
		case string:
			v.String = &x
		case bool:
			v.Bool = &x
		case float64:
			v.Double = &x
		case int, int64:
			s := strconv.FormatInt(num(x), 10)
			v.Int = &s
		default:
			continue
		}
		out = append(out, otlpKV{k, v})
	}
	return out
}

func attrs(kv []otlpKV) map[string]any {
	m := make(map[string]any, len(kv))
	for _, a := range kv {
		switch v := a.Value; {
		case v.String != nil:
			m[a.Key] = *v.String
		case v.Int != nil:
			n, _ := strconv.ParseInt(*v.Int, 10, 64)
			m[a.Key] = n
		case v.Double != nil:
			m[a.Key] = *v.Double
		case v.Bool != nil:
			m[a.Key] = *v.Bool
		}
	}
	return m
}

func nanos(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

func unnanos(s string) time.Time {
	n, _ := strconv.ParseInt(s, 10, 64)
	return time.Unix(0, n).UTC()
}
