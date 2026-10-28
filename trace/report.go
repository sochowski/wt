package main

// The report: where time went per agent, then detectors for habits that
// waste it. Each detector gives a total, a count and its worst examples.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A wait longer than this is you being away, not you being slow to answer.
const away = 2 * time.Hour

type example struct {
	conv *Conversation
	at   time.Time
	dur  time.Duration
	text string
}

type finding struct {
	title, about string
	total        time.Duration
	count        int
	extra        string // a non-time measure, e.g. tokens
	examples     []example
}

func (f *finding) add(c *Conversation, s *Span, d time.Duration, text string) {
	f.total += d
	f.count++
	f.examples = append(f.examples, example{c, s.Start, d, text})
}

var (
	rePolling = regexp.MustCompile(`\bsleep\s+[0-9]|\bwatch\s|inotifywait|tail\s+-[a-zA-Z]*f\b`)
	reFlock   = regexp.MustCompile(`\bflock\s+(?:-\S+\s+)*([^\s;&|]+)`)
	reTests   = regexp.MustCompile(`\b(pytest|go test|cargo test|vitest|jest|playwright test|(npm|pnpm|yarn|bun)( run)? test|make (test|check)[\w-]*)\b`)
)

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	within := fs.Duration("since", 7*24*time.Hour, "only conversations active in this window")
	q := first(parse(fs, args))
	all, err := loadAll()
	if err != nil {
		return err
	}
	convs := since(selectConvs(all, q), *within)
	if len(convs) == 0 {
		return fmt.Errorf("no conversations in that window")
	}
	var b strings.Builder
	report(&b, convs, *within)

	d := filepath.Join(stateDir(), "reports")
	os.MkdirAll(d, 0o700)
	out := filepath.Join(d, time.Now().Format("2006-01-02T1504")+".md")
	os.WriteFile(out, []byte(b.String()), 0o600)
	fmt.Print(b.String())
	fmt.Fprintf(os.Stderr, "\nsaved to %s\n", out)
	return nil
}

type stats struct {
	conv                         *Conversation
	active, model, tools, human  time.Duration
	idle                         time.Duration // waiting on a background task or a timer
	awayCount, turns             int
	outTokens, cacheWrite, input int64
}

func measure(c *Conversation) stats {
	st := stats{conv: c}
	var tools []*Span
	for _, s := range c.Spans[1:] {
		switch s.Kind() {
		case KindTurn:
			st.active += s.Dur()
			st.turns++
		case KindModel:
			st.model += s.Dur()
			st.outTokens += s.Int("gen_ai.usage.output_tokens")
			st.cacheWrite += s.Int("gen_ai.usage.cache_creation_input_tokens")
			st.input += s.Int("gen_ai.usage.input_tokens")
		case KindTool:
			if s.Str("gen_ai.tool.name") == "AskUserQuestion" { // the turn is waiting on you
				st.active -= s.Dur()
				st.human += s.Dur()
				continue
			}
			tools = append(tools, s)
		case KindWait:
			switch {
			case s.Dur() > away:
				st.awayCount++
			case s.Str("wait.kind") == "human":
				st.human += s.Dur()
			default:
				st.idle += s.Dur()
			}
		}
	}
	st.tools = union(tools)
	return st
}

// union is the wall-clock time covered by spans that may overlap.
func union(spans []*Span) time.Duration {
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start.Before(spans[j].Start) })
	var total time.Duration
	var end time.Time
	for _, s := range spans {
		if s.Start.After(end) {
			total += s.Dur()
			end = s.End
		} else if s.End.After(end) {
			total += s.End.Sub(end)
			end = s.End
		}
	}
	return total
}

