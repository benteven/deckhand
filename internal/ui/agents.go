package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/transcript"
)

func visibleAgents(r deck.Row) []transcript.Agent {
	if r.Kind == deck.Stale || r.Kind == deck.Suspended {
		return nil
	}
	return r.Agents[:min(len(r.Agents), agentLimit)]
}

func (m *model) agentIndex(r deck.Row) int {
	if !m.agentMode || m.agent == "" {
		return -1
	}
	return slices.IndexFunc(visibleAgents(r), func(a transcript.Agent) bool { return a.Path == m.agent })
}

func (m *model) agentAt(i int, r deck.Row) int {
	if i != m.cursor {
		return -1
	}
	return m.agentIndex(r)
}

func (m *model) toggleAgentMode() tea.Cmd {
	m.agentMode = !m.agentMode
	switch {
	case !m.agentMode:
		m.hint = "Agent mode off"
	case slices.ContainsFunc(m.shown(), func(r deck.Row) bool { return len(visibleAgents(r)) > 0 }):
		m.hint = "Agent mode on: j/k step through subagents"
	default:
		m.hint = "Agent mode on: no subagents are running"
	}
	if m.agent == "" {
		return nil
	}
	m.agent = ""
	return m.showSelected()
}

func (m *model) moveAgent(d int) tea.Cmd {
	rows := m.shown()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return m.selectIndex(m.cursor + d)
	}
	agents := visibleAgents(rows[m.cursor])
	k := m.agentIndex(rows[m.cursor])
	switch {
	case d > 0 && k+1 < len(agents):
		m.agent = agents[k+1].Path
		return m.showSelected()
	case d < 0 && k > 0:
		m.agent = agents[k-1].Path
		return m.showSelected()
	case d < 0 && k == 0:
		m.agent = ""
		return m.showSelected()
	case d < 0 && m.cursor > 0:
		m.cursor--
		m.selected = rows[m.cursor].SessionID
		m.agent = ""
		if prev := visibleAgents(rows[m.cursor]); len(prev) > 0 {
			m.agent = prev[len(prev)-1].Path
		}
		return m.showSelected()
	case d > 0 && m.cursor+1 >= len(rows):
		return nil
	}
	return m.selectIndex(m.cursor + d)
}

func (m *model) selectAgent(row, k int) tea.Cmd {
	rows := m.shown()
	agents := visibleAgents(rows[row])
	m.cursor, m.selected, m.agent = row, rows[row].SessionID, agents[k].Path
	return m.showSelected()
}

func agentTarget(r deck.Row, a transcript.Agent) string {
	return a.Path + "\t" + r.Name + " ↳ " + a.Label + "\t(subagent, read only)"
}
