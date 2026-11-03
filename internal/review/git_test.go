package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFull(t *testing.T) {
	unified := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	assert.Equal(t, []Line{
		{Kind: Same, New: 1, Old: 1, Text: "a"},
		{Kind: Del, New: 0, Old: 2, Text: "b"},
		{Kind: Add, New: 2, Old: 0, Text: "B"},
		{Kind: Same, New: 3, Old: 3, Text: "c"},
	}, ParseFull(unified))
}

func TestResolve(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "one")
	first := git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "feat")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte("package f\n"), 0o644))
	git("add", "f.go")
	git("commit", "-q", "-m", "two")
	git("checkout", "-q", "main")

	pr := PR{Repo: "x/y", Base: "main", Head: "feat"}
	src, err := Resolve(dir, pr)
	require.NoError(t, err)
	assert.False(t, src.CheckedOut)
	assert.Equal(t, "feat", src.Head)
	assert.Equal(t, first, src.MergeBase)

	git("checkout", "-q", "feat")
	src, err = Resolve(dir, pr)
	require.NoError(t, err)
	assert.True(t, src.CheckedOut)

	_, err = Resolve(t.TempDir(), pr)
	assert.EqualError(t, err, "folder is not a checkout of x/y")

	git("checkout", "-q", "main")
	git("update-ref", "refs/remotes/origin/feat", "feat")
	git("branch", "-q", "-D", "feat")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dirty.go"), []byte("package dirty\n"), 0o644))
	git("remote", "add", "origin", "git@github.com:X/Y.git")
	src, err = Resolve(dir, pr)
	require.NoError(t, err)
	assert.Equal(t, "origin/feat", src.Head)
	assert.False(t, src.CheckedOut)
	files, err := Changed(src)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "f.go", files[0].Path)

	git("remote", "set-url", "origin", "https://github.com/x/other")
	_, err = Resolve(dir, pr)
	assert.EqualError(t, err, "folder is not a checkout of x/y")
}
