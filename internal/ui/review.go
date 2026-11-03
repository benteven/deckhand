package ui

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"uuid"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/gh"
	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/tmux"
	"github.com/benteven/deckhand/internal/transcript"
)

type prPicker struct {
	id     string
	prs    []transcript.PRLink
	titles map[string]string
	cursor int
}

type prTitleMsg struct{ url, title string }

func (m *model) isReview(id string) bool {
	_, ok := m.reviews[id]
	return ok
}

func (m *model) reviewOf(source string) (string, review.State, bool) {
	for id, st := range m.reviews {
		if st.Source == source {
			return id, st, true
		}
	}
	return "", review.State{}, false
}

func (m *model) reviewable(r deck.Row) []transcript.PRLink {
	if m.isReview(r.SessionID) || (r.Kind != deck.Live && r.Kind != deck.Stale) {
		return nil
	}
	return visiblePRs(r.Summary.PRs, m.prState)
}

func (m *model) openReview(r deck.Row) tea.Cmd {
	prs := m.reviewable(r)
	switch len(prs) {
	case 0:
		return nil
	case 1:
		return m.reviewPR(r, prs, prs[0])
	}
	m.prPick = &prPicker{id: r.SessionID, prs: prs, titles: map[string]string{}}
	var cmds []tea.Cmd
	for _, pr := range prs {
		url := pr.URL
		cmds = append(cmds, func() tea.Msg {
			_, title, _, _, err := gh.Info(url)
			if err != nil {
				return nil
			}
			return prTitleMsg{url: url, title: title}
		})
	}
	return tea.Batch(cmds...)
}

func (m *model) prPickKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.prPick
	i := slices.IndexFunc(m.shown(), func(r deck.Row) bool { return r.SessionID == p.id })
	if i < 0 {
		m.prPick = nil
		return nil
	}
	switch msg.String() {
	case "up", "k":
		p.cursor = max(p.cursor-1, 0)
	case "down", "j":
		p.cursor = min(p.cursor+1, len(p.prs)-1)
	case "enter":
		m.prPick = nil
		return m.reviewPR(m.shown()[i], p.prs, p.prs[p.cursor])
	case "esc", "v":
		m.prPick = nil
	}
	return nil
}

func (m *model) prPickLines(w int) []string {
	p := m.prPick
	var lines []string
	for i, pr := range p.prs {
		text := white.Render(fmt.Sprintf("#%d", pr.Number))
		if t := p.titles[pr.URL]; t != "" {
			text += " " + gray.Render(strings.Join(strings.Fields(t), " "))
		}
		lines = append(lines, ansi.Truncate("  "+selectionBar(i == p.cursor)+text, w, "…"))
	}
	return lines
}

func (m *model) reviewPR(r deck.Row, prs []transcript.PRLink, chosen transcript.PRLink) tea.Cmd {
	if m.startingReview[r.SessionID] {
		m.hint = "Review is starting"
		return nil
	}
	id, st, ok := m.reviewOf(r.SessionID)
	if !ok {
		return m.startReview(r, prs, chosen)
	}
	home := m.home
	save := func() tea.Msg {
		if fresh, ok, err := review.Load(home, id); err == nil && ok {
			st = fresh
		}
		i := slices.IndexFunc(st.PRs, func(p review.PR) bool { return p.URL == chosen.URL })
		if i < 0 {
			n, title, base, head, err := gh.Info(chosen.URL)
			if err != nil {
				return errMsg{fmt.Errorf("PR #%d: %w", chosen.Number, err)}
			}
			st.PRs = append(st.PRs, review.PR{Number: n, URL: chosen.URL, Repo: chosen.Repo, Title: title, Base: base, Head: head})
			i = len(st.PRs) - 1
		}
		st.Current = i
		if err := st.Save(home, id); err != nil {
			return errMsg{err}
		}
		return nil
	}
	return tea.Sequence(save, m.openRow(id))
}

func (m *model) openRow(id string) tea.Cmd {
	if slices.ContainsFunc(m.active, func(r deck.Row) bool { return r.SessionID == id }) {
		m.selectRow(id)
		return m.enter()
	}
	if !slices.ContainsFunc(m.suspended, func(r deck.Row) bool { return r.SessionID == id }) {
		return nil
	}
	m.setMode(modeSuspended)
	m.selected = id
	m.syncCursor()
	return m.enter()
}

