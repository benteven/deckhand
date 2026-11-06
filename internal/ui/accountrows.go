package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/benteven/deckhand/internal/accounts"
	"github.com/benteven/deckhand/internal/deck"
)

var accountColors = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#5fd7d7")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#ff87d7")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#afd75f")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#af87ff")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaf5f")),
}

func (m *model) accountStyle(name string) lipgloss.Style {
	k := accounts.Index(m.accountList, name)
	if name == "" || k < 1 {
		return normal
	}
	return accountColors[(k-1)%len(accountColors)]
}

func (m *model) nameStyle(r deck.Row) lipgloss.Style {
	return m.accountStyle(m.rowAccount(r))
}

func (m *model) rowHeader(rows []deck.Row, i int) string {
	if m.mode == modeActive && rows[i].Kind == deck.Elsewhere && (i == 0 || rows[i-1].Kind != deck.Elsewhere) {
		return "Elsewhere"
	}
	return ""
}

func (m *model) loggedIn() []string {
	var out []string
	for _, a := range m.accountList {
		if a.Name == "" || a.Email != "" {
			out = append(out, a.Name)
		}
	}
	return out
}

func nextAccount(list []string, cur string) string {
	return list[(slices.Index(list, cur)+1)%len(list)]
}

func (m *model) moveAccount(r deck.Row) tea.Cmd {
	list := m.loggedIn()
	if len(list) < 2 {
		m.hint = "Add a second account with A first"
		return nil
	}
	return m.switchOne(r, nextAccount(list, m.rowAccount(r)))
}

func (m *model) cursorAccount() string {
	if r, ok := m.current(); ok && (r.Kind == deck.Live || r.Kind == deck.Stale) {
		return m.rowAccount(r)
	}
	return m.state.Account
}
