package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/style"
	"github.com/benteven/deckhand/internal/textclean"
	"github.com/benteven/deckhand/internal/transcript"
	"github.com/benteven/deckhand/internal/update"
)

const (
	barCells   = 12
	agentLimit = 3
)

var (
	normal = style.Normal
	gray   = style.Gray
	dim    = style.Dim
	orange = style.Orange
	yellow = style.Yellow
	green  = style.Green
	red    = style.Red
	blue   = style.Blue
	purple = style.Purple
	rule   = style.Rule

	numColors = []string{"#ff6b6b", "#ffa94d", "#ffd43b", "#69db7c", "#38d9a9", "#4dabf7", "#9775fa", "#f783ac", "#e9ecef"}
)

func numLabel(n int) string {
	if n < 1 || n > len(numColors) {
		return ""
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(numColors[n-1])).Render(strconv.Itoa(n))
}

func nameLine(name string, style lipgloss.Style, mark string, num int, remote bool, inner int) string {
	tag := numLabel(num)
	if remote {
		tag = strings.TrimSpace(yellow.Render("●") + " " + tag)
	}
	reserved := lipgloss.Width(mark)
	if tag != "" {
		reserved += lipgloss.Width(tag) + 1
	}
	left := style.Render(ansi.Truncate(name, max(inner-reserved, 1), "…")) + mark
	if tag == "" {
		return left
	}
	pad := max(inner-lipgloss.Width(left)-lipgloss.Width(tag), 1)
	return left + strings.Repeat(" ", pad) + tag
}

