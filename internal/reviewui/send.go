package reviewui

import (
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/live"
	"github.com/benteven/deckhand/internal/paths"
)

type sentMsg struct {
	text    string
	waiting bool
	err     error
}

func (m *model) send(text string) tea.Cmd {
	t, id := m.t, m.id
	return func() tea.Msg {
		panes, err := t.Panes()
		if err != nil {
			return sentMsg{text: text, err: err}
		}
		pane := ""
		for _, p := range panes {
			if p.SessionID == id && !p.Dead {
				pane = p.ID
			}
		}
		if pane == "" {
			return sentMsg{text: text, err: errors.New("review session is not running")}
		}
		sessions, _ := live.Running(paths.ClaudeHome())
		for _, s := range sessions {
			if s.SessionID == id && s.Status == "waiting" {
				return sentMsg{text: text, waiting: true}
			}
		}
		if _, err := t.Run("set-buffer", "-b", "deck-review", text); err != nil {
			return sentMsg{text: text, err: err}
		}
		if _, err := t.Run("paste-buffer", "-p", "-d", "-b", "deck-review", "-t", pane); err != nil {
			return sentMsg{text: text, err: err}
		}
		time.Sleep(150 * time.Millisecond)
		_, err = t.Run("send-keys", "-t", pane, "Enter")
		return sentMsg{text: text, err: err}
	}
}

func (m *model) sent(msg sentMsg) tea.Cmd {
	switch {
	case msg.waiting:
		m.status, m.statusErr = "waiting for Claude to finish asking", false
		text := msg.text
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return retryMsg(text) })
	case msg.err != nil:
		m.status, m.statusErr = msg.err.Error(), true
	case msg.text == m.pending:
		m.pending = ""
		m.commenting = false
		m.input.SetValue("")
		m.input.Blur()
		m.status, m.statusErr = "sent", false
	}
	return nil
}
