package ui

import (
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/accounts"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/usage"
)

const recheckEvery = 30 * time.Minute

type limitCheckMsg struct {
	account string
	ok      bool
	err     error
}

func limitFresh(r deck.Row) bool {
	l := r.Summary.Limit
	return r.Kind == deck.Live && !l.At.IsZero() && !r.Started.IsZero() && l.At.After(r.Started)
}

func chooseAccount(name string, loggedIn []string, limits map[string]time.Time, now time.Time) (string, error) {
	if name == "default" {
		name = ""
	}
	if !slices.Contains(loggedIn, name) {
		return "", fmt.Errorf("no logged-in account %s", accounts.Label(name))
	}
	if t, ok := limits[name]; ok && t.After(now) {
		return "", fmt.Errorf("%s is at its limit until %s; leave out --account to use a free account", accounts.Label(name), t.Format("Mon 15:04"))
	}
	return name, nil
}

func (m *model) limitedUntil(account string, now time.Time) (time.Time, bool) {
	t := m.state.Limits[account]
	if u, ok := m.usage[account].LimitedUntil(now); ok && u.After(t) {
		t = u
	}
	return t, t.After(now)
}

func (m *model) freeAccount(skip string, now time.Time) (string, bool) {
	var names []string
	for _, name := range append([]string{m.state.Account}, m.loggedIn()...) {
		if name != skip && slices.Contains(m.loggedIn(), name) {
			names = append(names, name)
		}
	}
	return usage.Pick(names, m.usage, func(name string) bool {
		_, limited := m.limitedUntil(name, now)
		return limited
	}, now)
}

func (m *model) setAccount(id, name string) {
	if name == m.state.Account {
		delete(m.state.Accounts, id)
		return
	}
	if m.state.Accounts == nil {
		m.state.Accounts = map[string]string{}
	}
	m.state.Accounts[id] = name
}

func (m *model) fallBack(id, from, to string) {
	m.setAccount(id, to)
	if m.state.Fallbacks == nil {
		m.state.Fallbacks = map[string]string{}
	}
	if _, ok := m.state.Fallbacks[id]; !ok {
		m.state.Fallbacks[id] = from
	}
}

func answered(r deck.Row, since time.Time) bool {
	s := r.Summary
	return r.Kind == deck.Live && s.Limit.At.IsZero() && s.Model != "<synthetic>" && s.LastUsed.After(since)
}

func (m *model) clearLimit(acct string, now time.Time) {
	delete(m.state.Limits, acct)
	if m.state.LimitSeen == nil {
		m.state.LimitSeen = map[string]time.Time{}
	}
	m.state.LimitSeen[acct] = now
}

func (m *model) nudge(r deck.Row) tea.Cmd {
	if m.nudged == nil {
		m.nudged = map[string]time.Time{}
	}
	at := r.Summary.Limit.At
	if r.Busy || r.Asking || r.PaneID == m.slot || m.typing[r.PaneID] || m.nudged[r.SessionID].Equal(at) {
		return nil
	}
	m.nudged[r.SessionID] = at
	return m.typeOrHold(r, "continue", func() {})
}

func (m *model) recheck(skip string, now time.Time) []tea.Cmd {
	if m.checks == nil {
		m.checks = map[string]time.Time{}
	}
	var cmds []tea.Cmd
	for name := range m.state.Limits {
		if name == skip || !slices.Contains(m.loggedIn(), name) || now.Sub(m.checks[name]) < recheckEvery {
			continue
		}
		m.checks[name] = now
		ops, account := m.ops, name
		cmds = append(cmds, func() tea.Msg {
			ok, err := ops.CheckAccount(account)
			return limitCheckMsg{account: account, ok: ok, err: err}
		})
	}
	return cmds
}

func (m *model) limitChecked(msg limitCheckMsg) tea.Cmd {
	if msg.err != nil || !msg.ok {
		return nil
	}
	m.clearLimit(msg.account, time.Now())
	m.hint = accounts.Label(msg.account) + " works again"
	return tea.Batch(m.saveState(), m.advanceLimits(time.Now()))
}

