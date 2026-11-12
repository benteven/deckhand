package ui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/tasks"
)

func TestNightlyDue(t *testing.T) {
	at := func(s string) time.Time { t, _ := time.ParseInLocation("2006-01-02 15:04", s, time.Local); return t }
	assert.False(t, nightlyDue("2026-11-12", at("2026-11-13 03:59")))
	assert.True(t, nightlyDue("2026-11-12", at("2026-11-13 04:00")))
	assert.True(t, nightlyDue("2026-11-11", at("2026-11-13 09:30")))
	assert.False(t, nightlyDue("2026-11-13", at("2026-11-13 23:00")))
}

func TestRemapFollowsTasks(t *testing.T) {
	m := &model{
		state: store.State{Projects: []string{"p1"}, Models: map[string]string{"s1": "opus"}},
		tasks: []tasks.Task{{ID: "t1", Project: "p1", Session: "s1"}},
	}
	m.remapSubs("s1", "s2")
	m.remapSubs("p1", "p2")
	assert.Equal(t, "p2", m.tasks[0].Project)
	assert.Equal(t, "s2", m.tasks[0].Session)
	assert.Equal(t, map[string]string{"s2": "opus"}, m.state.Models)
	assert.Equal(t, []string{"p2"}, m.state.Projects)
}
