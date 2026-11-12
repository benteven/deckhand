package deck

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyTranscript(t *testing.T) {
	dir := t.TempDir()
	id := "11111111-2222-3333-4444-555555555555"
	from := filepath.Join(dir, "projects", "-a-b", id+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(from), 0o755))
	require.NoError(t, os.WriteFile(from, []byte("parent\n"), 0o644))

	require.NoError(t, CopyTranscript(dir, "/a/b", "/a/b/.claude/worktrees/x", id))
	to := filepath.Join(dir, "projects", "-a-b--claude-worktrees-x", id+".jsonl")
	got, err := os.ReadFile(to)
	require.NoError(t, err)
	assert.Equal(t, "parent\n", string(got))

	require.NoError(t, os.WriteFile(to, []byte("fork\n"), 0o644))
	require.NoError(t, CopyTranscript(dir, "/a/b", "/a/b/.claude/worktrees/x", id))
	got, err = os.ReadFile(to)
	require.NoError(t, err)
	assert.Equal(t, "fork\n", string(got))
}
