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
