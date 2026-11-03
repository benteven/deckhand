package reviewui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/textclean"
)

var (
	white  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	gray   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7c8291"))
	orange = lipgloss.NewStyle().Foreground(lipgloss.Color("#d97757"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#e5b85c"))
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("#8cc97a"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e06c75"))
	blue   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7fb3ff"))
	purple = lipgloss.NewStyle().Foreground(lipgloss.Color("#b39ddb"))
	rule   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	cursor = lipgloss.NewStyle().Background(lipgloss.Color("#2a2f3a"))
)

const hint = "j/k move  n next change  ]/[ file  J/K PR  c comment  t tests"

func (m *model) room() int {
	return max(m.h-1, 1)
}

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m *model) render() string {
	if m.w < 10 || m.h < 3 {
		return ""
	}
	colW := min(30, m.w/3)
	diffW := m.w - colW - 1
	left := m.fileColumn(colW, m.room())
	right := m.diffView(diffW, m.room())
	lines := make([]string, 0, m.h)
	for i := 0; i < m.room(); i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines = append(lines, l+strings.Repeat(" ", max(colW-ansi.StringWidth(l), 0))+rule.Render("│")+r)
	}
	return strings.Join(append(lines, m.bottom()), "\n")
}

func (m *model) bottom() string {
	status := yellow
	if m.statusErr {
		status = red
	}
	switch {
	case m.commenting && m.status != "":
		return status.Render(ansi.Truncate(m.status, m.w, "…"))
	case m.commenting:
		return yellow.Render("comment › ") + m.input.View()
	case m.status != "" && m.statusErr:
		return red.Render(ansi.Truncate(m.status, m.w, "…"))
	case m.status != "":
		return yellow.Render(ansi.Truncate(m.status, m.w, "…"))
	}
	return gray.Render(ansi.Truncate(hint, m.w, "…"))
}

func (m *model) fileColumn(w, h int) []string {
	head := white.Render(fmt.Sprintf("PR #%d", m.pr.Number)) + gray.Render("  J/K")
	out := []string{ansi.Truncate(head, w, "…")}
	if m.err == nil && !m.src.CheckedOut {
		out = append(out, yellow.Render(ansi.Truncate("not checked out", w, "…")))
	}
	var body []string
	selLine := 0
	for g := review.Changes; g <= review.Generated; g++ {
		var idx []int
		for i, f := range m.files {
			if f.Group == g {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			continue
		}
		if g >= review.Tests && !m.showFixed {
			body = append(body, gray.Render(ansi.Truncate(fmt.Sprintf("▸ %s (%d)", g.Label(), len(idx)), w, "…")))
			continue
		}
		body = append(body, gray.Render(ansi.Truncate(g.Label(), w, "…")))
		for _, i := range idx {
			if i == m.sel {
				selLine = len(body)
			}
			body = append(body, fileLine(m.files[i], i == m.sel, w))
		}
	}
	room := max(h-len(out), 1)
	start := max(min(selLine-room/2, len(body)-room), 0)
	return append(out, body[start:min(start+room, len(body))]...)
}

func fileLine(f review.File, selected bool, w int) string {
	bar := "  "
	if selected {
		bar = orange.Render("▌") + " "
	}
	stats := green.Render(fmt.Sprintf("+%d", f.Added)) + " " + red.Render(fmt.Sprintf("-%d", f.Removed))
	if f.Binary {
		stats = gray.Render("bin")
	}
	room := w - 2 - ansi.StringWidth(stats) - 1
	name := f.Path
	if r := []rune(name); len(r) > room && room > 1 {
		name = "…" + string(r[len(r)-room+1:])
	}
	style := gray
	if selected {
		style = white
	}
	return bar + style.Render(name) + strings.Repeat(" ", max(room-ansi.StringWidth(name), 0)+1) + stats
}

func (m *model) diffView(w, h int) []string {
	if m.err != nil {
		return []string{red.Render(ansi.Truncate(m.err.Error(), w, "…"))}
	}
	f, ok := m.selected()
	if !ok {
		return []string{gray.Render("No changes")}
	}
	title := f.Path
	if f.OldPath != "" {
		title = f.OldPath + " → " + f.Path
	}
	out := []string{blue.Render(ansi.Truncate(title, w, "…"))}
	if f.Note != "" {
		wrapped := strings.Split(ansi.Wrap(textclean.Clean(f.Note), w, ""), "\n")
		for _, l := range wrapped[:min(2, len(wrapped))] {
			out = append(out, purple.Render(l))
		}
	}
	room := max(h-len(out), 1)
	if m.cur < m.top {
		m.top = m.cur
	}
	if m.cur >= m.top+room {
		m.top = m.cur - room + 1
	}
	for i := m.top; i < len(m.lines) && i < m.top+room; i++ {
		out = append(out, codeLine(m.lines[i], i == m.cur, w))
	}
	return out
}

func codeLine(l review.Line, atCursor bool, w int) string {
	num, mark, style := l.New, " ", white
	switch l.Kind {
	case review.Add:
		mark, style = "+", green
	case review.Del:
		num, mark, style = l.Old, "-", red
	}
	n := "    "
	if num > 0 {
		n = fmt.Sprintf("%4d", num)
	}
	text := ansi.Truncate(n+" "+mark+" "+textclean.Clean(l.Text), w, "…")
	if atCursor {
		return cursor.Inherit(style).Render(text + strings.Repeat(" ", max(w-ansi.StringWidth(text), 0)))
	}
	return style.Render(text)
}
