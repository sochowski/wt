// install-hooks: wire wt-hook into each installed agent's own config format.
// Replaces the hand-coded per-agent blocks that used to live in install.sh
// (jq merges for Claude/Gemini, a TOML append for Codex, a symlink for
// opencode). Templates stay on disk under --template-dir so the opencode plugin
// can remain a live symlink into the checkout.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cmdAgentsInstallHooks: wt-state agents install-hooks --template-dir DIR [--home DIR]
func cmdAgentsInstallHooks(args []string) {
	fs := flag.NewFlagSet("agents install-hooks", flag.ExitOnError)
	templateDir := fs.String("template-dir", "", "directory holding the hook templates (config/)")
	home := fs.String("home", "", "target home dir (default $HOME)")
	fs.Parse(args)

	if *templateDir == "" {
		fatalf("install-hooks: --template-dir is required")
	}
	h := homeDir(*home)

	for _, a := range registry {
		if !a.isInstalled() {
			fmt.Printf("  %s: not installed, skipping hooks\n", a.Name)
			continue
		}
		msg, err := installAgentHooks(a, *templateDir, h)
		if err != nil {
			// Non-fatal: one agent's failure shouldn't abort the others.
			fmt.Fprintf(os.Stderr, "  %s: hook install failed: %v\n", a.Name, err)
			continue
		}
		fmt.Printf("  %s: %s\n", a.Name, msg)
	}
}

// installAgentHooks applies one agent's HookSpec, returning a human-readable
// summary of what it did.
func installAgentHooks(a Agent, templateDir, home string) (string, error) {
	spec := a.Hook
	if spec.Template == "" {
		return "no hooks to install", nil
	}
	src := filepath.Join(templateDir, spec.Template)
	target := filepath.Join(home, spec.Target)

	switch spec.Format {
	case hookJSONMerge:
		return installJSONMerge(src, target)
	case hookTOMLAppend:
		return installTOMLAppend(src, target, spec.Marker)
	case hookSymlinkPlugin:
		return installSymlinkPlugin(src, target)
	default:
		return "", fmt.Errorf("unknown hook format %q", spec.Format)
	}
}

// installJSONMerge deep-merges the template into an existing JSON settings file,
// matching `jq -s '.[0] * .[1]'`: objects merge recursively, non-objects (incl.
// arrays) are replaced by the template's value.
func installJSONMerge(src, target string) (string, error) {
	tmpl, err := readJSON(src)
	if err != nil {
		return "", fmt.Errorf("read template: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}

	if _, err := os.Stat(target); err == nil {
		existing, err := readJSON(target)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", target, err)
		}
		merged := mergeJSON(existing, tmpl)
		if err := writeJSON(target, merged); err != nil {
			return "", err
		}
		return "merged hooks into " + target, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	if err := writeJSON(target, tmpl); err != nil {
		return "", err
	}
	return "created " + target, nil
}

// installTOMLAppend appends the template block to a TOML config unless the
// marker substring is already present (idempotent). Creates the file if absent.
func installTOMLAppend(src, target, marker string) (string, error) {
	block, err := os.ReadFile(src)
	if err != nil {
		return "", fmt.Errorf("read template: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}

	if existing, err := os.ReadFile(target); err == nil {
		if marker != "" && strings.Contains(string(existing), marker) {
			return "hooks already configured", nil
		}
		// Separate the appended block from prior content with a blank line.
		out := existing
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, '\n')
		out = append(out, block...)
		if err := os.WriteFile(target, out, 0o644); err != nil {
			return "", err
		}
		return "added notify hook to " + target, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	if err := os.WriteFile(target, block, 0o644); err != nil {
		return "", err
	}
	return "created " + target, nil
}

// installSymlinkPlugin points target at src. An existing symlink is replaced; a
// real file is backed up to .bak first.
func installSymlinkPlugin(src, target string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return "", err
	}

	if fi, err := os.Lstat(target); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(target); err != nil {
				return "", err
			}
		} else {
			if err := os.Rename(target, target+".bak"); err != nil {
				return "", err
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	if err := os.Symlink(abs, target); err != nil {
		return "", err
	}
	return "installed status plugin at " + target, nil
}

// mergeJSON recursively merges src into dst (jq `*` semantics): both objects →
// merge keys; otherwise src wins.
func mergeJSON(dst, src any) any {
	dm, dok := dst.(map[string]any)
	sm, sok := src.(map[string]any)
	if !dok || !sok {
		return src
	}
	out := make(map[string]any, len(dm)+len(sm))
	for k, v := range dm {
		out[k] = v
	}
	for k, sv := range sm {
		if dv, ok := out[k]; ok {
			out[k] = mergeJSON(dv, sv)
		} else {
			out[k] = sv
		}
	}
	return out
}

func readJSON(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return v, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
