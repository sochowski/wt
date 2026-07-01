// wt-state — authoritative SQLite store for wt session state.
//
// Pure state layer: it never touches tmux. bash callers read rows from it and
// project tmux @wt-* options themselves. JSON field names match the legacy
// .status keys so existing jq-based consumers keep working.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		usage()
		return
	}

	st, err := Open(dbPath())
	if err != nil {
		fatalf("open db: %v", err)
	}
	defer st.Close()

	switch cmd {
	case "set":
		cmdSet(st, args)
	case "get":
		cmdGet(st, args)
	case "list":
		cmdList(st, args)
	case "counts":
		cmdCounts(st, args)
	case "delete":
		cmdDelete(st, args)
	case "migrate":
		cmdMigrate(st, args)
	default:
		fatalf("unknown command %q (try: set get list counts delete migrate)", cmd)
	}
}

func stateDir() string {
	if d := os.Getenv("WT_STATUS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "wt")
}

func dbPath() string {
	if p := os.Getenv("WT_DB"); p != "" {
		return p
	}
	dir := stateDir()
	// Ensure the directory exists; the DB file itself is created by the driver.
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "wt.db")
}

// cmdSet: wt-state set <name> [--status ...] [--message ...] ... [--is-master 0|1]
func cmdSet(st *Store, args []string) {
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	vals := map[string]*string{}
	for _, c := range columns {
		if c == "updated_at" || c == "status_changed_at" || c == "pr_state_checked_at" {
			continue // managed automatically, not settable
		}
		vals[c] = fs.String(flagName(c), "", "set "+c)
	}
	name, rest := popName(args, "set")
	fs.Parse(rest)

	// Only flags the user actually passed become updates; the rest are preserved.
	provided := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		col := colName(f.Name)
		if p, ok := vals[col]; ok {
			provided[col] = *p
		}
	})

	sess, err := st.Set(name, provided)
	if err != nil {
		fatalf("set: %v", err)
	}
	printJSON(sess)
}

// cmdGet: wt-state get <name> [--field <col>] [--json]
func cmdGet(st *Store, args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	field := fs.String("field", "", "print a single raw column value")
	_ = fs.Bool("json", false, "output JSON (default)")
	name, rest := popName(args, "get")
	fs.Parse(rest)
	sess, ok, err := st.Get(name)
	if err != nil {
		fatalf("get: %v", err)
	}
	if !ok {
		os.Exit(1) // absent: silent non-zero, mirrors `[[ -f file ]]` checks
	}
	if *field != "" {
		fmt.Println(fieldValue(sess, *field))
		return
	}
	printJSON(sess)
}

// cmdList: wt-state list [--master|--no-master] [--json]
func cmdList(st *Store, args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	onlyMaster := fs.Bool("master", false, "only the master row")
	noMaster := fs.Bool("no-master", false, "exclude the master row")
	sort := fs.String("sort", "recency", "order: recency (most-recently-active first) or name")
	_ = fs.Bool("json", false, "output JSON (default)")
	fs.Parse(args)

	mode := "all"
	switch {
	case *onlyMaster:
		mode = "only"
	case *noMaster:
		mode = "exclude"
	}
	sessions, err := st.List(mode, *sort)
	if err != nil {
		fatalf("list: %v", err)
	}
	if sessions == nil {
		sessions = []Session{}
	}
	printJSON(sessions)
}

// cmdCounts: wt-state counts [--json]
func cmdCounts(st *Store, args []string) {
	fs := flag.NewFlagSet("counts", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "output JSON instead of shell-friendly lines")
	fs.Parse(args)

	counts, err := st.Counts()
	if err != nil {
		fatalf("counts: %v", err)
	}
	if *asJSON {
		printJSON(counts)
		return
	}
	// shell-friendly: `working=N` lines, easy to eval or read with read.
	for _, k := range []string{"working", "idle", "input", "error"} {
		fmt.Printf("%s=%d\n", k, counts[k])
	}
}

// cmdDelete: wt-state delete <name>
func cmdDelete(st *Store, args []string) {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	name, rest := popName(args, "delete")
	fs.Parse(rest)
	if _, err := st.Delete(name); err != nil {
		fatalf("delete: %v", err)
	}
}

// cmdMigrate: wt-state migrate [--dir DIR]
func cmdMigrate(st *Store, args []string) {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dir := fs.String("dir", stateDir(), "directory of legacy .status files")
	fs.Parse(args)

	n, err := st.Migrate(*dir)
	if err != nil {
		fatalf("migrate: %v", err)
	}
	fmt.Fprintf(os.Stderr, "migrated %d session(s) from %s\n", n, *dir)
}

// fieldValue returns a column's raw string value for `get --field`.
func fieldValue(s Session, col string) string {
	switch col {
	case "name":
		return s.Name
	case "status":
		return s.Status
	case "message":
		return s.Message
	case "repo":
		return s.Repo
	case "branch":
		return s.Branch
	case "wt_path":
		return s.WtPath
	case "pr":
		return s.PR
	case "agent":
		return s.Agent
	case "opencode_config":
		return s.OpencodeConfig
	case "is_master":
		return fmt.Sprintf("%d", b2i(s.IsMaster))
	case "updated_at":
		return fmt.Sprintf("%d", s.UpdatedAt)
	case "status_changed_at":
		return fmt.Sprintf("%d", s.StatusChangedAt)
	case "pr_state":
		return s.PRState
	case "pr_state_checked_at":
		return fmt.Sprintf("%d", s.PRStateCheckedAt)
	default:
		fatalf("get: unknown field %q", col)
		return ""
	}
}

// flagName/colName map between column names (snake_case) and CLI flag names
// (kebab-case), e.g. wt_path <-> wt-path.
func flagName(col string) string { return replaceAll(col, '_', '-') }
func colName(flagN string) string { return replaceAll(flagN, '-', '_') }

func replaceAll(s string, from, to byte) string {
	b := []byte(s)
	for i := range b {
		if b[i] == from {
			b[i] = to
		}
	}
	return string(b)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fatalf("encode: %v", err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `wt-state — SQLite-backed wt session store

usage:
  wt-state set <name> [--status S] [--message M] [--repo R] [--branch B]
                      [--wt-path P] [--pr N] [--agent A]
                      [--opencode-config C] [--is-master 0|1]
                      [--pr-state merged|open|closed|draft|none]
  wt-state get <name> [--field COL | --json]
  wt-state list [--master | --no-master] [--sort recency|name] [--json]
  wt-state counts [--json]
  wt-state delete <name>
  wt-state migrate [--dir DIR]

env:
  WT_STATUS_DIR   state dir (default ~/.local/state/wt); holds wt.db
  WT_DB           override full path to the SQLite file
`)
}

// popName extracts the leading <name> positional, returning it and the
// remaining args for flag parsing. Go's flag package stops at the first
// non-flag arg, so the name must be peeled off before Parse.
func popName(args []string, cmd string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fatalf("%s: missing <name>", cmd)
	}
	return args[0], args[1:]
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "wt-state: "+format+"\n", a...)
	os.Exit(1)
}
