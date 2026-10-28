// wt-trace profiles agent conversations from their saved transcripts: a
// flame view per conversation in Perfetto, and a report of where time goes
// across all of them. Run as `wt trace`.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const usage = `usage: wt trace <command> [flags]

  convert            turn new or changed transcripts into spans (OTLP JSON)
  perfetto QUERY     write a Chrome trace of the matching conversations, for ui.perfetto.dev
  report [QUERY]     rank where time went and the habits that waste it
  ls [QUERY]         list converted conversations

QUERY matches a session id prefix, an agent name, or a wt workspace; a
workspace also takes in the subagents its coordinator launched.

Spans are kept in %s.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, stateDir())
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "convert":
		err = cmdConvert(args)
	case "perfetto":
		err = cmdPerfetto(args)
	case "report":
		err = cmdReport(args)
	case "ls":
		err = cmdLs(args)
	case "-h", "--help", "help":
		fmt.Printf(usage, stateDir())
	default:
		fmt.Fprintf(os.Stderr, "wt trace: unknown command %q\n\n"+usage, cmd, stateDir())
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "wt trace:", err)
		os.Exit(1)
	}
}

func stateDir() string {
	d := os.Getenv("XDG_STATE_HOME")
	if d == "" {
		d = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(d, "wt-trace")
}

// The index remembers each transcript's size and mtime, so convert only
// rereads what changed.
type indexEntry struct {
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
	Spans string    `json:"spans"`
	Text  bool      `json:"text"`
}

func cmdConvert(args []string) error {
	fs := flag.NewFlagSet("convert", flag.ExitOnError)
	noText := fs.Bool("no-text", false, "keep no prompt, command or file text in the spans, only metadata")
	force := fs.Bool("force", false, "reconvert everything")
	fs.Parse(args)

	dir := filepath.Join(stateDir(), "spans")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	idxPath := filepath.Join(stateDir(), "index.json")
	idx := map[string]indexEntry{}
	if b, err := os.ReadFile(idxPath); err == nil {
		json.Unmarshal(b, &idx)
	}

	type job struct {
		path string
		fi   os.FileInfo
	}
	var jobs []job
	for _, p := range claudeSources() {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		e, ok := idx[p]
		if !*force && ok && e.Size == fi.Size() && e.Mtime.Equal(fi.ModTime()) && e.Text == !*noText {
			continue
		}
		jobs = append(jobs, job{p, fi})
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var failed []string
	ch := make(chan job)
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				c, err := parseClaude(j.path, !*noText)
				if err == nil && len(c.Spans) < 2 {
					continue // nothing happened in it
				}
				out := filepath.Join(dir, "claude-"+c.ID+".otlp.json")
				if err == nil {
					err = writeOTLP(out, c)
				}
				mu.Lock()
				if err != nil {
					failed = append(failed, j.path+": "+err.Error())
				} else {
					idx[j.path] = indexEntry{j.fi.Size(), j.fi.ModTime(), out, !*noText}
				}
				mu.Unlock()
			}
		}()
	}
	start := time.Now()
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()

	b, _ := json.MarshalIndent(idx, "", " ")
	if err := os.WriteFile(idxPath, b, 0o600); err != nil {
		return err
	}
	fmt.Printf("converted %d transcripts in %s → %s\n", len(jobs)-len(failed), time.Since(start).Round(time.Millisecond), dir)
	for _, f := range failed {
		fmt.Fprintln(os.Stderr, "  failed:", f)
	}
	return nil
}

// loadAll reads every converted conversation.
func loadAll() ([]*Conversation, error) {
	paths, _ := filepath.Glob(filepath.Join(stateDir(), "spans", "*.otlp.json"))
	if len(paths) == 0 {
		return nil, fmt.Errorf("no spans yet; run `wt trace convert` first")
	}
	out := make([]*Conversation, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for i, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if c, err := readOTLP(p); err == nil && len(c.Spans) > 0 {
				out[i] = c
			}
		}()
	}
	wg.Wait()
	var convs []*Conversation
	for _, c := range out {
		if c != nil {
			convs = append(convs, c)
		}
	}
	sort.Slice(convs, func(i, j int) bool { return convs[i].Root().Start.Before(convs[j].Root().Start) })
	return convs, nil
}

