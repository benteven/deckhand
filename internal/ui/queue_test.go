package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/benteven/deckhand/internal/store"
)

func TestQueueMovesOnEnd(t *testing.T) {
	m := &model{saver: &stateSaver{}, state: store.State{Queues: map[string][]string{"erp": {"a", "b"}}}}
	m.dropFromQueues("a")
	assert.Equal(t, []string{"b"}, m.state.Queues["erp"])
}
