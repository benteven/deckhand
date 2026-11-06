package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/benteven/deckhand/internal/accounts"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/tmux"
)

type accountDialog struct {
	row      deck.Row
	hasRow   bool
	list     []accounts.Account
	unshared map[string][]string
	cursor   int
	naming   bool
	removing string
	input    textinput.Model
}

type accountRemovedMsg struct {
	name string
	err  error
}

func (m *model) openAccounts() tea.Cmd {
	if m.loginPane != "" {
		m.hint = "Finish the login first"
		return tea.Sequence(m.show(m.loginPane, "", false), m.focus(tmux.Slot))
	}
	d := &accountDialog{list: accounts.List(m.home, m.claudeHome), unshared: map[string][]string{}}
	for _, a := range d.list[1:] {
		d.unshared[a.Name] = accounts.Unshared(m.home, m.claudeHome, a.Name)
	}
	if r, ok := m.current(); ok && (r.Kind == deck.Live || r.Kind == deck.Suspended) {
		d.row, d.hasRow = r, true
	}
	current := m.state.Account
	if d.hasRow {
		current = m.rowAccount(d.row)
	}
	d.cursor = max(accounts.Index(d.list, current), 0)
	m.accounts = d
	return m.zoom(true)
}

func (m *model) rowAccount(r deck.Row) string {
	switch r.Kind {
	case deck.Live:
		return m.paneAccount[r.PaneID]
	case deck.Suspended, deck.Stale:
		return m.state.AccountFor(r.SessionID)
	}
	return ""
}

func (m *model) accountTag(r deck.Row) string {
	cur := m.rowAccount(r)
	if r.Kind == deck.Live && m.switches[r.PaneID] {
		return accounts.Label(cur) + " → " + accounts.Label(m.state.AccountFor(r.SessionID))
	}
	return ""
}

func (m *model) accountUsers(name string) int {
	n := 0
	for _, r := range m.liveRows() {
		if m.paneAccount[r.PaneID] == name || m.state.AccountFor(r.SessionID) == name {
			n++
		}
	}
	return n
}

func (m *model) accountsFooter(text string, w int) string {
	if m.hint != "" {
		return yellow.Render(ansi.Truncate(m.hint, w, "…"))
	}
	return gray.Render(ansi.Truncate(text, w, ""))
}

func (m *model) accountsView(w, h int) string {
	d := m.accounts
	if d.naming {
		body := []string{
			normal.Render(ansi.Truncate("Name for the new account:", w, "…")),
			"",
			ansi.Truncate(selectionBar(true)+d.input.View(), w, ""),
			"",
			gray.Render(ansi.Truncate("Use a-z, 0-9, - and _. Then log in with that account.", w, "…")),
		}
		return page(body, m.accountsFooter("enter log in  esc cancel", w), h)
	}
	if d.removing != "" {
		body := []string{
			normal.Render(ansi.Truncate(`Remove account "`+d.removing+`"? (y/n)`, w, "…")),
			"",
			gray.Render(ansi.Truncate("The deck logs it out and moves its folder to the Trash.", w, "…")),
			gray.Render(ansi.Truncate("Transcripts stay in ~/.claude.", w, "…")),
		}
		return page(body, gray.Render(ansi.Truncate("y remove  n cancel", w, "")), h)
	}
	title := "Accounts"
	if d.hasRow {
		title = `Account for "` + d.row.Name + `"`
	}
	body := []string{normal.Render(ansi.Truncate(title, w, "…")), ""}
	col, now := 0, time.Now()
	for _, a := range d.list {
		col = max(col, ansi.StringWidth(accounts.Label(a.Name)))
	}
	for i, a := range d.list {
		label := accounts.Label(a.Name)
		email := gray.Render(a.Email)
		switch {
		case a.Email != "":
		case a.Name == "":
			email = gray.Render("~/.claude")
		default:
			email = yellow.Render("not logged in")
		}
		var tags []string
		if d.hasRow && a.Name == m.rowAccount(d.row) {
			tags = append(tags, "this session")
		}
		if a.Name == m.state.Account {
			tags = append(tags, "new sessions")
		}
		line := selectionBar(i == d.cursor) + m.accountStyle(a.Name).Render(label) + strings.Repeat(" ", col+2-ansi.StringWidth(label)) + email
		if len(tags) > 0 {
			line += "  " + orange.Render(strings.Join(tags, ", "))
		}
		if t, ok := m.limitedUntil(a.Name, now); ok {
			line += "  " + red.Render("limit until "+resetLabel(t, now))
		}
		body = append(body, ansi.Truncate(line, w, "…"))
		if u := d.unshared[a.Name]; len(u) > 0 {
			body = append(body, ansi.Truncate("    "+yellow.Render("not shared with ~/.claude: "+strings.Join(u, ", ")), w, "…"))
		}
	}
	body = append(body, ansi.Truncate(selectionBar(d.cursor == len(d.list))+gray.Render("+ add account"), w, ""))
	return page(body, m.accountsFooter(d.hint(), w), h)
}

