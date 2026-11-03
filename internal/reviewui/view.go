package reviewui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/style"
	"github.com/benteven/deckhand/internal/textclean"
)

var (
	white  = style.White
	gray   = style.Gray
	orange = style.Orange
	yellow = style.Yellow
	green  = style.Green
	red    = style.Red
	blue   = style.Blue
	purple = style.Purple
	rule   = style.Rule
	cursor = lipgloss.NewStyle().Background(lipgloss.Color("#2a2f3a"))
)

const hint = "j/k move  n/N change  ]/[ file  J/K PR  c comment  t tests  tab claude"

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
	case m.status != "":
		return status.Render(ansi.Truncate(textclean.Clean(m.status), m.w, "…"))
	case m.commenting:
		return yellow.Render("comment › ") + m.input.View() + gray.Render("  enter send  esc cancel")
	}
	return gray.Render(ansi.Truncate(hint, m.w, "…"))
}

func (m *model) fileColumn(w, h int) []string {
	if !m.ready {
		return []string{gray.Render("loading")}
	}
	head := white.Render(fmt.Sprintf("PR #%d", m.pr.Number)) + gray.Render("  J/K")
	out := []string{ansi.Truncate(head, w, "…")}
	if m.want != "" {
		return append(out, gray.Render("loading"))
	}
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
	bar := style.SelectionBar(selected)
	stats := green.Render(fmt.Sprintf("+%d", f.Added)) + " " + red.Render(fmt.Sprintf("-%d", f.Removed))
	if f.Binary {
		stats = gray.Render("bin")
	}
	room := max(w-2-ansi.StringWidth(stats)-1, 1)
	name := textclean.Clean(f.Path)
	if over := ansi.StringWidth(name) - room; over > 0 {
		name = ansi.TruncateLeft(name, over+1, "…")
	}
	look := gray
	if selected {
		look = white
	}
	return ansi.Truncate(bar+look.Render(name)+strings.Repeat(" ", max(room-ansi.StringWidth(name), 0)+1)+stats, w, "")
}

func (m *model) diffView(w, h int) []string {
	if !m.ready || m.want != "" {
		return nil
	}
	if m.err != nil {
		return []string{red.Render(ansi.Truncate(textclean.Clean(m.err.Error()), w, "…"))}
	}
	f, ok := m.selected()
	if !ok && len(m.files) > 0 {
		return []string{gray.Render(ansi.Truncate("Only tests and generated files, t shows them", w, "…"))}
	}
	if !ok {
		return []string{gray.Render("No changes")}
	}
	title := textclean.Clean(f.Path)
	if f.OldPath != "" {
		title = textclean.Clean(f.OldPath) + " → " + title
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
	num, mark, look := l.New, " ", white
	switch l.Kind {
	case review.Add:
		mark, look = "+", green
	case review.Del:
		num, mark, look = l.Old, "-", red
	}
	n := "    "
	if num > 0 {
		n = fmt.Sprintf("%4d", num)
	}
	text := ansi.Truncate(n+" "+mark+" "+textclean.Clean(l.Text), w-1, "…")
	if atCursor {
		return orange.Render("▌") + cursor.Inherit(look).Render(text+strings.Repeat(" ", max(w-1-ansi.StringWidth(text), 0)))
	}
	return " " + look.Render(text)
}
