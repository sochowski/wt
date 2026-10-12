package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type boundedEvidenceInventory struct {
	bytes.Buffer
	limit int
}

func (b *boundedEvidenceInventory) Write(data []byte) (int, error) {
	if b.Len()+len(data) > b.limit {
		return 0, errors.New("bounded durable evidence inventory exceeded")
	}
	return b.Buffer.Write(data)
}

// Same fail-closed v1 coverage/limits as the host, including ignored files.
// Refuse unsupported checkout sizes BEFORE spending child/wake admission.
func preflightDurableEvidence(cwd string) error { return preflightDurableEvidenceProfile(cwd, "") }
func preflightDurableEvidenceProfile(cwd, profile string) error {
	maxPaths, maxInventory, maxFile, maxBytes := 4096, 1024*1024, int64(16*1024*1024), int64(64*1024*1024)
	if profile == "whole-checkout-v2" {
		maxPaths = 65536
		maxInventory = 16 * 1024 * 1024
		maxFile = 64 * 1024 * 1024
		maxBytes = 512 * 1024 * 1024
	} else if profile != "" {
		return errors.New("unknown evidence profile")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", cwd, "ls-files", "-z", "--cached", "--others")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	inventory := &boundedEvidenceInventory{limit: maxInventory}
	cmd.Stdout = inventory
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("durable task checkout evidence inventory cannot be bounded; no child admitted")
	}
	paths := map[string]bool{}
	var total int64
	for _, path := range strings.Split(inventory.String(), "\x00") {
		if path == "" || paths[path] {
			continue
		}
		paths[path] = true
		if len(paths) > maxPaths {
			return fmt.Errorf("durable task checkout exceeds %d evidence paths for its explicit profile; no child admitted", maxPaths)
		}
		if filepath.IsAbs(path) || (path == ".." || strings.HasPrefix(path, "../")) {
			return errors.New("durable evidence path escapes checkout")
		}
		info, err := os.Lstat(filepath.Join(cwd, path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return errors.New("durable evidence path cannot be inspected")
		}
		if info.Mode().IsRegular() {
			total += info.Size()
			if info.Size() > maxFile || total > maxBytes {
				return errors.New("durable task checkout exceeds explicit-profile evidence byte bounds; no child admitted")
			}
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return errors.New("unsupported durable evidence path type")
		}
	}
	return nil
}
