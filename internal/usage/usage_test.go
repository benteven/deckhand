package usage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPick(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour).Unix()
	use := map[string]Usage{
		"a":    {Five: &Window{Used: 50, Resets: later}},
		"b":    {Five: &Window{Used: 20, Resets: later}, Week: &Window{Used: 60, Resets: later}},
		"c":    {Five: &Window{Used: 20, Resets: later}, Week: &Window{Used: 30, Resets: later}},
		"near": {Five: &Window{Used: 95, Resets: later}},
	}
	none := func(string) bool { return false }

	got, ok := Pick([]string{"a", "b", "c"}, use, none, now)
	assert.True(t, ok)
	assert.Equal(t, "c", got)

	got, _ = Pick([]string{"a", "b", "c"}, use, func(n string) bool { return n == "c" }, now)
	assert.Equal(t, "b", got)

	_, ok = Pick([]string{"near"}, use, none, now)
	assert.False(t, ok)
}

func TestStatusLine(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	seen := filepath.Join(dir, "seen.json")
	require.NoError(t, os.WriteFile(settings, []byte(`{"statusLine":{"type":"command","command":"cat > '`+seen+`'; echo mine"}}`), 0o600))
	in := `{"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1738425600}}}`

	var out bytes.Buffer
	require.NoError(t, StatusLine(filepath.Join(dir, "usage"), "work", settings, strings.NewReader(in), &out, time.Now()))

	assert.Equal(t, "mine\n", out.String())
	got, err := os.ReadFile(seen)
	require.NoError(t, err)
	assert.Equal(t, in, string(got))
	u, ok := Load(filepath.Join(dir, "usage"), "work")
	require.True(t, ok)
	assert.Equal(t, 23.5, u.Five.Used)
	assert.Nil(t, u.Week)
}
