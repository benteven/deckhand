package deck

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodexLaunch(t *testing.T) {
	t.Setenv("DECKHAND_CODEX", "/bin/codex")
	o := Ops{Home: t.TempDir(), Exe: "/tmp/my deck'hand"}
	args, err := o.codexArgs(Launch{SessionID: "d1", Resume: true, Thread: "th1", Model: "gpt-5.5", Prompt: "go on"})
	require.NoError(t, err)
	assert.Equal(t, []string{"env", "DECKHAND_SESSION=d1", "/bin/codex", "resume", "th1", "--model", "gpt-5.5", "-c", `sandbox_workspace_write.writable_roots=["` + filepath.Join(o.Home, "requests") + `"]`}, args[:9])
	assert.Equal(t, "go on", args[len(args)-1])
	assert.Contains(t, args, `hooks.Stop=[{hooks=[{type="command",command="'/tmp/my deck'\\''hand' hook codex",timeout=3}]}]`)
	assert.Contains(t, args[len(args)-2], `"/<session-flags>/config.toml:session_start:0:0"={trusted_hash="sha256:bee6e7fa0ebfad959ca6c5475b041fdd72e5c6d5c7222d92c937f6310a866372"}`)

	_, err = o.codexArgs(Launch{SessionID: "d1", Resume: true})
	assert.Error(t, err)
}
