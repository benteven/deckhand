package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/deck"
)

const requestMemory = 2 * time.Minute

type listLine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Folder  string `json:"folder"`
	State   string `json:"state"`
	Need    string `json:"need,omitempty"`
	Message string `json:"message,omitempty"`
}

type searchLine struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Folder string `json:"folder"`
	Last   string `json:"last"`
	Match  string `json:"match"`
}

func (m *model) handleRequests(pending []ctl.Request) tea.Cmd {
	now := time.Now()
	if m.requests == nil {
		m.requests = map[string]time.Time{}
	}
	waiting := map[string]bool{}
	var cmds []tea.Cmd
	for _, req := range pending {
		waiting[req.ID] = true
		if _, seen := m.requests[req.ID]; seen {
			continue
		}
		m.requests[req.ID] = now
		if strings.HasPrefix(req.Op, "sub-") {
			cmds = append(cmds, m.subRequest(req))
			continue
		}
		cmds = append(cmds, m.handleRequest(req))
	}
	for id, at := range m.requests {
		if !waiting[id] && now.Sub(at) > requestMemory {
			delete(m.requests, id)
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) handleRequest(req ctl.Request) tea.Cmd {
	fail := func(format string, a ...any) tea.Cmd {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf(format, a...)})
	}
	if !m.ops.Cfg.Assistant || req.Caller != m.state.Assistant.ID {
		return fail("only the assistant can do this")
	}
	switch req.Op {
	case "list":
		return m.answer(req.ID, ctl.Reply{Out: m.listOut()})
	case "search":
		return m.searchRequest(req)
	case "start":
		for _, r := range m.active {
			if r.Name == req.Name && (r.Kind == deck.Live || r.Kind == deck.Elsewhere) {
				return fail("a session named %s is running", req.Name)
			}
		}
		account := req.Account
		if account == "" {
			account = m.state.Account
		}
		id := uuid.NewV4().String()
		return forRequest(m.start(id, req.Folder, req.Name, account, req.Text), req.ID, id)
	case "resume", "send", "handoff", "close":
	default:
		return fail("unknown request %s", req.Op)
	}
	r, ok := m.rowByID(req.Target)
	if !ok {
		return fail("no session %s", req.Target)
	}
	if req.Op == "resume" {
		switch {
		case r.Kind == deck.Elsewhere:
			return fail("%s runs in another terminal", r.Name)
		case r.Kind == deck.Live || m.resuming[r.SessionID]:
			return fail("%s is already running", r.Name)
		}
		m.resuming[r.SessionID] = true
		delete(m.ending, r.SessionID)
		return forRequest(m.resume(r), req.ID, "resumed")
	}
	if (req.Op == "handoff" || req.Op == "close") && r.SessionID == m.state.Assistant.ID {
		return fail("the assistant cannot do this to itself")
	}
	if r.Kind != deck.Live || m.resuming[r.SessionID] {
		return fail("%s is not running", r.Name)
	}
	if req.Op == "close" {
		return m.suspend(r, req.ID)
	}
	switch {
	case m.in.Events[r.SessionID].Menu():
		return fail("%s shows a menu; ask the user", r.Name)
	case m.slot == r.PaneID && !m.focused:
		return fail("the user is in %s now; try again later", r.Name)
	case m.handoffs[r.PaneID] != nil:
		return fail("%s is handing off; try again later", r.Name)
	}
	if req.Op == "send" {
		return m.typeAndAnswer(r.PaneID, "[via assistant] "+req.Text, req.ID, "sent")
	}
	if why := m.handoffRefusal(r); why != "" {
		return fail("%s", why)
	}
	prompt, err := m.beginHandoff(r)
	if err != nil {
		return fail("%s", err)
	}
	return m.typeAndAnswer(r.PaneID, prompt, req.ID, "handed off")
}

func (m *model) answer(id string, rep ctl.Reply) tea.Cmd {
	home := m.home
	return func() tea.Msg {
		if err := ctl.Answer(home, id, rep); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func forRequest(cmd tea.Cmd, id, out string) tea.Cmd {
	return func() tea.Msg {
		msg, _ := cmd().(startedMsg)
		msg.req, msg.out = id, out
		return msg
	}
}

func (m *model) typeAndAnswer(pane, text, id, out string) tea.Cmd {
	typing, home := m.typeIn(pane, text), m.home
	return func() tea.Msg {
		msg := typing()
		rep := ctl.Reply{Out: out}
		if e, ok := msg.(errMsg); ok {
			rep = ctl.Reply{Err: e.err.Error()}
		}
		if err := ctl.Answer(home, id, rep); err != nil {
			return errMsg{err}
		}
		return msg
	}
}

func (m *model) listOut() string {
	var lines []string
	for _, r := range slices.Concat(m.active, m.suspended) {
		ev := m.in.Events[r.SessionID]
		line := listLine{ID: r.SessionID, Name: r.Name, Folder: r.Cwd, State: rowState(r), Need: ev.Need, Message: ev.Message}
		data, err := json.Marshal(line)
		if err == nil {
			lines = append(lines, string(data))
		}
	}
	return strings.Join(lines, "\n")
}

func rowState(r deck.Row) string {
	switch {
	case r.Kind == deck.Suspended || r.Kind == deck.Stale:
		return "suspended"
	case limitFresh(r):
		return "limit"
	case r.Busy:
		return "working"
	}
	return "waiting"
}

func (m *model) searchRequest(req ctl.Request) tea.Cmd {
	index, home, rows := m.index, m.home, slices.Clone(m.suspended)
	named := map[string]bool{}
	for _, r := range deck.Filter(rows, req.Text) {
		named[r.SessionID] = true
	}
	return func() tea.Msg {
		terms := strings.Fields(strings.ToLower(req.Text))
		var lines []string
		for _, r := range rows {
			match, hit := "", false
			if r.TranscriptPath != "" {
				match, hit = index.Search(r.TranscriptPath, terms)
			}
			if !hit && !named[r.SessionID] {
				continue
			}
			data, err := json.Marshal(searchLine{ID: r.SessionID, Name: r.Name, Folder: r.Cwd, Last: r.LastInteraction.Format(time.RFC3339), Match: match})
			if err == nil {
				lines = append(lines, string(data))
			}
		}
		if err := ctl.Answer(home, req.ID, ctl.Reply{Out: strings.Join(lines, "\n")}); err != nil {
			return errMsg{err}
		}
		return nil
	}
}
