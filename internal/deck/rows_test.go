package deck

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/transcript"
)

func ids(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.SessionID
	}
	return out
}

func TestActiveOrder(t *testing.T) {
	t0 := time.Now()
	in := Inputs{
		State: store.State{Active: []store.ActiveEntry{
			{SessionID: "a", LastInteraction: t0},
			{SessionID: "b", LastInteraction: t0.Add(-time.Hour)},
			{SessionID: "c", LastInteraction: t0.Add(-2 * time.Hour)},
		}},
		Panes:     map[string]string{"a": "%1", "b": "%2", "c": "%3"},
		Summaries: map[string]transcript.Summary{"a": {LastUsed: t0.Add(3 * time.Minute)}, "b": {PromptedAt: t0.Add(time.Minute)}, "c": {PromptedAt: t0.Add(2 * time.Minute)}},
	}
	active, _ := Build(in)
	assert.Equal(t, []string{"c", "b", "a"}, ids(active))
}

func TestStaleRows(t *testing.T) {
	in := Inputs{
		State: store.State{
			Names:  map[string]string{"gone": "ledger import fix"},
			Active: []store.ActiveEntry{{SessionID: "gone", Cwd: "/x"}},
		},
		Files: []transcript.File{{SessionID: "old", Path: "/p/old.jsonl"}},
	}
	active, suspended := Build(in)
	require.Len(t, active, 1)
	assert.Equal(t, Stale, active[0].Kind)
	assert.Equal(t, "ledger import fix", active[0].Name)
	assert.Equal(t, []string{"old"}, ids(suspended))
}

func TestFilter(t *testing.T) {
	rows := []Row{
		{SessionID: "1", Name: "ledger import fix", Summary: transcript.Summary{PRs: []transcript.PRLink{{Number: 4598}}}},
		{SessionID: "2", Name: "ledger csv", Cwd: "/Users/dev/Projects/shop"},
		{SessionID: "3", Name: "risk", Summary: transcript.Summary{Branch: "ledger-tests"}},
	}
	assert.Equal(t, []string{"1", "2", "3"}, ids(Filter(rows, "Ledger")))
	assert.Equal(t, []string{"2"}, ids(Filter(rows, "ledger shop")))
	assert.Equal(t, []string{"1"}, ids(Filter(rows, "#4598")))
}

func TestAssistantAndSubsessionRows(t *testing.T) {
	t0 := time.Now()
	at := func(h int) time.Time { return t0.Add(-time.Duration(h) * time.Hour) }
	in := Inputs{
		State: store.State{
			Assistant: store.Assistant{ID: "a"},
			Subs: []store.Subsession{
				{ID: "c2", Parent: "p"},
				{ID: "c1", Parent: "p"},
				{ID: "o", Parent: "gone"},
			},
			Names: map[string]string{"gone": "old parent"},
			Active: []store.ActiveEntry{
				{SessionID: "a", LastInteraction: at(5)},
				{SessionID: "p", LastInteraction: at(2)},
				{SessionID: "x", LastInteraction: at(1)},
				{SessionID: "c1", LastInteraction: at(0)},
				{SessionID: "c2", LastInteraction: at(4)},
				{SessionID: "o", LastInteraction: at(3)},
			},
		},
		Panes: map[string]string{"a": "%1", "p": "%2", "x": "%3", "c1": "%4", "c2": "%5", "o": "%6"},
	}
	active, _ := Build(in)
	assert.Equal(t, []string{"a", "x", "p", "c2", "c1", "o"}, ids(active))
	assert.True(t, active[0].Assistant)
	assert.Equal(t, "p", active[3].Parent)
	assert.Equal(t, "old parent", active[5].Orphan)
	assert.Equal(t, []string{"p", "c2", "c1"}, ids(Family(active, "c1")))
	assert.Empty(t, Family(active, "x"))
}
