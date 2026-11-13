package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/roles"
)

const queueGrace = 3 * time.Second

type queueNamedMsg struct {
	req  ctl.Request
	name string
	err  error
}

func (m *model) queueRequest(req ctl.Request) tea.Cmd {
	switch req.Op {
	case "queue-list":
		return m.answer(req.ID, ctl.Reply{Out: m.queueListOut()})
	case "queue-take", "queue-give":
		if req.Name != "" {
			return m.queueApply(req, req.Name)
		}
		return m.queueName(req)
	}
	return m.answer(req.ID, ctl.Reply{Err: "unknown request " + req.Op})
}

func (m *model) queueName(req ctl.Request) tea.Cmd {
	home, role, repo, cwd := m.home, m.state.Roles[req.Caller], "", ""
	if sub, ok := m.state.SubOf(req.Caller); ok {
		repo = sub.Repo
	}
	if r, ok := m.rowByID(req.Caller); ok {
		cwd = r.Cwd
	}
	return func() tea.Msg {
		if repo == "" && cwd != "" {
			repo = ctl.TopLevel(cwd)
		}
		if role != "" {
			if r, err := roles.Find(home, repo, role); err == nil && r.Queue != "" {
				return queueNamedMsg{req: req, name: r.Queue}
			}
		}
		if repo == "" {
			return queueNamedMsg{req: req, err: errors.New("this session is not in a repo; give the queue name")}
		}
		return queueNamedMsg{req: req, name: filepath.Base(repo)}
	}
}

func (m *model) queueNamed(msg queueNamedMsg) tea.Cmd {
	if msg.err != nil {
		return m.answer(msg.req.ID, ctl.Reply{Err: msg.err.Error()})
	}
	return m.queueApply(msg.req, msg.name)
}

func (m *model) queueApply(req ctl.Request, name string) tea.Cmd {
	id := req.Caller
	if req.Op == "queue-give" {
		if !slices.Contains(m.state.Queues[name], id) {
			return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf("you are not in queue %s", name)})
		}
		m.leaveQueue(name, id)
		return tea.Batch(m.saveState(), m.deliverTurns(), m.answer(req.ID, ctl.Reply{Out: "given"}))
	}
	q := m.state.Queues[name]
	i := slices.Index(q, id)
	if i < 0 {
		if m.state.Queues == nil {
			m.state.Queues = map[string][]string{}
		}
		i = len(q)
		m.state.Queues[name] = append(slices.Clone(q), id)
	}
	out := "yours"
	if i > 0 {
		out = fmt.Sprintf("waiting, %d ahead", i)
	}
	return tea.Batch(m.saveState(), m.answer(req.ID, ctl.Reply{Out: out}))
}

func (m *model) leaveQueue(name, id string) {
	q := m.state.Queues[name]
	i := slices.Index(q, id)
	if i < 0 {
		return
	}
	q = slices.Delete(slices.Clone(q), i, i+1)
	if len(q) == 0 {
		delete(m.state.Queues, name)
		return
	}
	m.state.Queues[name] = q
	if i == 0 {
		if m.turns == nil {
			m.turns = map[string]string{}
		}
		m.turns[q[0]] = name
	}
}

func (m *model) dropFromQueues(id string) tea.Cmd {
	delete(m.turns, id)
	changed := false
	for name, q := range m.state.Queues {
		if slices.Contains(q, id) {
			m.leaveQueue(name, id)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return tea.Batch(m.saveState(), m.deliverTurns())
}

func (m *model) pruneQueues(now time.Time) tea.Cmd {
	queued := map[string]bool{}
	var gone []string
	for _, q := range m.state.Queues {
		for _, id := range q {
			queued[id] = true
			if m.running(id) {
				delete(m.stoppedAt, id)
				continue
			}
			if m.stoppedAt == nil {
				m.stoppedAt = map[string]time.Time{}
			}
			since, seen := m.stoppedAt[id]
			if !seen {
				m.stoppedAt[id] = now
			} else if now.Sub(since) >= queueGrace {
				gone = append(gone, id)
			}
		}
	}
	for id := range m.stoppedAt {
		if !queued[id] {
			delete(m.stoppedAt, id)
		}
	}
	var cmds []tea.Cmd
	for _, id := range gone {
		delete(m.stoppedAt, id)
		cmds = append(cmds, m.dropFromQueues(id))
	}
	return tea.Batch(cmds...)
}

func (m *model) deliverTurns() tea.Cmd {
	var cmds []tea.Cmd
	for id, name := range m.turns {
		if q := m.state.Queues[name]; len(q) == 0 || q[0] != id {
			delete(m.turns, id)
			continue
		}
		r, ok := m.rowByID(id)
		if !ok || !m.typeReady(r) {
			continue
		}
		delete(m.turns, id)
		cmds = append(cmds, m.typeOrHold(r, "[deckhand] your turn in queue "+name, func() {
			if q := m.state.Queues[name]; len(q) > 0 && q[0] == id {
				m.turns[id] = name
			}
		}))
	}
	return tea.Batch(cmds...)
}

func (m *model) queueListOut() string {
	names := make([]string, 0, len(m.state.Queues))
	for name, q := range m.state.Queues {
		if len(q) > 0 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var lines []string
	for _, name := range names {
		q := m.state.Queues[name]
		var waiting []string
		for _, id := range q[1:] {
			waiting = append(waiting, m.sessionName(id))
		}
		lines = append(lines, name+"\t"+m.sessionName(q[0])+"\t"+strings.Join(waiting, " "))
	}
	return strings.Join(lines, "\n")
}
