package deck

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

func projectDir(cwd string) string {
	b := []byte(cwd)
	for i, c := range b {
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

func CopyTranscript(configDir, fromCwd, toCwd, id string) error {
	dir := filepath.Join(configDir, "projects", projectDir(toCwd))
	to := filepath.Join(dir, id+".jsonl")
	if _, err := os.Lstat(to); err == nil {
		return nil
	}
	src, err := os.Open(filepath.Join(configDir, "projects", projectDir(fromCwd), id+".jsonl"))
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(to)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(to)
		return err
	}
	return nil
}
