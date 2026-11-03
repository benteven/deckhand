package reviewui

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/clipperhouse/uax29/v2/graphemes"

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
	addBg  = lipgloss.Color("#1d3324")
	delBg  = lipgloss.Color("#3b2024")
)

var cursorBg = map[review.LineKind]color.Color{
	review.Same: lipgloss.Color("#2a2f3a"),
	review.Add:  lipgloss.Color("#2a4a35"),
	review.Del:  lipgloss.Color("#552a30"),
}

const hint = "j/k move  n/N change  ]/[ file  J/K PR  c comment  t tests  tab claude"

func (m *model) room() int {
	return max(m.h-1, 1)
}

func (m *model) diffWidth() int {
	return m.w - min(30, m.w/3) - 1
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
	colW := m.w - m.diffWidth() - 1
	left := m.fileColumn(colW, m.room())
	right := m.diffView(m.diffWidth(), m.room())
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
	rows := func(i int) int { return len(m.rowStarts(i, w)) }
	if m.recenter && len(m.lines) > 0 {
		m.recenter = false
		m.centerBlock(room, rows)
	}
	m.follow(room, rows)
	for i := m.top; i < len(m.lines) && len(out) < h; i++ {
		out = append(out, m.codeRows(i, w, h-len(out))...)
	}
	return out
}

func (m *model) follow(room int, rows func(int) int) {
	if m.cur < m.top {
		m.top = m.cur
	}
	used := 0
	for i := m.top; i <= m.cur && i < len(m.lines); i++ {
		used += rows(i)
	}
	for m.top < m.cur && used > room {
		used -= rows(m.top)
		m.top++
	}
}

func (m *model) centerBlock(room int, rows func(int) int) {
	end := m.cur
	for end+1 < len(m.lines) && m.lines[end+1].Kind != review.Same {
		end++
	}
	block := 0
	for i := m.cur; i <= end; i++ {
		block += rows(i)
	}
	above := max(room-block, 0) / 2
	m.top = m.cur
	for m.top > 0 && rows(m.top-1) <= above {
		above -= rows(m.top - 1)
		m.top--
	}
	filled := 0
	for i := m.top; i < len(m.lines) && filled < room; i++ {
		filled += rows(i)
	}
	for m.top > 0 && filled+rows(m.top-1) <= room {
		filled += rows(m.top - 1)
		m.top--
	}
}

func (m *model) rowStarts(i, w int) []int {
	if m.layoutW != w || len(m.layout) != len(m.lines) {
		m.layout, m.layoutW = make([][]int, len(m.lines)), w
	}
	if m.layout[i] == nil {
		m.layout[i] = rowStarts(m.code[i], codeWidth(m.lines[i], w))
	}
	return m.layout[i]
}

func gutter(l review.Line) (head string, fg, bg color.Color) {
	num, mark, look, bg := l.New, " ", gray, color.Color(nil)
	switch l.Kind {
	case review.Add:
		mark, look, bg = "+", green, addBg
	case review.Del:
		num, mark, look, bg = l.Old, "-", red, delBg
	}
	n := "    "
	if num > 0 {
		n = fmt.Sprintf("%4d", num)
	}
	return n + " " + mark + " ", look.GetForeground(), bg
}

func codeWidth(l review.Line, w int) int {
	head, _, _ := gutter(l)
	return max(w-1-ansi.StringWidth(head), 1)
}

func rowStarts(code []span, width int) []int {
	var pos []int
	var space []bool
	at := 0
	for _, s := range code {
		g := graphemes.FromString(s.text)
		for g.Next() {
			pos = append(pos, at)
			space = append(space, g.Value() == " ")
			at += ansi.StringWidth(g.Value())
		}
	}
	pos = append(pos, at)
	starts := []int{0}
	row, gap := 0, -1
	for i := range space {
		for pos[i+1]-pos[row] > width && i > row {
			cut := i
			if gap > row {
				cut = gap
			}
			starts = append(starts, pos[cut])
			row, gap = cut, -1
		}
		if space[i] {
			gap = i + 1
		}
	}
	return starts
}

func cutSpans(code []span, lo, hi int) []span {
	var out []span
	at := 0
	for _, s := range code {
		sw := ansi.StringWidth(s.text)
		if a, z := max(lo, at), min(hi, at+sw); a < z {
			out = append(out, span{text: ansi.Cut(s.text, a-at, z-at), fg: s.fg})
		}
		at += sw
	}
	return out
}

func (m *model) codeRows(i, w, limit int) []string {
	l, code := m.lines[i], m.code[i]
	head, fg, bg := gutter(l)
	bar := " "
	if i == m.cur {
		bar = orange.Render("▌")
		bg = cursorBg[l.Kind]
	}
	width := codeWidth(l, w)
	blank := strings.Repeat(" ", ansi.StringWidth(head))
	starts := m.rowStarts(i, w)
	out := make([]string, 0, min(len(starts), limit))
	for r, lo := range starts[:min(len(starts), limit)] {
		hi := math.MaxInt
		if r+1 < len(starts) {
			hi = starts[r+1]
		}
		g := blank
		if r == 0 {
			g = head
		}
		var b strings.Builder
		b.WriteString(bar)
		b.WriteString(paint(fg, bg).Render(g))
		used := 0
		for _, s := range cutSpans(code, lo, hi) {
			used += ansi.StringWidth(s.text)
			b.WriteString(paint(s.fg, bg).Render(s.text))
		}
		b.WriteString(paint(nil, bg).Render(strings.Repeat(" ", max(width-used, 0))))
		out = append(out, b.String())
	}
	return out
}

func paint(fg, bg color.Color) lipgloss.Style {
	st := lipgloss.NewStyle().Foreground(white.GetForeground())
	if fg != nil {
		st = st.Foreground(fg)
	}
	if bg != nil {
		st = st.Background(bg)
	}
	return st
}
