package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAssistantTakesOne(t *testing.T) {
	s := State{Assistant: Assistant{ID: "a"}, Active: []ActiveEntry{{SessionID: "x", Num: 1}, {SessionID: "a", Num: 3}}}
	s.Number()
	assert.Equal(t, 2, s.Active[0].Num)
	assert.Equal(t, 1, s.Active[1].Num)
	assert.Equal(t, 3, s.Claim("y", 1))
}
