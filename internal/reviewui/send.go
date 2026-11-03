package reviewui

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/live"
	"github.com/benteven/deckhand/internal/paths"
	"github.com/benteven/deckhand/internal/textclean"
	"github.com/benteven/deckhand/internal/tmux"
)

type outbound struct {
	text    string
	comment bool
	notes   int
}

type sentMsg struct {
	out                outbound
	waiting, delivered bool
	err                error
}

type retryMsg struct{ out outbound }

var (
	sendMu   sync.Mutex
	sendSeq  atomic.Int64
	menuLine = regexp.MustCompile(`(?m)^\s*(❯\s*)?1\.\s+Yes\b`)
)

func (m *model) send(o outbound) tea.Cmd {
	t, id := m.t, m.id
	return func() tea.Msg {
		sendMu.Lock()
		defer sendMu.Unlock()
		msg := deliver(t, id, textclean.Clean(o.text))
		msg.out = o
		return msg
	}
}

func claudePane(t tmux.Client, id string) (string, error) {
	panes, err := t.Panes()
	if err != nil {
		return "", err
	}
	for _, p := range panes {
		if p.SessionID == id && !p.Dead {
			return p.ID, nil
		}
	}
	return "", errors.New("review session is not running")
}

func deliver(t tmux.Client, id, text string) sentMsg {
	pane, err := claudePane(t, id)
	if err != nil {
		return sentMsg{err: err}
	}
	if asking(t, id, pane) {
		return sentMsg{waiting: true}
	}
	buffer := fmt.Sprintf("deck-review-%s-%d", id, sendSeq.Add(1))
	if err := t.RunInput(text, "load-buffer", "-b", buffer, "-"); err != nil {
		return sentMsg{err: err}
	}
	if _, err := t.Run("paste-buffer", "-p", "-d", "-b", buffer, "-t", pane); err != nil {
		return sentMsg{err: err}
	}
	time.Sleep(150 * time.Millisecond)
	if asking(t, id, pane) {
		return sentMsg{delivered: true, err: errors.New("Claude started asking; press enter in Claude to send your comment")}
	}
	_, err = t.Run("send-keys", "-t", pane, "Enter")
	return sentMsg{err: err}
}

func asking(t tmux.Client, id, pane string) bool {
	sessions, _ := live.Running(paths.ClaudeHome())
	for _, s := range sessions {
		if s.SessionID == id && s.Status == "waiting" {
			return true
		}
	}
	screen, err := t.Run("capture-pane", "-p", "-t", pane, "-S", "-20")
	return err == nil && menuLine.MatchString(screen)
}

func (m *model) sent(msg sentMsg) tea.Cmd {
	o := msg.out
	if msg.waiting {
		m.status, m.statusErr = "waiting for Claude to finish asking", false
		return tea.Tick(time.Second, func(time.Time) tea.Msg { return retryMsg{o} })
	}
	if m.status == "waiting for Claude to finish asking" {
		m.status = ""
	}
	if o.notes != 0 {
		if msg.err != nil && !msg.delivered {
			delete(m.asked, o.notes)
			m.status, m.statusErr = "notes request failed: "+msg.err.Error(), true
			return nil
		}
		return m.markAsked(o.notes)
	}
	if !o.comment || o.text != m.pending {
		return nil
	}
	if msg.err != nil && !msg.delivered {
		m.pending = ""
		m.status, m.statusErr = msg.err.Error(), true
		return nil
	}
	m.pending = ""
	m.commenting = false
	m.input.SetValue("")
	m.input.Blur()
	m.status, m.statusErr = "sent", false
	if msg.err != nil {
		m.status, m.statusErr = msg.err.Error(), true
	}
	return nil
}

func (m *model) focusClaude(key string) tea.Cmd {
	t, id := m.t, m.id
	return func() tea.Msg {
		pane, err := claudePane(t, id)
		if err != nil {
			return statusMsg{text: err.Error(), err: true}
		}
		if key != "" {
			t.Run("send-keys", "-t", pane, key)
		}
		t.Run("select-pane", "-t", pane)
		return nil
	}
}
