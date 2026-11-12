package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/textclean"
	"github.com/benteven/deckhand/internal/typing"
)

const (
	handoffTimeout = 15 * time.Minute
	clearWait      = 5 * time.Second
)

type handoffDialog struct{ row deck.Row }

type pendingHandoff struct {
	id, name, file string
	sent, cleared  time.Time
	fresh          bool
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
	case r.Asking || m.in.Events[r.SessionID].Menu():
		return "The session shows a menu. Try again when it waits"
	case r.Busy || m.typing[r.PaneID]:
		return "The session is busy. Try again when it is idle"
	}
	return ""
}

func (m *model) handoffKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		r, ok := m.rowInPane(m.handoff.row.PaneID)
		m.handoff = nil
		if !ok || r.Kind != deck.Live {
			return m.zoom(false)
		}
		if why := m.handoffRefusal(r); why != "" {
			m.hint = why
			return m.zoom(false)
		}
		prompt, err := m.beginHandoff(r)
		if err != nil {
			m.err = err
			return m.zoom(false)
		}
		return tea.Sequence(m.zoom(false), m.typeIn(r, prompt, m.handoffTyped(r.PaneID, func(err error) tea.Cmd {
			if err != nil {
				m.err = typeError(r.Name, err)
			}
			return nil
		})))
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

func (m *model) handoffTyped(pane string, report func(error) tea.Cmd) func(error) tea.Cmd {
	h := m.handoffs[pane]
	return func(err error) tea.Cmd {
		if err != nil && !errors.Is(err, typing.ErrPasted) && m.handoffs[pane] == h {
			delete(m.handoffs, pane)
			m.hint = ""
		}
		return report(err)
	}
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
			if !m.canType(r) || !written(h.file, h.sent) {
				continue
			}
			if h.fresh {
				if waitsForIdle(r) || !m.userAway(r) {
					continue
				}
				delete(m.handoffs, pane)
				m.hint = ""
				cmds = append(cmds, m.freshAssistant(r, h.file))
				continue
			}
			h.cleared = now
			m.hint = "Clearing " + h.name
			cmds = append(cmds, m.typeIn(r, "/clear", func(err error) tea.Cmd {
				switch {
				case m.handoffs[pane] != h:
				case errors.Is(err, typing.ErrMenu):
					h.cleared = time.Time{}
				case err != nil:
					delete(m.handoffs, pane)
					m.err = typeError(h.name, err)
				}
				return nil
			}))
		case (ok && r.SessionID != h.id) || now.Sub(h.cleared) > clearWait:
			if !ok {
				r = deck.Row{PaneID: pane, Name: h.name}
			} else if !m.canType(r) {
				continue
			}
			delete(m.handoffs, pane)
			m.hint = ""
			cmds = append(cmds, m.typeIn(r, fmt.Sprintf("Read %s. It is the handoff note from before /clear. Continue the work from it.", h.file), func(err error) tea.Cmd {
				switch {
				case errors.Is(err, typing.ErrMenu) && m.handoffs[pane] == nil:
					m.handoffs[pane] = h
				case err != nil:
					m.err = typeError(h.name, err)
				}
				return nil
			}))
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

type typedMsg struct {
	pane string
	err  error
	then func(error) tea.Cmd
}

func (m *model) typeIn(r deck.Row, text string, then func(error) tea.Cmd) tea.Cmd {
	if m.typing == nil {
		m.typing = map[string]bool{}
	}
	m.typing[r.PaneID] = true
	t, id, pane, text := m.ops.T, r.SessionID, r.PaneID, textclean.Clean(text)
	return func() tea.Msg {
		return typedMsg{pane: pane, err: typing.Type(t, id, pane, text), then: then}
	}
}

func (m *model) typed(msg typedMsg) tea.Cmd {
	delete(m.typing, msg.pane)
	if msg.then != nil {
		return msg.then(msg.err)
	}
	return nil
}

func typeError(name string, err error) error {
	switch {
	case errors.Is(err, typing.ErrMenu):
		return fmt.Errorf("%s shows a menu; try again when it waits", name)
	case errors.Is(err, typing.ErrPasted):
		return fmt.Errorf("%s started asking; the text waits in its prompt", name)
	}
	return err
}

func (m *model) canType(r deck.Row) bool {
	return !waitsForIdle(r) && !m.in.Events[r.SessionID].Menu() && !m.typing[r.PaneID]
}
