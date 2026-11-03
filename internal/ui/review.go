package ui

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"uuid"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/gh"
	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/textclean"
	"github.com/benteven/deckhand/internal/tmux"
	"github.com/benteven/deckhand/internal/transcript"
)

type prPicker struct {
	id     string
	prs    []transcript.PRLink
	cursor int
}

type prInfoMsg struct {
	url  string
	info gh.PR
}

func (m *model) isReview(id string) bool {
	_, ok := m.reviews[id]
	return ok
}

func (m *model) hasRow(id string) bool {
	has := func(r deck.Row) bool { return r.SessionID == id }
	return slices.ContainsFunc(m.active, has) || slices.ContainsFunc(m.suspended, has)
}

func (m *model) reviewOf(source string) (string, review.State, bool) {
	for id, st := range m.reviews {
		if st.Source == source && m.hasRow(id) {
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

type stackMsg struct {
	id   string
	open map[string][]gh.PR
}

func (m *model) openReview(r deck.Row) tea.Cmd {
	prs := m.reviewable(r)
	if len(prs) == 0 {
		return nil
	}
	var repos []string
	for _, p := range prs {
		if !slices.Contains(repos, p.Repo) {
			repos = append(repos, p.Repo)
		}
	}
	id := r.SessionID
	m.hint = "Finding PRs"
	return func() tea.Msg {
		open := map[string][]gh.PR{}
		for _, repo := range repos {
			if list, err := gh.Open(repo); err == nil {
				open[repo] = list
			}
		}
		return stackMsg{id: id, open: open}
	}
}

func (m *model) gotStack(msg stackMsg) tea.Cmd {
	if m.hint == "Finding PRs" {
		m.hint = ""
	}
	i := slices.IndexFunc(m.shown(), func(r deck.Row) bool { return r.SessionID == msg.id })
	if i < 0 {
		return nil
	}
	r := m.shown()[i]
	if m.prInfos == nil {
		m.prInfos = map[string]gh.PR{}
	}
	for _, list := range msg.open {
		for _, p := range list {
			m.prInfos[p.URL] = p
		}
	}
	prs := stackOf(m.reviewable(r), msg.open, m.prInfos)
	switch len(prs) {
	case 0:
		return nil
	case 1:
		return m.reviewPR(r, prs, prs[0])
	}
	m.prPick = &prPicker{id: r.SessionID, prs: mergeOrder(prs, m.prInfos)}
	var cmds []tea.Cmd
	for _, pr := range prs {
		if _, ok := m.prInfos[pr.URL]; ok {
			continue
		}
		url := pr.URL
		cmds = append(cmds, func() tea.Msg {
			info, err := gh.Info(url)
			if err != nil {
				return nil
			}
			return prInfoMsg{url: url, info: info}
		})
	}
	return tea.Batch(cmds...)
}

func (m *model) gotPRInfo(msg prInfoMsg) {
	if m.prInfos == nil {
		m.prInfos = map[string]gh.PR{}
	}
	m.prInfos[msg.url] = msg.info
	if p := m.prPick; p != nil {
		at := p.prs[p.cursor].URL
		p.prs = mergeOrder(p.prs, m.prInfos)
		p.cursor = slices.IndexFunc(p.prs, func(l transcript.PRLink) bool { return l.URL == at })
	}
}

func stackOf(seed []transcript.PRLink, open map[string][]gh.PR, infos map[string]gh.PR) []transcript.PRLink {
	out := slices.Clone(seed)
	linked := func(repo string, p gh.PR) bool {
		return slices.ContainsFunc(out, func(l transcript.PRLink) bool {
			q := infos[l.URL]
			return l.Repo == repo && q.Head != "" && (q.Head == p.Base || q.Base == p.Head)
		})
	}
	for grew := true; grew; {
		grew = false
		for repo, list := range open {
			for _, p := range list {
				if !slices.ContainsFunc(out, func(l transcript.PRLink) bool { return l.URL == p.URL }) && linked(repo, p) {
					out = append(out, transcript.PRLink{Number: p.Number, URL: p.URL, Repo: repo})
					grew = true
				}
			}
		}
	}
	return out
}

func mergeOrder(links []transcript.PRLink, infos map[string]gh.PR) []transcript.PRLink {
	prs := make([]review.PR, len(links))
	for i, l := range links {
		info, ok := infos[l.URL]
		if !ok {
			info = gh.PR{Number: l.Number}
		}
		prs[i] = reviewPRFor(l, info)
	}
	out := make([]transcript.PRLink, 0, len(links))
	for _, p := range review.MergeOrder(prs) {
		out = append(out, links[slices.IndexFunc(links, func(l transcript.PRLink) bool { return l.URL == p.URL })])
	}
	return out
}

func currentAt(st *review.State, url string) {
	st.PRs = review.MergeOrder(st.PRs)
	st.Current = slices.IndexFunc(st.PRs, func(p review.PR) bool { return p.URL == url })
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
		if t := m.prInfos[pr.URL].Title; t != "" {
			text += " " + gray.Render(strings.Join(strings.Fields(textclean.Clean(t)), " "))
		}
		lines = append(lines, ansi.Truncate("  "+selectionBar(i == p.cursor)+text, w, "…"))
	}
	return lines
}

func prInfos(links []transcript.PRLink, known map[string]gh.PR) map[string]gh.PR {
	out := map[string]gh.PR{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, l := range links {
		if info, ok := known[l.URL]; ok {
			out[l.URL] = info
			continue
		}
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			if info, err := gh.Info(url); err == nil {
				mu.Lock()
				out[url] = info
				mu.Unlock()
			}
		}(l.URL)
	}
	wg.Wait()
	return out
}

func reviewPRFor(link transcript.PRLink, info gh.PR) review.PR {
	return review.PR{Number: info.Number, URL: link.URL, Repo: link.Repo, Title: info.Title, Base: info.Base, Head: info.Head}
}

func (m *model) reviewPR(r deck.Row, prs []transcript.PRLink, chosen transcript.PRLink) tea.Cmd {
	if m.startingReview[r.SessionID] {
		m.hint = "Review is starting"
		return nil
	}
	id, _, ok := m.reviewOf(r.SessionID)
	if !ok {
		return m.startReview(r, prs, chosen)
	}
	home, known := m.home, maps.Clone(m.prInfos)
	save := func() tea.Msg {
		_, err := review.Update(home, id, func(st *review.State) bool {
			if !slices.ContainsFunc(st.PRs, func(p review.PR) bool { return p.URL == chosen.URL }) {
				info, ok := prInfos([]transcript.PRLink{chosen}, known)[chosen.URL]
				if !ok {
					return false
				}
				st.PRs = append(st.PRs, reviewPRFor(chosen, info))
			}
			currentAt(st, chosen.URL)
			return true
		})
		if err != nil {
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
	id, source string
	err        error
}

func (m *model) startReview(r deck.Row, prs []transcript.PRLink, chosen transcript.PRLink) tea.Cmd {
	id := uuid.NewV4().String()
	m.setAccount(id, m.state.AccountFor(r.SessionID))
	account := m.launchAccount(id, m.state.AccountFor(id))
	ops, home, known := m.ops, m.home, maps.Clone(m.prInfos)
	if m.startingReview == nil {
		m.startingReview = map[string]bool{}
	}
	m.startingReview[r.SessionID] = true
	m.hint = "Starting review"
	return func() tea.Msg {
		fail := func(err error) tea.Msg {
			os.RemoveAll(review.Dir(home, id))
			return reviewFailedMsg{id: id, source: r.SessionID, err: err}
		}
		infos := prInfos(prs, known)
		if _, ok := infos[chosen.URL]; !ok {
			_, err := gh.Info(chosen.URL)
			return fail(fmt.Errorf("PR #%d: %w", chosen.Number, err))
		}
		st := review.State{Source: r.SessionID, Cwd: r.Cwd}
		for _, p := range prs {
			info, ok := infos[p.URL]
			if !ok {
				continue
			}
			st.PRs = append(st.PRs, reviewPRFor(p, info))
		}
		currentAt(&st, chosen.URL)
		pr, _ := st.PR()
		src, err := review.Resolve(st.Cwd, pr)
		if review.NeedsFetch(err) {
			review.Fetch(st.Cwd, pr)
			src, err = review.Resolve(st.Cwd, pr)
		}
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
	delete(m.state.Accounts, msg.id)
	delete(m.state.Fallbacks, msg.id)
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
