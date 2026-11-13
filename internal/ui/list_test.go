package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/benteven/deckhand/internal/deck"
)

func TestDownSkipsSubsessions(t *testing.T) {
	m := &model{want: &slotWant{}, active: []deck.Row{{SessionID: "p"}, {SessionID: "c", Parent: "p"}, {SessionID: "x"}}}
	m.key(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.Equal(t, "x", m.selected)
	m.key(tea.KeyPressMsg{Code: 'k', Text: "k"})
	assert.Equal(t, "p", m.selected)
	m.key(tea.KeyPressMsg{Code: 'J', Text: "J"})
	assert.Equal(t, "c", m.selected)
	m.key(tea.KeyPressMsg{Code: tea.KeyUp})
	assert.Equal(t, "p", m.selected)
}