type reviewFailedMsg struct {
	source string
	err    error
}

func (m *model) startReview(r deck.Row, prs []transcript.PRLink, chosen transcript.PRLink) tea.Cmd {
	id := uuid.NewV4().String()
	account := m.launchAccount(id, m.state.AccountFor(r.SessionID))
	ops, home := m.ops, m.home
	if m.startingReview == nil {
		m.startingReview = map[string]bool{}
	}
	m.startingReview[r.SessionID] = true
	m.hint = "Starting review"
	return func() tea.Msg {
		fail := func(err error) tea.Msg { return reviewFailedMsg{source: r.SessionID, err: err} }
		st := review.State{Source: r.SessionID, Cwd: r.Cwd}
		for _, p := range prs {
			n, title, base, head, err := gh.Info(p.URL)
			if err != nil {
				return fail(fmt.Errorf("PR #%d: %w", p.Number, err))
			}
			if p.URL == chosen.URL {
				st.Current = len(st.PRs)
			}
			st.PRs = append(st.PRs, review.PR{Number: n, URL: p.URL, Repo: p.Repo, Title: title, Base: base, Head: head})
		}
		pr, _ := st.PR()
		if _, err := review.Resolve(st.Cwd, pr); err != nil {
			return fail(err)
		}
		review.Fetch(st.Cwd, pr)
		src, err := review.Resolve(st.Cwd, pr)
		if err != nil {
			return fail(err)
		}
		st.Asked = []int{pr.Number}
		if err := st.Save(home, id); err != nil {
			return fail(err)
		}
		prompt := review.NotesPrompt(pr, src, review.NotesPath(home, id, pr.Number))
		pane, err := ops.Start(st.Cwd, id, "", false, account, prompt)
		if err != nil {
			return fail(err)
		}
		return startedMsg{id: id, cwd: st.Cwd, pane: pane, rev: &st}
	}
}

func (m *model) reviewFailed(msg reviewFailedMsg) {
	delete(m.startingReview, msg.source)
	m.hint = ""
	m.err = msg.err
}

func (m *model) reviewStarted(id string, st review.State) {
	delete(m.startingReview, st.Source)
	m.hint = ""
	if m.reviews == nil {
		m.reviews = map[string]review.State{}
	}
	m.reviews[id] = st
	if account := m.state.AccountFor(st.Source); account != m.state.Account {
		if m.state.Accounts == nil {
			m.state.Accounts = map[string]string{}
		}
		m.state.Accounts[id] = account
	}
}

func (m *model) syncReviews(panes []tmux.Pane, reviews map[string]review.State) []tea.Cmd {
	active := map[string]bool{}
	for _, e := range m.state.Active {
		active[e.SessionID] = true
	}
	for id, st := range m.reviews {
		if _, ok := reviews[id]; !ok && active[id] {
			reviews[id] = st
		}
	}
	m.reviews = reviews
	var kills []tea.Cmd
	for _, p := range panes {
		if p.Role == tmux.ReviewRole && !active[p.Review] && !m.resuming[p.Review] {
			kills = append(kills, m.tmux("kill-pane", "-t", p.ID))
		}
	}
	return kills
}

func (m *model) labelReviews() {
	for i, r := range m.active {
		if st, ok := m.reviews[r.SessionID]; ok {
			if pr, ok := st.PR(); ok {
				m.active[i].Name = fmt.Sprintf("review #%d", pr.Number)
			}
		}
	}
}

func (m *model) reviewOfPane(pane string) string {
	if pane == "" {
		return ""
	}
	for id, p := range m.in.Panes {
		if p == pane && m.isReview(id) {
			return id
		}
	}
	return ""
}

func showReview(ops deck.Ops, pane, id string) error {
	panes, err := ops.T.Panes()
	if err != nil {
		return err
	}
	reviewPane := ""
	for _, p := range panes {
		if p.Role != tmux.ReviewRole || p.Review != id {
			continue
		}
		if p.Dead {
			ops.T.Run("kill-pane", "-t", p.ID)
			continue
		}
		reviewPane = p.ID
	}
	if reviewPane == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		return ops.StartReview(exe, id, pane)
	}
	return ops.ShowReview(pane, reviewPane)
}
