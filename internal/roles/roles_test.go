package roles

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	write(t, filepath.Join(home, "roles", "tester.md"), "---\nname: tester\nmodel: sonnet\n---\nTest it.\n")
	if r, err := Find(home, repo, "tester"); err != nil || r.Model != "sonnet" || r.Body != "Test it." {
		t.Fatalf("home role = %+v, %v", r, err)
	}
	write(t, filepath.Join(repo, ".claude", "agents", "tester.md"), "---\nname: tester\nmodel: opus\ndeckhand: role\n---\nRepo test.\n")
	if r, err := Find(home, repo, "tester"); err != nil || r.Model != "opus" {
		t.Fatalf("repo role = %+v, %v", r, err)
	}
	write(t, filepath.Join(repo, ".claude", "agents", "reviewer.md"), "---\nname: reviewer\n---\nReview.\n")
	if _, err := Find(home, repo, "reviewer"); err == nil {
		t.Fatal("reviewer without deckhand: role was found")
	}
	if rs := List(home, repo); len(rs) != 1 || rs[0].Name != "tester" {
		t.Fatalf("list = %+v", rs)
	}
}
