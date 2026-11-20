package ui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
