package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// validateDiffPosition accepts inert checkout-relative locations, including a
// deleted file whose nearest existing ancestor must still be inside the target.
func validateDiffPosition(root string, state ViewState) error {
	p := state.Diff
	if p == nil {
		return nil
	}
	if p.File == "" || filepath.IsAbs(p.File) || strings.ContainsAny(p.File, "\x00\r\n") || p.Line < 1 || p.Column < 0 || p.Topline < 1 || p.Leftcol < 0 {
		return errors.New("invalid diff position")
	}
	clean := filepath.Clean(p.File)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return errors.New("diff file escapes target")
	}
	parent := filepath.Join(root, clean)
	for {
		_, err := os.Lstat(parent)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return errors.New("missing diff target")
		}
		parent = next
	}
	_, err := containedPath(root, parent)
	return err
}
