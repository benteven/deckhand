package deck

import (
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

func CopyTranscript(fromDir, toDir, fromCwd, toCwd, id string) error {
	dir := filepath.Join(toDir, "projects", projectDir(toCwd))
	to := filepath.Join(dir, id+".jsonl")
	src, err := os.Open(filepath.Join(fromDir, "projects", projectDir(fromCwd), id+".jsonl"))
	if err != nil {
		return err
	}
	defer src.Close()
	if a, err := src.Stat(); err == nil {
		if b, err := os.Stat(to); err == nil && os.SameFile(a, b) {
			return nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+id+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), to)
}