// selectConvs picks the conversations a query names, plus the built-in
// subagents they launched.
func selectConvs(all []*Conversation, q string) []*Conversation {
	if q == "" {
		return all
	}
	picked := map[string]bool{}
	for _, c := range all {
		r := c.Root()
		if strings.HasPrefix(c.ID, q) || r.Str("agent.name") == q || r.Str("wt.workspace") == q || r.Str("wt.parent_workspace") == q {
			picked[c.ID] = true
		}
	}
	var out []*Conversation
	for _, c := range all {
		if picked[c.ID] || picked[c.Root().Str("trace.parent_session")] {
			out = append(out, c)
		}
	}
	// Coordinator first, then its tabs, each followed by its own subagents.
	order := func(c *Conversation) string {
		r := c.Root()
		key := r.Start.Format(time.RFC3339)
		if p := r.Str("trace.parent_session"); p != "" {
			for _, o := range out {
				if o.ID == p {
					key = o.Root().Start.Format(time.RFC3339) + "/" + key
				}
			}
		}
		if r.Str("wt.parent_workspace") == "" && r.Str("trace.parent_session") == "" {
			return "0/" + key
		}
		return "1/" + key
	}
	sort.SliceStable(out, func(i, j int) bool { return order(out[i]) < order(out[j]) })
	return out
}

func since(convs []*Conversation, d time.Duration) []*Conversation {
	if d == 0 {
		return convs
	}
	cut := time.Now().Add(-d)
	var out []*Conversation
	for _, c := range convs {
		if c.Root().End.After(cut) {
			out = append(out, c)
		}
	}
	return out
}

func cmdPerfetto(args []string) error {
	fs := flag.NewFlagSet("perfetto", flag.ExitOnError)
	out := fs.String("o", "", "output file (default: <state>/perfetto/<query>.json)")
	pos := parse(fs, args)
	if len(pos) != 1 {
		return fmt.Errorf("usage: wt trace perfetto QUERY [-o file]")
	}
	q := pos[0]
	all, err := loadAll()
	if err != nil {
		return err
	}
	convs := selectConvs(all, q)
	if len(convs) == 0 {
		return fmt.Errorf("no conversation matches %q (try `wt trace ls`)", q)
	}
	if *out == "" {
		d := filepath.Join(stateDir(), "perfetto")
		os.MkdirAll(d, 0o700)
		*out = filepath.Join(d, strings.ReplaceAll(q, "/", "_")+".json")
	}
	if err := writePerfetto(*out, convs); err != nil {
		return err
	}
	host, _ := os.Hostname()
	fmt.Printf("%d conversations → %s\n", len(convs), *out)
	fmt.Printf("open https://ui.perfetto.dev and drag it in; from your Mac:\n  scp %s:%s .\n", host, *out)
	return nil
}

func cmdLs(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	within := fs.Duration("since", 0, "only conversations active in this window, e.g. 168h")
	q := first(parse(fs, args))
	all, err := loadAll()
	if err != nil {
		return err
	}
	for _, c := range since(selectConvs(all, q), *within) {
		r := c.Root()
		ws := r.Str("wt.workspace")
		if p := r.Str("wt.parent_workspace"); p != "" {
			ws += " ← " + p
		}
		fmt.Printf("%s  %-8s  %8s  %-40s  %s\n", r.Start.Local().Format("Jan 02 15:04"), c.ID[:min(8, len(c.ID))],
			r.Dur().Round(time.Minute), clip(r.Str("agent.name"), 40), ws)
	}
	return nil
}

// parse lets flags come before or after the positional arguments.
func parse(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		fs.Parse(args)
		if fs.NArg() == 0 {
			return pos
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func first(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return a[0]
}