func report(w io.Writer, convs []*Conversation, within time.Duration) {
	var sts []stats
	var sum stats
	for _, c := range convs {
		st := measure(c)
		sts = append(sts, st)
		sum.active += st.active
		sum.model += st.model
		sum.tools += st.tools
		sum.human += st.human
		sum.idle += st.idle
		sum.turns += st.turns
		sum.outTokens += st.outTokens
	}
	fmt.Fprintf(w, "# Where the time went: %d conversations, last %s\n\n", len(convs), human(within))
	fmt.Fprintf(w, "| | Time |\n|---|---|\n")
	fmt.Fprintf(w, "| Agents working (sum of turns) | %s |\n", human(sum.active))
	fmt.Fprintf(w, "| · model calls | %s (%s) |\n", human(sum.model), pct(sum.model, sum.active))
	fmt.Fprintf(w, "| · tool calls | %s (%s) |\n", human(sum.tools), pct(sum.tools, sum.active))
	fmt.Fprintf(w, "| Waiting on you (waits under %s) | %s |\n", human(away), human(sum.human))
	fmt.Fprintf(w, "| Idle on background tasks and timers | %s |\n\n", human(sum.idle))
	fmt.Fprintf(w, "Model and tool time can add up to more than the turn, because tool calls run in parallel and agents run at once.\n\n")

	sort.Slice(sts, func(i, j int) bool { return sts[i].active > sts[j].active })
	fmt.Fprintf(w, "## Agents by time working\n\n| Agent | Workspace | Working | Model | Tools | Waiting on you | Turns | Output tokens |\n|---|---|---|---|---|---|---|---|\n")
	for i, st := range sts {
		if i == 15 {
			break
		}
		r := st.conv.Root()
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %d | %s |\n", label(st.conv), r.Str("wt.workspace"),
			human(st.active), human(st.model), human(st.tools), human(st.human), st.turns, kilo(st.outTokens))
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "## Slowest tools\n\nBash is split by the command it ran.\n\n| Tool | Calls | Total | Longest |\n|---|---|---|---|\n")
	for i, t := range slowest(convs) {
		if i == 12 {
			break
		}
		fmt.Fprintf(w, "| `%s` | %d | %s | %s |\n", t.name, t.n, human(t.total), human(t.max))
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "## Habits, ranked by time\n\n")
	fs := detect(convs)
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].total > fs[j].total })
	for _, f := range fs {
		if f.count == 0 {
			continue
		}
		fmt.Fprintf(w, "### %s: %s over %d", f.title, human(f.total), f.count)
		if f.extra != "" {
			fmt.Fprintf(w, " · %s", f.extra)
		}
		fmt.Fprintf(w, "\n\n%s\n\n", f.about)
		sort.SliceStable(f.examples, func(i, j int) bool { return f.examples[i].dur > f.examples[j].dur })
		for i, e := range f.examples {
			if i == 3 {
				break
			}
			fmt.Fprintf(w, "- %s, %s · %s: `%s`\n", label(e.conv), e.at.Local().Format("Jan 02 15:04"), human(e.dur),
				strings.ReplaceAll(clip(firstLine(e.text), 120), "`", "'"))
		}
		fmt.Fprintln(w)
	}
}

