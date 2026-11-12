package ui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"uuid"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/live"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/tmux"
)

type subCreatedMsg struct {
	req               ctl.Request
	dir, repo, branch string
	err               error
}

type cleanupMsg struct{ id string }

func (m *model) subRequest(req ctl.Request) tea.Cmd {
	fail := func(format string, a ...any) tea.Cmd {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf(format, a...)})
	}
	switch req.Op {
	case "sub-start":
		if _, ok := m.state.SubOf(req.Caller); ok {
			return fail("a subsession cannot start subsessions")
		}
		i := slices.IndexFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == req.Caller })
		if i < 0 {
			return fail("the parent session is not active in deckhand")
		}
		if !ctl.ValidName(req.Name) {
			return fail("use a short name: a-z, 0-9 and -")
		}
		if m.nameRunning(req.Name) {
			return fail("a session named %s is running", req.Name)
		}
		cwd := m.state.Active[i].Cwd
		return func() tea.Msg {
			dir, repo, branch, err := ctl.Create(cwd, req.Name)
			return subCreatedMsg{req: req, dir: dir, repo: repo, branch: branch, err: err}
		}
	case "sub-list":
		var lines []string
		for _, sub := range m.state.Subs {
			if sub.Parent != req.Caller {
				continue
			}
			state := "stopped"
			if r, ok := m.rowByID(sub.ID); ok && r.Kind == deck.Live {
				state = "running"
			}
			lines = append(lines, sub.Name+"\t"+state+"\t"+sub.Dir)
		}
		return m.answer(req.ID, ctl.Reply{Out: strings.Join(lines, "\n")})
	case "sub-close":
		assistant := m.ops.Cfg.Assistant && req.Caller == m.state.Assistant.ID
		var sub store.Subsession
		for _, s := range m.state.Subs {
			if s.Name == req.Name && (assistant || s.Parent == req.Caller) {
				sub = s
			}
		}
		if sub.ID == "" {
			return fail("no subsession named %s", req.Name)
		}
		r, ok := m.rowByID(sub.ID)
		switch {
		case ok && r.Kind == deck.Elsewhere:
			return fail("%s runs in another terminal", r.Name)
		case ok && r.Kind == deck.Live && !m.resuming[sub.ID]:
			return m.suspend(r, req.ID)
		case ok && m.resuming[sub.ID]:
			return fail("%s is starting; try again later", r.Name)
		}
		if m.helperRole(sub.ID) {
			m.state.Subs = slices.DeleteFunc(m.state.Subs, func(s store.Subsession) bool { return s.ID == sub.ID })
			return tea.Batch(m.saveState(), m.answer(req.ID, ctl.Reply{Out: "closed"}))
		}
		if _, err := os.Stat(ctl.Worktree(sub.Repo, sub.Name)); err != nil {
			return fail("the worktree %s was removed", ctl.Worktree(sub.Repo, sub.Name))
		}
		return tea.Batch(m.answer(req.ID, ctl.Reply{Out: "closed"}), m.queueCleanup(sub.ID))
	}
	return fail("unknown request %s", req.Op)
}

func (m *model) nameRunning(name string) bool {
	for _, r := range m.active {
		if r.Name == name && (r.Kind == deck.Live || r.Kind == deck.Elsewhere) {
			return true
		}
	}
	return slices.ContainsFunc(m.in.Live, func(l live.Session) bool { return l.Name == name })
}

func (m *model) subCreated(msg subCreatedMsg) tea.Cmd {
	req := msg.req
	if msg.err != nil {
		return m.answer(req.ID, ctl.Reply{Err: msg.err.Error()})
	}
	id := uuid.NewV4().String()
	account := m.state.AccountFor(req.Caller)
	m.state.Subs = append(m.state.Subs, store.Subsession{ID: id, Parent: req.Caller, Name: req.Name, Dir: msg.dir, Repo: msg.repo, Branch: msg.branch})
	if m.state.Accounts == nil {
		m.state.Accounts = map[string]string{}
	}
	m.state.Accounts[id] = account
	prompt := ctl.FirstPrompt(req.Parent, msg.dir, msg.branch, req.Text)
	out := fmt.Sprintf("started %s in %s on branch %s", req.Name, msg.dir, msg.branch)
	return tea.Batch(m.saveState(), forRequest(m.start(id, msg.dir, req.Name, account, prompt), req.ID, out))
}

func (m *model) queueCleanup(id string) tea.Cmd {
	if i, ok := m.taskOf(id); ok && m.tasks[i].Status == "done" {
		return nil
	}
	if !slices.Contains(m.state.Cleanups, id) {
		m.state.Cleanups = append(m.state.Cleanups, id)
	}
	return tea.Batch(m.saveState(), m.nextCleanup())
}

func (m *model) nextCleanup() tea.Cmd {
	if m.cleanup != nil {
		return nil
	}
	for len(m.state.Cleanups) > 0 {
		if sub, ok := m.state.SubOf(m.state.Cleanups[0]); ok {
			m.cleanup = &cleanupDialog{sub: sub, lastKey: time.Now()}
			return tea.Sequence(m.zoom(true), m.focus(tmux.ListPane))
		}
		m.state.Cleanups = m.state.Cleanups[1:]
	}
	return nil
}
