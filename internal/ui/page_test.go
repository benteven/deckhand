package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/events"
	"github.com/benteven/deckhand/internal/tasks"
	"github.com/benteven/deckhand/internal/web"
)

func TestAnswerRefusedOnMenu(t *testing.T) {
	m := &model{
		tasks:  []tasks.Task{{ID: "t1", Session: "s1", Status: "needs-you"}},
		active: []deck.Row{{SessionID: "s1", PaneID: "%1", Kind: deck.Live}},
		in:     deck.Inputs{Events: map[string]events.State{"s1": {Event: "Notification", Need: "permission_prompt"}}},
	}
	reply := make(chan error, 1)
	cmd := m.pageAction(web.Action{Kind: "answer", Task: "t1", Text: "yes", Reply: reply})
	assert.EqualError(t, <-reply, "answer the menu")
	assert.Nil(t, cmd)
	assert.Empty(t, m.typing)
	assert.Empty(t, m.pageAnswers)
}

func TestNotifyKeepsTextOutOfScript(t *testing.T) {
	title, need := `x" & (do shell script "touch /tmp/p") & "`, "-u critical"
	for _, goos := range []string{"darwin", "linux"} {
		_, args := notifyArgs(goos, title, need)
		assert.Equal(t, []string{title, need}, args[len(args)-2:], goos)
		for _, a := range args[:len(args)-2] {
			assert.NotContains(t, a, "touch", goos)
			assert.NotContains(t, a, "critical", goos)
		}
		assert.Equal(t, "--", args[len(args)-3], goos)
	}
}
