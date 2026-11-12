package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/benteven/deckhand/internal/deck"
)

const (
	handoffTimeout = 15 * time.Minute
	clearWait      = 5 * time.Second
)

type handoffDialog struct{ row deck.Row }

type pendingHandoff struct {
	id, name, file string
	sent, cleared  time.Time
}

func (d *handoffDialog) view(w, h int) string {
	body := []string{
		normal.Render(ansi.Truncate(`Hand off "`+d.row.Name+`"? (y/n)`, w, "…")),
		"",
		gray.Render(ansi.Truncate("Claude writes its state to a file.", w, "…")),
		gray.Render(ansi.Truncate("Then the deck runs /clear and", w, "…")),
		gray.Render(ansi.Truncate("Claude reads the file back.", w, "…")),
	}
	return page(body, gray.Render(ansi.Truncate("y hand off  n cancel", w, "")), h)
}

func (m *model) startHandoff(r deck.Row) tea.Cmd {
	if m.mode != modeActive || r.Kind != deck.Live || m.resuming[r.SessionID] {
		return nil
	}
	if why := m.handoffRefusal(r); why != "" {
		m.hint = why
		return nil
	}
	m.handoff = &handoffDialog{row: r}
	return m.zoom(true)
}

func (m *model) handoffRefusal(r deck.Row) string {
	switch {
	case m.handoffs[r.PaneID] != nil:
		return "A handoff is already running for this session"
	case r.Busy:
		return "The session is busy. Try again when it is idle"
	}
	return ""
}

func (m *model) handoffKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		r := m.handoff.row
		m.handoff = nil
		prompt, err := m.beginHandoff(r)
		if err != nil {
			m.err = err
			return m.zoom(false)
		}
		return tea.Sequence(m.zoom(false), m.typeIn(r.PaneID, prompt))
	case "n", "esc":
		m.handoff = nil
		return m.zoom(false)
	}
	return nil
}

func (m *model) beginHandoff(r deck.Row) (string, error) {
	file := filepath.Join(m.home, "handoffs", r.SessionID+".md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	if m.handoffs == nil {
		m.handoffs = map[string]*pendingHandoff{}
	}
	m.handoffs[r.PaneID] = &pendingHandoff{id: r.SessionID, name: r.Name, file: file, sent: time.Now()}
	m.hint = "Claude writes the handoff, then the deck clears"
	return fmt.Sprintf("Write a handoff note to %s so a fresh session can pick up this work: the goal, what is done, what is left, key files and commands, decisions made, and open questions. Replace the file if it exists. Write only that file, then stop.", file), nil
}

func (m *model) advanceHandoffs(now time.Time) tea.Cmd {
	var cmds []tea.Cmd
	for pane, h := range m.handoffs {
		r, ok := m.rowInPane(pane)
		switch {
		case h.cleared.IsZero() && (!ok || now.Sub(h.sent) > handoffTimeout):
			delete(m.handoffs, pane)
			m.err = fmt.Errorf("handoff for %q stopped before the note was written", h.name)
		case h.cleared.IsZero():
			if r.Busy || !written(h.file, h.sent) {
				continue
			}
			h.cleared = now
			m.hint = "Clearing " + h.name
			cmds = append(cmds, m.typeIn(pane, "/clear"))
		case (ok && r.SessionID != h.id) || now.Sub(h.cleared) > clearWait:
			delete(m.handoffs, pane)
			m.hint = ""
			cmds = append(cmds, m.typeIn(pane, fmt.Sprintf("Read %s. It is the handoff note from before /clear. Continue the work from it.", h.file)))
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) rowInPane(pane string) (deck.Row, bool) {
	for _, r := range m.active {
		if r.PaneID == pane {
			return r, true
		}
	}
	return deck.Row{}, false
}

func written(file string, since time.Time) bool {
	info, err := os.Stat(file)
	return err == nil && info.ModTime().After(since)
}

func (m *model) typeIn(pane, text string) tea.Cmd {
	t := m.ops.T
	return func() tea.Msg {
		if _, err := t.Run("send-keys", "-t", pane, "-l", text); err != nil {
			return errMsg{err}
		}
		time.Sleep(200 * time.Millisecond)
		if _, err := t.Run("send-keys", "-t", pane, "Enter"); err != nil {
			return errMsg{err}
		}
		return nil
	}
}