func detect(convs []*Conversation) []*finding {
	polling := &finding{title: "Polling", about: "Shell loops and sleeps that wait for something. A background command with a completion notice, or `Monitor`, waits without holding the turn."}
	retries := &finding{title: "Retrying a failed call", about: "The same failing tool call made again in the same turn, without changing it first."}
	tests := &finding{title: "Test runs", about: "Time in test commands. Long ones are worth narrowing to the tests that changed, or running in the background."}
	rereads := &finding{title: "Re-reading files", about: "A file read again with no edit to it in between. The first read is still in context."}
	big := &finding{title: "Big tool outputs", about: "Results over ~20k tokens. They stay in context and make every later model call slower and costlier."}
	misses := &finding{title: "Prompt-cache misses", about: "Model calls that rewrote the cache instead of reading it. Grouped by cause below; a miss after an idle hour is the cache expiring."}
	compactions := &finding{title: "Compactions", about: "Context filled up and got summarised. Big outputs and long sessions cause these."}
	timeouts := &finding{title: "Commands that hit the foreground timeout", about: "The agent sat blocked until the timeout, then the command moved to the background anyway. Start long commands with `run_in_background` instead, and wait for the notice."}
	locks := &finding{title: "Queuing on a shared lock", about: "Time a `flock` command spent behind another agent holding the same lock. Narrower test runs release it sooner."}
	waits := &finding{title: "Waiting on you", about: "Agent finished a turn and waited for your reply (waits over 2h count as you being away and are left out). The quoted text is what it last said."}

	type lockUse struct {
		c *Conversation
		s *Span
	}
	held := map[string][]lockUse{}
	var bigTokens, rereadBytes int64
	missCause := map[string]int{}
	for _, c := range convs {
		lastFail := map[string]bool{}
		read := map[string]bool{}
		var prev *Span
		var prevEnd time.Time
		models := 0
		for _, s := range c.Spans[1:] {
			switch s.Kind() {
			case KindTurn:
				lastFail = map[string]bool{}
			case KindWait:
				if s.Str("wait.kind") == "human" && s.Dur() <= away {
					waits.add(c, s, s.Dur(), s.Str("trace.asked"))
				}
			case KindCompaction:
				compactions.add(c, s, s.Dur(), fmt.Sprintf("%s → %s tokens", kilo(s.Int("compaction.pre_tokens")), kilo(s.Int("compaction.post_tokens"))))
				prev = s
			case KindModel:
				models++
				w, r := s.Int("gen_ai.usage.cache_creation_input_tokens"), s.Int("gen_ai.usage.cache_read_input_tokens")
				if models > 1 && w > 20000 && w > r {
					cause := "other"
					switch {
					case prev != nil && prev.Kind() == KindCompaction:
						cause = "after a compaction"
					case !prevEnd.IsZero() && s.Start.Sub(prevEnd) > time.Hour:
						cause = "after an idle hour"
					}
					missCause[cause]++
					misses.add(c, s, s.Dur(), fmt.Sprintf("%s: wrote %s, read %s", cause, kilo(w), kilo(r)))
				}
				prevEnd = s.End
				prev = s
			case KindTool:
				cmd := s.Str("trace.command")
				name := s.Str("gen_ai.tool.name")
				key := name + "\x00" + cmd
				if name == "AskUserQuestion" {
					waits.add(c, s, s.Dur(), cmd)
				}
				if s.Int("tool.timed_out_ms") > 0 {
					timeouts.add(c, s, s.Dur(), cmd)
				}
				if m := reFlock.FindStringSubmatch(cmd); m != nil && name == "Bash" {
					held[m[1]] = append(held[m[1]], lockUse{c, s})
				}
				if s.Error && cmd != "" {
					if lastFail[key] {
						retries.add(c, s, s.Dur(), name+": "+cmd)
					}
					lastFail[key] = true
				}
				if name == "Bash" {
					if rePolling.MatchString(cmd) {
						polling.add(c, s, s.Dur(), cmd)
					}
					if reTests.MatchString(cmd) && !s.Bool("tool.background") {
						tests.add(c, s, s.Dur(), cmd)
					}
				}
				if fp := s.Str("tool.file_path"); fp != "" {
					switch name {
					case "Read":
						if read[fp] {
							rereads.add(c, s, s.Dur(), fp)
							rereadBytes += s.Int("tool.output_bytes")
						}
						read[fp] = true
					case "Edit", "Write", "MultiEdit", "NotebookEdit":
						delete(read, fp)
					}
				}
				if n := s.Int("tool.output_bytes"); n > 80000 {
					big.add(c, s, s.Dur(), name+": "+cmd)
					bigTokens += n / 4
				}
				prev = s
			}
		}
	}
	// For each use of a lock, the part of it that overlapped an earlier
	// holder from another conversation is time spent queuing.
	for _, uses := range held {
		sort.Slice(uses, func(i, j int) bool { return uses[i].s.Start.Before(uses[j].s.Start) })
		for i, u := range uses {
			var busy time.Time
			for _, o := range uses[:i] {
				if o.c != u.c && o.s.End.After(busy) {
					busy = o.s.End
				}
			}
			if busy.After(u.s.Start) {
				end := busy
				if u.s.End.Before(end) {
					end = u.s.End
				}
				locks.add(u.c, u.s, end.Sub(u.s.Start), u.s.Str("trace.command"))
			}
		}
	}
	big.extra = "~" + kilo(bigTokens) + " tokens"
	rereads.extra = "~" + kilo(rereadBytes/4) + " tokens"
	var causes []string
	for k, v := range missCause {
		causes = append(causes, fmt.Sprintf("%d %s", v, k))
	}
	sort.Strings(causes)
	misses.extra = strings.Join(causes, ", ")
	return []*finding{waits, timeouts, tests, locks, polling, compactions, misses, big, retries, rereads}
}

