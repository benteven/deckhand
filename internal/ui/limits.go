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
			delete(m.state.Limits, name)
			changed = true
		}
	}
	var moved, back []deck.Row
	from, to := "", ""
	for _, r := range m.liveRows() {
		if !limitFresh(r) || m.resuming[r.SessionID] || m.switches[r.PaneID] {
			continue
		}
		acct := m.paneAccount[r.PaneID]
		if until := r.Summary.Limit.Resets; until.After(now) && until.After(m.state.Limits[acct]) {
			if m.state.Limits == nil {
				m.state.Limits = map[string]time.Time{}
			}
			m.state.Limits[acct] = until
			changed = true
		}
		if _, limited := m.limitedUntil(acct, now); !limited {
			continue
		}
		next, ok := m.freeAccount(acct, now)
		if !ok {
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
		return nil
	}
	cmds := []tea.Cmd{m.saveState()}
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
