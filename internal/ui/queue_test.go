package ui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/store"
)

func TestQueueMovesOnEnd(t *testing.T) {
	m := &model{saver: &stateSaver{}, state: store.State{Queues: map[string][]string{"erp": {"a", "b"}}}}
	m.dropFromQueues("a")
	assert.Equal(t, []string{"b"}, m.state.Queues["erp"])
}

func TestQueueKeepsTurnForGrace(t *testing.T) {
	m := &model{saver: &stateSaver{}, state: store.State{Queues: map[string][]string{"erp": {"a", "b"}}}, active: []deck.Row{{SessionID: "b", Kind: deck.Live}}}
	start := time.Now()
	m.pruneQueues(start)
	m.pruneQueues(start.Add(time.Second))
	assert.Equal(t, []string{"a", "b"}, m.state.Queues["erp"])
	m.pruneQueues(start.Add(queueGrace))
	assert.Equal(t, []string{"b"}, m.state.Queues["erp"])
}
