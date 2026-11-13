package ui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/keys"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/tmux"
)

const (
	cycleWindow = 1500 * time.Millisecond
	ctrlPoll    = 40 * time.Millisecond
)

type cycle struct {
	order []string
	i     int
	seq   int
	at    time.Time
	held  bool
}

type cycleDoneMsg struct{ seq int }

type ctrlPollMsg struct{ seq int }

func (m *model) slotSession() string {
	for _, r := range m.liveRows() {
		if r.PaneID == m.slot {
			return r.SessionID
		}
	}
	return ""
}

func (m *model) cycleOrder() []string {
	cur := m.slotSession()
	if cur == "" {
		cur = m.selected
	}
	order := []string{cur}
	for _, r := range m.liveRows() {
		if r.SessionID != cur && r.Parent == "" {
			order = append(order, r.SessionID)
		}
	}
	return order
}

func (m *model) toggleLast(back bool) tea.Cmd {
	if m.quitting || m.dialogOpen() {
		return nil
	}
	now := time.Now()
	c := m.cycle
	if c == nil || (!c.held && now.Sub(c.at) > cycleWindow) {
		c = &cycle{order: m.cycleOrder()}
		m.cycle = c
	}
	if len(c.order) < 2 {
		m.cycle = nil
		m.hint = "No other session to go to"
		return nil
	}
	step := 1
	if back {
		step = len(c.order) - 1
	}
	c.i = (c.i + step) % len(c.order)
	c.at = now
	c.seq++
	rows := m.liveRows()
	i := slices.IndexFunc(rows, func(r deck.Row) bool { return r.SessionID == c.order[c.i] })
	if i < 0 {
		return nil
	}
	r := rows[i]
	m.selectRow(r.SessionID)
	seq := c.seq
	wait := tea.Tick(cycleWindow, func(time.Time) tea.Msg { return cycleDoneMsg{seq} })
	if down, known := keys.CtrlDown(); known && down {
		c.held = true
		wait = pollCtrl(seq)
	}
	return tea.Batch(tea.Sequence(m.show(r.PaneID, "", false), m.focus(tmux.Slot)), wait)
}

func pollCtrl(seq int) tea.Cmd {
	return tea.Tick(ctrlPoll, func(time.Time) tea.Msg { return ctrlPollMsg{seq} })
}

func (m *model) ctrlPolled(msg ctrlPollMsg) tea.Cmd {
	if c := m.cycle; c == nil || c.seq != msg.seq {
		return nil
	}
	if down, _ := keys.CtrlDown(); down {
		return pollCtrl(msg.seq)
	}
	return m.cycleDone(cycleDoneMsg(msg))
}

func (m *model) cycleDone(msg cycleDoneMsg) tea.Cmd {
	c := m.cycle
	if c == nil || c.seq != msg.seq {
		return nil
	}
	m.cycle = nil
	id := c.order[c.i]
	i := slices.IndexFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == id })
	if i < 0 || c.i == 0 {
		return nil
	}
	m.state.Active[i].LastInteraction = time.Now()
	m.rebuild()
	return m.saveState()
}
