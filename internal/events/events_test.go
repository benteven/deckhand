package events

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaiting(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	steps := []struct {
		event   string
		waiting bool
	}{
		{"UserPromptSubmit", false},
		{"Notification", true},
		{"PostToolUse", false},
		{"Stop", true},
		{"UserPromptSubmit", false},
	}
	for i, st := range steps {
		require.NoError(t, Record(dir, "s1", st.event, now.Add(time.Duration(i)*time.Second)))
		s, err := Read(dir, "s1")
		require.NoError(t, err)
		assert.Equal(t, st.waiting, s.Waiting(), st.event)
	}
	s, _ := Read(dir, "s1")
	assert.Equal(t, now.Add(4*time.Second).Unix(), s.PromptAt.Unix())
}

func TestNotificationNeed(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	require.NoError(t, RunHook(dir, strings.NewReader(`{"session_id":"s1","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission to use Bash"}`), now))
	s, err := Read(dir, "s1")
	require.NoError(t, err)
	assert.True(t, s.Menu())
	assert.Equal(t, "Claude needs your permission to use Bash", s.Message)

	require.NoError(t, RunHook(dir, strings.NewReader(`{"session_id":"s1","hook_event_name":"UserPromptSubmit"}`), now.Add(time.Second)))
	s, err = Read(dir, "s1")
	require.NoError(t, err)
	assert.False(t, s.Menu())
	assert.Empty(t, s.Message)
}
