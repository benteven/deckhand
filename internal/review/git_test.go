package review

import (
	"os/exec"
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
	dir := t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "one")
	first := git("rev-parse", "HEAD")
	git("checkout", "-q", "-b", "feat")
	git("commit", "-q", "--allow-empty", "-m", "two")
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
}