func (d *accountDialog) hint() string {
	if d.cursor == len(d.list) {
		return "enter add  esc cancel"
	}
	a := d.list[d.cursor]
	if a.Name != "" && a.Email == "" {
		return "enter log in  d remove  esc cancel"
	}
	text := "enter all sessions"
	if d.hasRow {
		text = "enter this session  A all sessions"
	}
	if a.Name != "" {
		text += "  d remove"
	}
	return text + "  esc cancel"
}

func (m *model) accountsKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.accounts
	switch {
	case d.naming:
		return m.namingKey(msg)
	case d.removing != "":
		name := d.removing
		switch msg.String() {
		case "y":
			m.accounts = nil
			home := m.home
			return tea.Sequence(m.zoom(false), func() tea.Msg {
				return accountRemovedMsg{name: name, err: deck.RemoveAccount(home, name)}
			})
		case "n", "esc":
			d.removing = ""
		}
		return nil
	}
	switch msg.String() {
	case "up", "k":
		d.cursor = max(d.cursor-1, 0)
	case "down", "j":
		d.cursor = min(d.cursor+1, len(d.list))
	case "esc", "q":
		m.accounts = nil
		return m.zoom(false)
	case "d":
		if d.cursor == len(d.list) || d.list[d.cursor].Name == "" {
			return nil
		}
		name := d.list[d.cursor].Name
		if n := m.accountUsers(name); n > 0 {
			m.hint = fmt.Sprintf("%s is used by %s, switch them first", name, sessionCount(n))
			return nil
		}
		d.removing = name
	case "enter", "A":
		if d.cursor == len(d.list) {
			if msg.String() == "A" {
				return nil
			}
			d.naming = true
			d.input = textinput.New()
			d.input.Prompt = ""
			return d.input.Focus()
		}
		a := d.list[d.cursor]
		if !accounts.LoggedIn(m.home, a.Name) {
			m.accounts = nil
			return tea.Sequence(m.zoom(false), m.login(a.Name))
		}
		m.accounts = nil
		if msg.String() == "A" || !d.hasRow {
			return tea.Sequence(m.zoom(false), m.switchAll(a.Name))
		}
		return tea.Sequence(m.zoom(false), m.switchOne(d.row, a.Name))
	}
	return nil
}

func (m *model) namingKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.accounts
	switch msg.String() {
	case "esc":
		d.naming = false
		return nil
	case "enter":
		name := strings.TrimSpace(d.input.Value())
		switch {
		case !accounts.ValidName(name):
			m.hint = "Use a-z, 0-9, - and _ in the name, not default"
			return nil
		case accounts.Exists(m.home, name):
			m.hint = name + " exists already"
			return nil
		}
		m.accounts = nil
		return tea.Sequence(m.zoom(false), m.login(name))
	}
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return cmd
}

func (m *model) accountRemoved(msg accountRemovedMsg) tea.Cmd {
	if msg.err != nil {
		m.err = msg.err
		return nil
	}
	if m.state.Account == msg.name {
		m.state.Account = ""
	}
	for id, a := range m.state.Accounts {
		if a == msg.name {
			delete(m.state.Accounts, id)
		}
	}
	for id, a := range m.state.Fallbacks {
		if a == msg.name {
			delete(m.state.Fallbacks, id)
		}
	}
	delete(m.state.Limits, msg.name)
	m.hint = "Removed " + msg.name
	return m.saveState()
}

