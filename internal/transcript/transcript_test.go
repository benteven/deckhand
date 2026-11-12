package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func prNumbers(prs []PRLink) []int {
	var out []int
	for _, p := range prs {
		out = append(out, p.Number)
	}
	return out
}

func copyFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/session.jsonl")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(path, data, 0o644))
	return path
}

func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.WriteString(text)
	require.NoError(t, err)
}

func TestSummary(t *testing.T) {
	r := NewReader(filepath.Join(t.TempDir(), "cache.json"))
	s, err := r.Summary("testdata/session.jsonl")
	require.NoError(t, err)
	assert.Equal(t, "Add a billing page", s.AITitle)
	assert.Equal(t, "billing", s.Title)
	assert.Equal(t, "develop", s.Branch)
	assert.Equal(t, "/Users/dev/Projects/shop", s.Cwd)
	assert.Equal(t, 205848, s.ContextTokens)
	assert.Equal(t, []int{4737, 4744}, prNumbers(s.PRs))
	assert.Equal(t, []string{"https://claude.ai/artifact/Billing1"}, s.Artifacts)
	assert.Equal(t, "Billing mockup", s.ArtifactTitles["https://claude.ai/artifact/Billing1"])
	assert.Equal(t, 20, s.ContextPercent())
}

func TestSummaryIncremental(t *testing.T) {
	path := copyFixture(t)
	cache := filepath.Join(t.TempDir(), "cache.json")
	r := NewReader(cache)
	_, err := r.Summary(path)
	require.NoError(t, err)
	appendRaw(t, path, `{"type":"ai-title","aiTitle":"Renamed`)
	s, _ := r.Summary(path)
	assert.Equal(t, "Add a billing page", s.AITitle)
	appendRaw(t, path, `"}`+"\n")
	require.NoError(t, r.Save())
	s, _ = NewReader(cache).Summary(path)
	assert.Equal(t, "Renamed", s.AITitle)
}

func TestIndexSearch(t *testing.T) {
	path := copyFixture(t)
	x := NewIndex()
	s, ok := x.Search(path, []string{"account", "view"})
	assert.True(t, ok)
	assert.Equal(t, "Please look at the account view", s)
	_, ok = x.Search(path, []string{"typedesign1"})
	assert.False(t, ok, "tool output is not indexed")

	appendRaw(t, path, `{"type":"assistant","message":{"content":[{"type":"text","text":"The Invoice  table\nis ready"}]}}`+"\n")
	s, ok = x.Search(path, []string{"invoice"})
	assert.True(t, ok)
	assert.Equal(t, "The Invoice table is ready", s)
}

func TestSummaryLimit(t *testing.T) {
	path := copyFixture(t)
	r := NewReader(filepath.Join(t.TempDir(), "cache.json"))
	appendRaw(t, path, `{"type":"assistant","timestamp":"2026-10-29T15:31:32.208Z","message":{"model":"<synthetic>","content":[{"type":"text","text":"You've hit your weekly limit · resets Nov 1 at 12am (Europe/Oslo)"}]},"error":"rate_limit"}`+"\n")
	s, err := r.Summary(path)
	require.NoError(t, err)
	oslo, err := time.LoadLocation("Europe/Oslo")
	require.NoError(t, err)
	assert.True(t, s.Limit.Resets.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, oslo)), s.Limit.Resets)

	at := time.Date(2026, 10, 29, 16, 0, 0, 0, oslo)
	assert.True(t, resetTime("resets 3:30pm (Europe/Oslo)", at).Equal(time.Date(2026, 10, 30, 15, 30, 0, 0, oslo)))

	appendRaw(t, path, `{"type":"assistant","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"Back."}]}}`+"\n")
	s, _ = r.Summary(path)
	assert.Zero(t, s.Limit)
}

func TestListPrefersProjectCopyOverWorktreeCopy(t *testing.T) {
	home := t.TempDir()
	write := func(dir string, at time.Time) string {
		p := filepath.Join(home, "projects", dir, "s1.jsonl")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("{}\n"), 0o600))
		require.NoError(t, os.Chtimes(p, at, at))
		return p
	}
	now := time.Now()
	live := write("-repo", now.Add(-time.Hour))
	write("-repo--claude-worktrees-fix-1", now)
	files, err := List(home)
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, live, files[0].Path)
}
