package ctl

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCreateFromDefaultBranch(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	tmp := t.TempDir()
	origin, clone := filepath.Join(tmp, "origin"), filepath.Join(tmp, "clone")
	run(tmp, "init", "-q", "-b", "main", origin)
	run(origin, "commit", "-q", "--allow-empty", "-m", "A")
	a := run(origin, "rev-parse", "HEAD")
	run(tmp, "clone", "-q", origin, clone)
	run(clone, "checkout", "-q", "-b", "other")
	run(clone, "commit", "-q", "--allow-empty", "-m", "B")

	_, _, branch, err := CreateFrom(clone, "t1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := run(clone, "rev-parse", "refs/heads/"+branch); branch != "t1" || got != a {
		t.Fatalf("branch %s at %s, want t1 at %s", branch, got, a)
	}
}
