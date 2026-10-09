package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type boundedEvidenceInventory struct{ bytes.Buffer }

func (b *boundedEvidenceInventory) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 1024*1024 {
		return 0, errors.New("bounded durable evidence inventory exceeded")
	}
	return b.Buffer.Write(data)
}

// Same fail-closed v1 coverage/limits as the host, including ignored files.
// Refuse unsupported checkout sizes BEFORE spending child/wake admission.
func preflightDurableEvidence(cwd string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", cwd, "ls-files", "-z", "--cached", "--others")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	inventory := &boundedEvidenceInventory{}
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
		if len(paths) > 4096 {
			return errors.New("durable v1 task checkout exceeds 4096 evidence paths; no child admitted")
		}
		if filepath.IsAbs(path) || strings.HasPrefix(path, "..") {
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
			if info.Size() > 16*1024*1024 || total > 64*1024*1024 {
				return errors.New("durable v1 task checkout exceeds bounded evidence bytes; no child admitted")
			}
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return errors.New("unsupported durable evidence path type")
		}
	}
	return nil
}
