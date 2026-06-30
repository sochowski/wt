package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// legacy fields recognized in .status files.
var legacyFields = map[string]bool{
	"status": true, "message": true, "repo": true, "branch": true,
	"wt_path": true, "pr": true, "agent": true, "opencode_config": true,
}

// statusUnescape reverses bin/wt's status_escape: backslash-escaped spaces and
// backslashes. Order matches load_status_file exactly (spaces first, then
// backslashes) so round-trips are faithful.
func statusUnescape(v string) string {
	v = strings.ReplaceAll(v, `\ `, " ")
	v = strings.ReplaceAll(v, `\\`, `\`)
	return v
}

// parseStatusFile reads a legacy <name>.status file into a field map.
func parseStatusFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fields := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		key, val, ok := strings.Cut(line, "=")
		if !ok || !legacyFields[key] {
			continue
		}
		fields[key] = statusUnescape(val)
	}
	return fields, sc.Err()
}

// Migrate imports every <name>.status file in dir into the store. Idempotent:
// re-running upserts the same rows. Returns the number of files imported.
func (s *Store) Migrate(dir string) (int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.status"))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, path := range matches {
		fields, err := parseStatusFile(path)
		if err != nil {
			return n, err
		}
		name := strings.TrimSuffix(filepath.Base(path), ".status")
		if _, err := s.Set(name, fields); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