func (m *model) View() tea.View {
	w, h := m.size()
	var content string
	switch {
	case m.cleanup != nil:
		content = m.cleanup.view(w, h)
	case m.picker != nil:
		content = m.picker.view(w, h)
	case m.prompt != nil:
		content = m.prompt.view(w, h)
	case m.takeover != nil:
		content = m.takeover.view(w, h)
	case m.quit != nil:
		content = m.quit.view(w, h)
	case m.del != nil:
		content = m.del.view(w, h)
	case m.restart != nil:
		content = m.restart.view(w, h)
	case m.assist != nil:
		content = m.assist.view(w, h)
	case m.handoff != nil:
		content = m.handoff.view(w, h)
	case m.help != nil:
		content = m.helpView(w, h)
	case m.accounts != nil:
		content = m.accountsView(w, h)
	default:
		content = m.listView(w, h)
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.ReportFocus = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m *model) listView(w, h int) string {
	blocks, hint, room := m.layout(w, h)
	head := []string{m.tabsLine(w)}
	if m.searching || m.query != "" {
		head = append(head, m.searchLine(w))
	}
	var body []string
	if len(blocks) == 0 {
		body = []string{gray.Render(ansi.Truncate(m.emptyText(), w, ""))}
	} else {
		body = window(blocks, m.cursor, room, w)
	}
	return page(append(head, body...), hint, h)
}

func (m *model) layout(w, h int) (blocks [][]string, hint string, room int) {
	now := time.Now()
	rows := m.shown()
	for i, r := range rows {
		var block []string
		header := m.rowHeader(rows, i)
		if header != "" {
			block = append(block, gray.Render(ansi.Truncate(header, w, "…")))
		}
		if m.mode == modeSuspended {
			block = append(block, suspendedLines(r, w, now, i == m.cursor, m.prState, m.nameStyle(r), m.textHit(r.SessionID), m.query)...)
		} else {
			block = append(block, rowLines(r, w, i == m.cursor, m.agentAt(i, r), m.prState, m.accountTag(r), m.nameStyle(r))...)
		}
		blocks = append(blocks, block)
		if m.rename != nil && r.SessionID == m.rename.id {
			idx := 0
			if header != "" {
				idx = 1
			}
			block[idx] = m.rename.line(w)
		}
		if m.arts != nil && r.SessionID == m.arts.id {
			blocks[len(blocks)-1] = append(blocks[len(blocks)-1], m.artifactLines(r, w)...)
		}
		if m.prPick != nil && r.SessionID == m.prPick.id {
			blocks[len(blocks)-1] = append(blocks[len(blocks)-1], m.prPickLines(w)...)
		}
	}
	hint = m.hintLine(w)
	room = h - 2 - strings.Count(hint, "\n")
	if m.searching || m.query != "" {
		room--
	}
	return blocks, hint, room
}

func (m *model) size() (int, int) {
	if m.width < 1 || m.height < 1 {
		return m.ops.Cfg.ListWidth, 24
	}
	return m.width, m.height
}

func (m *model) visibleRows() []deck.Row {
	w, h := m.size()
	blocks, _, room := m.layout(w, h)
	rows := m.shown()
	start := windowStart(blocks, m.cursor, room)
	end := start
	used := 0
	for end < len(blocks) {
		used += len(blocks[end])
		if end > start {
			used++
		}
		if used > room {
			break
		}
		end++
	}
	return rows[start:max(end, min(start+1, len(rows)))]
}

func (m *model) emptyText() string {
	switch {
	case m.query != "":
		return "No matches."
	case m.mode == modeSuspended:
		return "No suspended sessions."
	}
	return "No sessions. n starts one."
}

func (m *model) searchLine(w int) string {
	line := yellow.Render("/") + m.query
	if m.searching {
		line = m.search.View()
	}
	if m.textPending() {
		line += gray.Render("  searching")
	}
	return ansi.Truncate(line, w, "…")
}

func suspendedLines(r deck.Row, w int, now time.Time, selected bool, prState func(string) prStatus, name lipgloss.Style, hit, query string) []string {
	bar := selectionBar(selected)
	inner := max(w-2, 1)
	meta := []string{gray.Render(ago(now, r.LastInteraction))}
	if r.Cwd != "" {
		meta = append(meta, gray.Render(textclean.Clean(filepath.Base(r.Cwd))))
	}
	for _, pr := range visiblePRs(r.Summary.PRs, prState) {
		meta = append(meta, prText(pr, prState(pr.URL), true))
	}
	lines := []string{
		name.Render(ansi.Truncate(r.Name, inner, "…")),
		fitItems(meta, inner),
	}
	if hit != "" {
		lines = append(lines, ansi.Truncate(highlight(textclean.Clean(hit), query), inner, "…"))
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(bar+l, w, "")
	}
	return lines
}

func ago(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
	return fmt.Sprintf("%dw ago", int(d/(7*24*time.Hour)))
}

func windowStart(blocks [][]string, cursor, room int) int {
	height := func(from, to int) int {
		n := 0
		for i := from; i <= to; i++ {
			n += len(blocks[i]) + 1
		}
		return n - 1
	}
	start := 0
	for start < cursor && height(start, cursor) > room {
		start++
	}
	return start
}

func window(blocks [][]string, cursor, room, w int) []string {
	start := windowStart(blocks, cursor, room)
	sep := rule.Render(strings.Repeat("─", w))
	var out []string
	for i := start; i < len(blocks); i++ {
		if i > start {
			out = append(out, sep)
		}
		out = append(out, blocks[i]...)
	}
	return out
}

func page(body []string, hint string, h int) string {
	room := max(h-1-strings.Count(hint, "\n"), 0)
	if len(body) > room {
		body = body[:room]
	}
	for len(body) < room {
		body = append(body, "")
	}
	return strings.Join(append(body, hint), "\n")
}

func selectionBar(selected bool) string {
	return style.SelectionBar(selected)
}

func rowLines(r deck.Row, w int, selected bool, agent int, prState func(string) prStatus, account string, name lipgloss.Style) []string {
	if r.Kind == deck.Stale {
		return staleLines(r, w, selected, prState)
	}
	bar := selectionBar(selected)
	pad := subPad(r)
	inner := max(w-2-len(pad), 1)

	mark := ""
	switch {
	case limitFresh(r):
		mark = " " + red.Render("● limit")
	case r.Kind == deck.Live && r.Waiting:
		mark = " " + yellow.Render("● waiting")
	case r.Kind == deck.Elsewhere:
		mark = " " + gray.Render("elsewhere")
	}
	mark += rowMarks(r)
	ctx := contextLine(r.Summary.ContextPercent())
	if account != "" {
		ctx += gray.Render(" · " + account)
	}
	if limitFresh(r) {
		ctx += red.Render(" · resets " + resetLabel(r.Summary.Limit.Resets, time.Now()))
	}
	if selected {
		name = name.Bold(true)
	}
	lines := []string{nameLine(subName(r), name, mark, r.Num, r.Remote, inner), ctx}
	lines = append(lines, agentLines(r.Agents, inner, true)...)
	if selected {
		lines = append(lines, detailLines(r, inner)...)
	}

	var extra []string
	for _, pr := range visiblePRs(r.Summary.PRs, prState) {
		extra = append(extra, prText(pr, prState(pr.URL), true))
	}
	if len(extra) > 0 {
		lines = append(lines, fitItems(extra, inner))
	}
	if n := len(r.Summary.Artifacts); n > 0 {
		lines = append(lines, purple.Render(fmt.Sprintf("Artifacts: %d", n)))
	}

	for i, l := range lines {
		b := bar
		if agent >= 0 {
			b = selectionBar(i == 2+agent)
		}
		lines[i] = ansi.Truncate(b+pad+l, w, "")
	}
	return lines
}

func subName(r deck.Row) string {
	if r.Parent != "" {
		return "↳ " + r.Name
	}
	return r.Name
}

func rowMarks(r deck.Row) string {
	mark := ""
	if r.Assistant {
		mark += " " + gray.Render("assistant")
		if r.Paused {
			mark += " " + gray.Render("paused")
		}
	}
	if r.Orphan != "" {
		mark += " " + gray.Render("sub of "+r.Orphan)
	}
	return mark
}

func subPad(r deck.Row) string {
	if r.Parent != "" {
		return "  "
	}
	return ""
}

func detailLines(r deck.Row, width int) []string {
	var where []string
	if r.Cwd != "" {
		where = append(where, textclean.Clean(filepath.Base(r.Cwd)))
	}
	if b := r.Summary.Branch; b != "" {
		where = append(where, textclean.Clean(b))
	}
	if !r.LastInteraction.IsZero() {
		where = append(where, ago(time.Now(), r.LastInteraction))
	}
	var out []string
	if len(where) > 0 {
		out = append(out, gray.Render(ansi.Truncate(strings.Join(where, " · "), width, "…")))
	}
	if p := strings.Join(strings.Fields(textclean.Clean(r.Summary.LastPrompt)), " "); p != "" {
		out = append(out, gray.Render("› ")+normal.Render(ansi.Truncate(p, max(width-2, 1), "…")))
	}
	return out
}

func agentLines(agents []transcript.Agent, width int, colored bool) []string {
	shown := agents[:min(len(agents), agentLimit)]
	var out []string
	for _, a := range shown {
		arrow, desc := "↳", ansi.Truncate(a.Label, max(width-4, 1), "…")
		if colored {
			arrow, desc = gray.Render(arrow), normal.Render(desc)
		}
		out = append(out, "  "+arrow+" "+desc)
	}
	if n := len(agents) - len(shown); n > 0 {
		more := fmt.Sprintf("+%d more", n)
		if colored {
			more = gray.Render(more)
		}
		out = append(out, "  "+more)
	}
	return out
}

func staleLines(r deck.Row, w int, selected bool, prState func(string) prStatus) []string {
	bar := selectionBar(selected)
	pad := subPad(r)
	inner := max(w-2-len(pad), 1)
	pct := r.Summary.ContextPercent()
	filled := min(max(pct, 0), 100) * barCells / 100
	lines := []string{
		nameLine(subName(r), gray, rowMarks(r), r.Num, r.Remote, inner),
		strings.Repeat("━", filled) + strings.Repeat("─", barCells-filled) + fmt.Sprintf(" %d%% ctx", pct),
	}
	lines = append(lines, agentLines(r.Agents, inner, false)...)
	var extra []string
	for _, pr := range visiblePRs(r.Summary.PRs, prState) {
		extra = append(extra, prText(pr, prState(pr.URL), false))
	}
	if len(extra) > 0 {
		lines = append(lines, fitItems(extra, inner))
	}
	if n := len(r.Summary.Artifacts); n > 0 {
		lines = append(lines, fmt.Sprintf("Artifacts: %d", n))
	}
	for i, l := range lines {
		if i > 0 {
			l = gray.Render(l)
		}
		lines[i] = ansi.Truncate(bar+pad+l, w, "")
	}
	return lines
}

func fitItems(items []string, width int) string {
	out := items[0]
	for _, it := range items[1:] {
		if lipgloss.Width(out)+2+lipgloss.Width(it) > width {
			break
		}
		out += "  " + it
	}
	return out
}

func visiblePRs(prs []transcript.PRLink, prState func(string) prStatus) []transcript.PRLink {
	var out []transcript.PRLink
	for _, pr := range prs {
		if s := prState(pr.URL).state; s == "open" || s == "draft" || s == "unknown" {
			out = append(out, pr)
		}
	}
	return out
}

func linkable(url string) bool {
	return strings.HasPrefix(url, "https://") && !strings.ContainsFunc(url, func(r rune) bool { return r <= ' ' || r == 0x7f })
}

type prStatus struct{ state, checks string }

func prIcon(state string) string {
	switch state {
	case "open":
		return "●"
	case "draft":
		return "○"
	}
	return ""
}

func prText(pr transcript.PRLink, st prStatus, colored bool) string {
	text := fmt.Sprintf("#%d", pr.Number)
	if colored {
		text = blue.Render(text)
	}
	if linkable(pr.URL) {
		text = ansi.SetHyperlink(pr.URL) + text + ansi.ResetHyperlink()
	}
	icon := prIcon(st.state)
	switch {
	case icon == "":
		return text
	case colored:
		return checkStyle(st.checks).Render(icon) + " " + text
	}
	return icon + " " + text
}

func checkStyle(checks string) lipgloss.Style {
	switch checks {
	case "pass":
		return green
	case "fail":
		return red
	case "pending":
		return yellow
	}
	return blue
}

func contextLine(pct int) string {
	filled := min(max(pct, 0), 100) * barCells / 100
	style := green
	switch {
	case pct >= 85:
		style = red
	case pct >= 60:
		style = yellow
	}
	return style.Render(strings.Repeat("━", filled)) + dim.Render(strings.Repeat("━", barCells-filled)) + gray.Render(fmt.Sprintf(" %d%% ctx", pct))
}

func (m *model) hintLine(w int) string {
	if m.rename != nil {
		return m.rename.hint(w)
	}
	if m.arts != nil {
		return gray.Render(ansi.Truncate("↑↓ move  enter open  esc close", w, "…"))
	}
	if m.prPick != nil {
		return gray.Render(ansi.Truncate("↑↓ move  enter review  esc close", w, "…"))
	}
	var text string
	style := gray
	switch {
	case m.err != nil:
		text, style = m.err.Error(), red
	case m.hint != "":
		text, style = m.hint, yellow
	case m.searching:
		text = "enter keep  esc clear"
	case m.focused:
		text = m.listHint(w)
	default:
		text = prefixLabel(m.ops.Cfg.Prefix) + ": list"
	}
	if m.err != nil || m.hint != "" {
		text = strings.ReplaceAll(text, "\n", " ")
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = style.Render(ansi.Truncate(l, w, "…"))
	}
	if update.Newer(m.version, m.latest) {
		lines = append([]string{yellow.Render(ansi.Truncate("v"+m.latest+" is out, U upgrades", w, "…"))}, lines...)
	}
	return strings.Join(lines, "\n")
}

func (m *model) listHint(w int) string {
	lines := [][]string{{"enter open", "n new", "s suspend", "Q quit"}, {"? help", "/ search", "tab suspended", "q close"}}
	switch {
	case m.mode == modeSuspended:
		lines = [][]string{{"enter resume", "r rename", "tab active"}, {"? help", "d delete", "/ search", "q close", "Q quit"}}
	case m.onElsewhere():
		lines = [][]string{{"t take over", "n new", "q close", "Q quit"}, {"? help", "/ search", "tab suspended"}}
	}
	fitted := make([]string, len(lines))
	for i, items := range lines {
		fitted[i] = fitItems(items, w)
	}
	text := strings.Join(fitted, "\n")
	if r, ok := m.current(); ok && r.Kind == deck.Live {
		text = addItem(text, "R restart", w)
		text = addItem(text, "c handoff", w)
		if len(m.loggedIn()) > 1 {
			text = addItem(text, "t move", w)
		}
	}
	if r, ok := m.current(); ok && len(visiblePRs(r.Summary.PRs, m.prState)) > 0 {
		text = addItem(text, "o open PR", w)
	}
	if r, ok := m.current(); ok && len(m.reviewable(r)) > 0 {
		text = addItem(text, "v review", w)
	}
	if r, ok := m.current(); ok && len(r.Summary.Artifacts) > 0 {
		text = addItem(text, "a artifacts", w)
	}
	if m.mode == modeActive {
		if m.agentMode {
			text = addItem(text, "g agents off", w)
		} else {
			text = addItem(text, "g agents", w)
		}
	}
	text = addItem(text, "A account", w)
	text = addItem(text, "U upgrade", w)
	return text
}

func addItem(text, item string, w int) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if lipgloss.Width(l)+2+lipgloss.Width(item) <= w {
			lines[i] = l + "  " + item
			return strings.Join(lines, "\n")
		}
	}
	return text
}

func (m *model) onElsewhere() bool {
	r, ok := m.current()
	return ok && r.Kind == deck.Elsewhere
}

func prefixLabel(p string) string {
	if rest, ok := strings.CutPrefix(p, "C-"); ok {
		return "ctrl-" + rest
	}
	return p
}

func (m *model) tabLabels() (string, string) {
	return fmt.Sprintf("Active %d", len(m.active)), fmt.Sprintf("Suspended %d", len(m.suspended))
}

func (m *model) tabsLine(w int) string {
	a, s := m.tabLabels()
	current := normal.Underline(true)
	if m.mode == modeSuspended {
		a, s = normal.Render(a), current.Render(s)
	} else {
		a, s = current.Render(a), normal.Render(s)
	}
	return ansi.Truncate(a+"  "+s, w, "")
}

func highlight(text, query string) string {
	lower := strings.ToLower(text)
	terms := strings.Fields(strings.ToLower(query))
	if len(lower) != len(text) || len(terms) == 0 {
		return gray.Render(text)
	}
	at := strings.Index(lower, terms[0])
	if at < 0 {
		return gray.Render(text)
	}
	end := at + len(terms[0])
	return gray.Render(text[:at]) + yellow.Render(text[at:end]) + gray.Render(text[end:])
}