func human(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	return fmt.Sprintf("%.0fs", d.Seconds())
}

func pct(a, b time.Duration) string {
	if b == 0 {
		return "–"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(a)/float64(b))
}

func kilo(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

type toolTime struct {
	name       string
	n          int
	total, max time.Duration
}

var reLeading = regexp.MustCompile(`^(\s*(cd\s+[^;&]+|set\s+-[a-z]+|source\s+\S+|\.\s+\S+|export\s+\S+|[A-Z_]+=\S*)\s*(&&|;)\s*)+`)

// commandName is what a Bash command mostly ran: the first word after any
// cd, env and source preamble, plus the subcommand for runners.
func commandName(cmd string) string {
	cmd = reLeading.ReplaceAllString(cmd, "")
	f := strings.Fields(cmd)
	for len(f) > 0 && (strings.Contains(f[0], "=") || f[0] == "timeout" || f[0] == "time" || strings.HasPrefix(f[0], "-") || isNumber(f[0])) {
		f = f[1:]
	}
	if len(f) == 0 {
		return "?"
	}
	switch f[0] {
	case "uv", "go", "git", "make", "npm", "pnpm", "yarn", "bun", "gh", "rex", "docker", "wt", "cargo":
		if len(f) > 1 {
			if f[0] == "uv" && f[1] == "run" && len(f) > 2 {
				return "uv run " + f[2]
			}
			return f[0] + " " + f[1]
		}
	case "flock":
		return "flock … " + commandName(strings.Join(dropFlags(f[1:]), " "))
	}
	return f[0]
}

func dropFlags(f []string) []string {
	for len(f) > 0 && strings.HasPrefix(f[0], "-") {
		f = f[1:]
	}
	if len(f) > 0 {
		f = f[1:] // the lock file
	}
	return f
}

func isNumber(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' && r != 's' && r != 'm' {
			return false
		}
	}
	return s != ""
}

func slowest(convs []*Conversation) []toolTime {
	m := map[string]*toolTime{}
	for _, c := range convs {
		for _, s := range c.Spans[1:] {
			if s.Kind() != KindTool {
				continue
			}
			name := s.Str("gen_ai.tool.name")
			if name == "AskUserQuestion" {
				continue
			}
			if name == "Bash" {
				name = commandName(s.Str("trace.command"))
			}
			if i := strings.LastIndex(name, "__"); strings.HasPrefix(name, "mcp__") && i > 0 {
				name = strings.TrimPrefix(name[:i], "mcp__")
			}
			t := m[name]
			if t == nil {
				t = &toolTime{name: name}
				m[name] = t
			}
			t.n++
			t.total += s.Dur()
			t.max = max(t.max, s.Dur())
		}
	}
	var out []toolTime
	for _, t := range m {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].total > out[j].total })
	return out
}