func (m *model) switchOne(r deck.Row, name string) tea.Cmd {
	if m.state.Accounts == nil {
		m.state.Accounts = map[string]string{}
	}
	m.state.Accounts[r.SessionID] = name
	delete(m.state.Fallbacks, r.SessionID)
	if r.Kind != deck.Live {
		m.hint = fmt.Sprintf("%s resumes with %s", r.Name, accounts.Label(name))
		return m.saveState()
	}
	return tea.Batch(m.saveState(), m.queueSwitch([]deck.Row{r}, 0))
}

func (m *model) switchAll(name string) tea.Cmd {
	m.state.Account = name
	m.state.Accounts = nil
	m.state.Fallbacks = nil
	elsewhere := 0
	for _, r := range m.active {
		if r.Kind == deck.Elsewhere {
			elsewhere++
		}
	}
	return tea.Batch(m.saveState(), m.queueSwitch(m.liveRows(), elsewhere))
}

func waitsForIdle(r deck.Row) bool {
	return r.Busy || r.Asking
}

func (m *model) queueSwitch(rows []deck.Row, elsewhere int) tea.Cmd {
	if m.switches == nil {
		m.switches = map[string]bool{}
	}
	n, waiting := 0, 0
	for _, r := range rows {
		if m.paneAccount[r.PaneID] != m.state.AccountFor(r.SessionID) {
			m.switches[r.PaneID] = true
			n++
			if waitsForIdle(r) {
				waiting++
			}
		}
	}
	var parts []string
	switch {
	case n == 0:
		parts = append(parts, "Already on that account")
	case waiting > 0:
		parts = append(parts, fmt.Sprintf("Switching %s, %d when idle", sessionCount(n), waiting))
	default:
		parts = append(parts, "Switching "+sessionCount(n))
	}
	if elsewhere > 0 {
		parts = append(parts, fmt.Sprintf("%d elsewhere not switched", elsewhere))
	}
	m.hint = strings.Join(parts, "; ")
	return m.advanceSwitches()
}

func (m *model) advanceSwitches() tea.Cmd {
	var cmds []tea.Cmd
	for pane := range m.switches {
		r, ok := m.rowInPane(pane)
		switch {
		case !ok || m.paneAccount[pane] == m.state.AccountFor(r.SessionID):
			delete(m.switches, pane)
		case !waitsForIdle(r) && !m.resuming[r.SessionID] && m.handoffs[pane] == nil:
			delete(m.switches, pane)
			cmds = append(cmds, m.restartRow(r))
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) login(name string) tea.Cmd {
	exe, err := os.Executable()
	if err != nil {
		m.err = err
		return nil
	}
	ops := m.ops
	return func() tea.Msg {
		pane, err := ops.Login(exe, name)
		return loginMsg{pane: pane, name: name, err: err}
	}
}

type loginMsg struct {
	pane, name string
	err        error
}

func (m *model) loginStarted(msg loginMsg) tea.Cmd {
	if msg.err != nil {
		m.err = msg.err
		return nil
	}
	m.loginPane, m.loginName = msg.pane, msg.name
	return tea.Sequence(m.show(msg.pane, "", false), m.focus(tmux.Slot))
}

func (m *model) endLogin(dead bool, gone bool) tea.Cmd {
	if m.loginPane == "" || (!dead && !gone) {
		return nil
	}
	pane := m.loginPane
	m.loginPane = ""
	m.hint = m.loginName + " is not logged in"
	if accounts.LoggedIn(m.home, m.loginName) {
		m.hint = m.loginName + " is logged in, press A to use it"
	}
	cmds := []tea.Cmd{m.showSelected(), m.focus(tmux.ListPane)}
	if !gone {
		cmds = append(cmds, m.tmux("kill-pane", "-t", pane))
	}
	return tea.Sequence(cmds...)
}
