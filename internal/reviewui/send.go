package reviewui

import (
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/live"
	"github.com/benteven/deckhand/internal/paths"
	"github.com/benteven/deckhand/internal/tmux"
)

type sentMsg struct {
	text                        string
	comment, waiting, delivered bool
	err                         error
}

func (m *model) send(text string, comment bool) tea.Cmd {
	t, id := m.t, m.id
	return func() tea.Msg {
		msg := deliver(t, id, text)
		msg.comment = comment
		return msg
	}
}

func deliver(t tmux.Client, id, text string) sentMsg {
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
	if asking(id) {
		return sentMsg{text: text, waiting: true}
	}
	if err := t.RunInput(text, "load-buffer", "-b", "deck-review", "-"); err != nil {
		return sentMsg{text: text, err: err}
	}
	if _, err := t.Run("paste-buffer", "-p", "-d", "-b", "deck-review", "-t", pane); err != nil {
		return sentMsg{text: text, err: err}
	}
	time.Sleep(150 * time.Millisecond)
	if asking(id) {
		return sentMsg{text: text, delivered: true, err: errors.New("Claude started asking; the text is in Claude, press enter there to send it")}
	}
	_, err = t.Run("send-keys", "-t", pane, "Enter")
	return sentMsg{text: text, err: err}
}

func asking(id string) bool {
	sessions, _ := live.Running(paths.ClaudeHome())
	for _, s := range sessions {
		if s.SessionID == id && s.Status == "waiting" {
			return true
		}
	}
	return false
}

func (m *model) sent(msg sentMsg) tea.Cmd {
	switch {
	case msg.waiting:
		m.status, m.statusErr = "waiting for Claude to finish asking", false
		retry := retryMsg{text: msg.text, comment: msg.comment}
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return retry })
	case msg.comment && msg.text == m.pending && (msg.err == nil || msg.delivered):
		m.pending = ""
		m.commenting = false
		m.input.SetValue("")
		m.input.Blur()
		m.status, m.statusErr = "sent", false
		if msg.err != nil {
			m.status, m.statusErr = msg.err.Error(), true
		}
	case msg.err != nil:
		if msg.comment && msg.text == m.pending {
			m.pending = ""
		}
		m.status, m.statusErr = msg.err.Error(), true
	}
	return nil
}