func (m *model) launchAccount(id, account string) string {
	now := time.Now()
	if _, limited := m.limitedUntil(account, now); !limited {
		return account
	}
	to, ok := m.freeAccount(account, now)
	if !ok {
		return account
	}
	m.fallBack(id, account, to)
	return to
}

func (m *model) advanceLimits(now time.Time) tea.Cmd {
	changed := false
	for name, t := range m.state.Limits {
		if !t.After(now) {
			m.clearLimit(name, now)
			changed = true
		}
	}
	held := map[string]bool{}
	for _, r := range m.liveRows() {
		if acct := m.paneAccount[r.PaneID]; limitFresh(r) && !r.Summary.Limit.At.Before(m.state.LimitSeen[acct]) {
			held[acct] = true
		}
	}
	for _, r := range m.liveRows() {
		acct := m.paneAccount[r.PaneID]
		if _, limited := m.state.Limits[acct]; limited && !held[acct] && answered(r, m.state.LimitSeen[acct]) {
			m.clearLimit(acct, now)
			changed = true
		}
	}
	var moved, back []deck.Row
	var cmds []tea.Cmd
	from, to := "", ""
	for _, r := range m.liveRows() {
		if !limitFresh(r) || m.resuming[r.SessionID] || m.switches[r.PaneID] {
			continue
		}
		acct := m.paneAccount[r.PaneID]
		_, limited := m.limitedUntil(acct, now)
		stale := r.Summary.Limit.At.Before(m.state.LimitSeen[acct])
		if stale && !limited {
			cmds = append(cmds, m.nudge(r))
			continue
		}
		if until := r.Summary.Limit.Resets; !stale && until.After(now) && until.After(m.state.Limits[acct]) {
			if m.state.Limits == nil {
				m.state.Limits = map[string]time.Time{}
			}
			if m.state.LimitSeen == nil {
				m.state.LimitSeen = map[string]time.Time{}
			}
			m.state.Limits[acct] = until
			if r.Summary.Limit.At.After(m.state.LimitSeen[acct]) {
				m.state.LimitSeen[acct] = r.Summary.Limit.At
			}
			changed = true
		}
		if _, limited := m.limitedUntil(acct, now); !limited {
			continue
		}
		next, ok := m.freeAccount(acct, now)
		if !ok {
			cmds = append(cmds, m.recheck(acct, now)...)
			continue
		}
		m.fallBack(r.SessionID, acct, next)
		if m.continueAfter == nil {
			m.continueAfter = map[string]bool{}
		}
		m.continueAfter[r.SessionID] = true
		moved = append(moved, r)
		from, to = acct, next
		changed = true
	}
	for id, origin := range m.state.Fallbacks {
		if _, limited := m.limitedUntil(origin, now); limited {
			continue
		}
		if r, ok := m.rowByID(id); ok && r.Kind == deck.Live {
			if r.PaneID == m.slot {
				continue
			}
			back = append(back, r)
		} else if !ok {
			delete(m.state.Fallbacks, id)
			changed = true
			continue
		}
		m.setAccount(id, origin)
		delete(m.state.Fallbacks, id)
		changed = true
	}
	if !changed {
		return tea.Batch(cmds...)
	}
	cmds = append(cmds, m.saveState())
	if len(back) > 0 {
		cmds = append(cmds, m.queueSwitch(back, 0))
	}
	if len(moved) > 0 {
		cmds = append(cmds, m.queueSwitch(moved, 0))
		m.hint = fmt.Sprintf("%s hit its limit, moving %s to %s", accounts.Label(from), sessionCount(len(moved)), accounts.Label(to))
	}
	return tea.Batch(cmds...)
}

func (m *model) rowByID(id string) (deck.Row, bool) {
	for _, rows := range [][]deck.Row{m.active, m.suspended} {
		if i := slices.IndexFunc(rows, func(r deck.Row) bool { return r.SessionID == id }); i >= 0 {
			return rows[i], true
		}
	}
	return deck.Row{}, false
}

func resetLabel(t, now time.Time) string {
	if y1, m1, d1 := t.Date(); y1 == now.Year() && m1 == now.Month() && d1 == now.Day() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}
