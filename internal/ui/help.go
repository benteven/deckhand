package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type helpScreen struct{ top int }

type helpSection struct {
	title string
	keys  [][2]string
}

func helpSections(prefix string) []helpSection {
	p := prefixLabel(prefix)
	return []helpSection{
		{"Anywhere", [][2]string{
			{p, "Focus the list"},
			{"ctrl-j / ctrl-k", "Next or previous session"},
			{"ctrl-q / ctrl-shift-q", "Next or previous in the list like Cmd-Tab; the pick moves to the top"},
			{p + " 1-9", "Go to session 1 to 9"},
			{p + " < / >", "Narrower or wider list"},
			{p + " ?", "This help"},
			{p + " " + p, "Same as ctrl-q"},
			{p + " ctrl-j / k / q", "Send that key to Claude Code"},
		}},
		{"In the list", [][2]string{
			{"↑↓ / j k", "Move; the right side follows"},
			{"enter", "Open, or resume a suspended session"},
			{"esc", "Back to the session you came from"},
			{"1-9", "Go to that session"},
			{"tab", "Switch between Active and Suspended"},
			{"/", "Search; in Suspended also the whole conversation"},
			{"n", "New session in any folder"},
			{"s", "Suspend: rename, then stop"},
			{"r", "Rename"},
			{"R", "Restart the session in place"},
			{"c", "Hand off: save state, /clear, read it back"},
			{"A", "Switch account, for this session or all; limits move sessions on their own"},
			{"g", "Agent mode: step through subagents"},
			{"t", "Take over from another terminal, or move to the next account"},
			{"o", "Open the pull requests"},
			{"v", "Review the pull requests: diff, notes, comments to Claude"},
			{"a", "List the artifacts"},
			{"d", "Move a suspended session to the Trash"},
			{"U", "Upgrade and reload; sessions keep running"},
			{"q", "Close the app; sessions keep running"},
			{"Q", "Stop all sessions and quit"},
		}},
		{"Mouse", [][2]string{
			{"click row", "Open the session"},
			{"click ● ○", "Open the pull request"},
			{"click Artifacts", "List the artifacts"},
			{"drag border", "Resize the list"},
		}},
	}
}

func (m *model) helpLines(w int) []string {
	sections := helpSections(m.ops.Cfg.Prefix)
	col := 0
	for _, s := range sections {
		for _, k := range s.keys {
			col = max(col, ansi.StringWidth(k[0]))
		}
	}
	var lines []string
	for i, s := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, normal.Bold(true).Render(s.title))
		for _, k := range s.keys {
			key := orange.Render(k[0]) + strings.Repeat(" ", col+2-ansi.StringWidth(k[0]))
			lines = append(lines, ansi.Truncate("  "+key+gray.Render(k[1]), w, "…"))
		}
	}
	return lines
}

func (m *model) helpView(w, h int) string {
	lines := m.helpLines(w)
	room := max(h-1, 1)
	m.help.top = min(m.help.top, max(len(lines)-room, 0))
	return page(lines[m.help.top:], gray.Render(ansi.Truncate("↑↓ scroll  esc close", w, "")), h)
}

func (m *model) helpKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		m.help.top = max(m.help.top-1, 0)
	case "down", "j":
		m.help.top++
	case "esc", "?", "q", "enter":
		m.help = nil
		return m.zoom(false)
	}
	return nil
}
