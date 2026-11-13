package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/events"
	"github.com/benteven/deckhand/internal/store"
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

func TestPageStage(t *testing.T) {
	m := &model{
		tasks: []tasks.Task{
			{ID: "review", Session: "s1", Project: "p", Dir: "/a", PR: "https://x/pull/1", Status: "working"},
			{ID: "testing", Session: "s2", Project: "p", Dir: "/b", PR: "https://x/pull/2", Status: "working"},
			{ID: "building", Session: "s3", Project: "p", Dir: "/c", Status: "needs-you"},
			{ID: "none", Status: "todo"},
		},
		prs: map[string]*prEntry{"https://x/pull/1": {state: "open"}, "https://x/pull/2": {state: "merged"}},
		state: store.State{
			Subs:  []store.Subsession{{ID: "r1", Parent: "p", Dir: "/a"}, {ID: "r2", Parent: "p", Dir: "/b"}, {ID: "r3", Parent: "p", Dir: "/c"}, {ID: "s3", Parent: "p", Dir: "/c"}},
			Roles: map[string]string{"r1": "tester", "r2": "tester", "r3": "tester", "s3": "task"},
		},
		active:    []deck.Row{{SessionID: "r1", Kind: deck.Live}, {SessionID: "r2", Kind: deck.Live}, {SessionID: "s3", Kind: deck.Live}},
		suspended: []deck.Row{{SessionID: "r3", Kind: deck.Suspended}},
	}
	got := map[string]string{}
	for _, tv := range m.pageView().Tasks {
		got[tv.ID] = tv.Stage
	}
	assert.Equal(t, map[string]string{"review": "review", "testing": "testing", "building": "building", "none": ""}, got)
}
