package ui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/transcript"
)

func TestChooseAccount(t *testing.T) {
	now := time.Date(2026, 11, 20, 22, 0, 0, 0, time.UTC)
	logged := []string{"", "work"}
	limits := map[string]time.Time{"work": now.Add(time.Hour)}
	_, err := chooseAccount("work", logged, limits, now)
	assert.Error(t, err)
	_, err = chooseAccount("home", logged, limits, now)
	assert.Error(t, err)
	a, err := chooseAccount("default", logged, limits, now)
	assert.NoError(t, err)
	assert.Equal(t, "", a)
}

func TestAnsweredAfterLimit(t *testing.T) {
	seen := time.Date(2026, 11, 17, 13, 33, 0, 0, time.UTC)
	reply := deck.Row{Kind: deck.Live, Summary: transcript.Summary{Model: "claude-opus-5-5", LastUsed: seen.Add(time.Hour)}}
	assert.True(t, answered(reply, seen))
	assert.False(t, answered(reply, seen.Add(2*time.Hour)))
	limited := reply
	limited.Summary.Limit = transcript.Limit{At: seen.Add(time.Hour)}
	assert.False(t, answered(limited, seen))
}
