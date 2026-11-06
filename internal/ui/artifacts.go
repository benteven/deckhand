package ui

import (
	"fmt"
	"path"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/textclean"
)

const artifactRows = 8

type artifactList struct {
	id     string
	urls   []string
	cursor int
}

func (m *model) openArtifacts(r deck.Row) tea.Cmd {
	urls := slices.Clone(r.Summary.Artifacts)
	slices.Reverse(urls)
	switch len(urls) {
	case 0:
		return nil
	case 1:
		return openURLs(urls)
	}
	m.arts = &artifactList{id: r.SessionID, urls: urls}
	return nil
}

func (m *model) artifactsKey(msg tea.KeyPressMsg) tea.Cmd {
	a := m.arts
	if !slices.ContainsFunc(m.shown(), func(r deck.Row) bool { return r.SessionID == a.id }) {
		m.arts = nil
		return nil
	}
	switch msg.String() {
	case "up", "k":
		a.cursor = max(a.cursor-1, 0)
	case "down", "j":
		a.cursor = min(a.cursor+1, len(a.urls)-1)
	case "enter":
		m.arts = nil
		return openURLs([]string{a.urls[a.cursor]})
	case "esc", "a":
		m.arts = nil
	}
	return nil
}

func (m *model) artifactTitle(r deck.Row, url string) string {
	if t := r.Summary.ArtifactTitles[url]; t != "" {
		return t
	}
	for _, s := range m.in.Summaries {
		if t := s.ArtifactTitles[url]; t != "" {
			return t
		}
	}
	return ""
}

func (m *model) artifactLines(r deck.Row, w int) []string {
	a := m.arts
	start := max(a.cursor-artifactRows+1, 0)
	end := min(start+artifactRows, len(a.urls))
	var lines []string
	for i := start; i < end; i++ {
		u := a.urls[i]
		text := gray.Render(path.Base(u))
		if t := m.artifactTitle(r, u); t != "" {
			text = normal.Render(strings.Join(strings.Fields(textclean.Clean(t)), " "))
		}
		line := ansi.Truncate("  "+selectionBar(i == a.cursor)+text, w, "…")
		if linkable(u) {
			line = ansi.SetHyperlink(u) + line + ansi.ResetHyperlink()
		}
		lines = append(lines, line)
	}
	if more := len(a.urls) - end; more > 0 {
		lines = append(lines, gray.Render(fmt.Sprintf("    +%d more", more)))
	}
	return lines
}

func (m *model) artifactAt(line string) string {
	if m.arts == nil {
		return ""
	}
	for _, u := range m.arts.urls {
		if strings.Contains(line, ansi.SetHyperlink(u)) {
			return u
		}
	}
	return ""
}

func artifactCountAt(line string, x int) bool {
	plain := ansi.Strip(line)
	i := strings.Index(plain, "Artifacts: ")
	if i < 0 || strings.TrimLeft(plain[:i], "▌ ") != "" {
		return false
	}
	from := ansi.StringWidth(plain[:i])
	end := i + len("Artifacts: ")
	for end < len(plain) && plain[end] >= '0' && plain[end] <= '9' {
		end++
	}
	return x >= from && x < ansi.StringWidth(plain[:end])
}
