package ctl

import (
	"errors"
	"os"
	"path/filepath"
)

func WriteInstructions(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "instructions.md"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, werr := f.WriteString(Instructions)
	return errors.Join(werr, f.Close())
}
