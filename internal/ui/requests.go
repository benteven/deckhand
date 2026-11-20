package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/accounts"
	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/typing"
)

const requestMemory = 2 * time.Minute

type listLine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Folder  string `json:"folder"`
	State   string `json:"state"`
	Agent   string `json:"agent"`
	Account string `json:"account"`
	Model   string `json:"model"`
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
		req.Caller = m.callerOf(req)
		if req.Caller == "" {
			cmds = append(cmds, m.answer(req.ID, ctl.Reply{Err: "run this from a deckhand session"}))
			continue
		}
		if strings.HasPrefix(req.Op, "sub-") {
			cmds = append(cmds, m.subRequest(req))
			continue
		}
		if strings.HasPrefix(req.Op, "task-") {
			cmds = append(cmds, m.taskRequest(req))
			continue
		}
		if strings.HasPrefix(req.Op, "project-") {
			cmds = append(cmds, m.projectRequest(req))
			continue
		}
		if strings.HasPrefix(req.Op, "role-") {
			cmds = append(cmds, m.roleRequest(req))
			continue
		}
		if strings.HasPrefix(req.Op, "queue-") {
			cmds = append(cmds, m.queueRequest(req))
			continue
		}
		if req.Op == "msg" {
			cmds = append(cmds, m.msgRequest(req))
			continue
		}
		if strings.HasPrefix(req.Op, "chat-") {
			cmds = append(cmds, m.chatRequest(req))
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

func (m *model) callerOf(req ctl.Request) string {
	if req.Pane == "" {
		return req.Caller
	}
	for id, pane := range m.in.Panes {
		if pane == req.Pane {
			return id
		}
	}
	return ""
}

func (m *model) handleRequest(req ctl.Request) tea.Cmd {
	fail := func(format string, a ...any) tea.Cmd {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf(format, a...)})
	}
	if !m.ops.Cfg.Assistant || req.Caller != m.state.Assistant.ID {
		return fail("only the assistant can do this")
	}
	model := req.Args["model"]
	badModel := model != "" && !slices.Contains(taskModels, model)
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
		codex := req.Args["agent"] == "codex"
		switch {
		case badModel && !codex:
			return fail("model is one of %s", strings.Join(taskModels, ", "))
		case codex && req.Account != "":
			return fail("codex sessions use the default ~/.codex; leave out --account")
		}
		account := m.state.Account
		if codex {
			account = ""
		} else if req.Account != "" {
			a, err := chooseAccount(req.Account, m.loggedIn(), m.state.Limits, time.Now())
			if err != nil {
				return fail("%s", err)
			}
			account = a
		}
		text := req.Text
		if text != "" {
			text = viaAssistant(text)
		}
		id := uuid.NewV4().String()
		if codex {
			m.setCodex(id, req.Folder, req.Name)
			text = strings.TrimSpace(ctl.CodexIntro(m.ops.Exe) + "\n\n" + text)
		}
		m.setModel(id, model)
		start := m.start(id, req.Folder, req.Name, account, text)
		return tea.Batch(m.saveState(), forRequest(start, req.ID, id+" on account "+accounts.Label(m.state.AccountFor(id))))
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
		case badModel && m.state.Agent(r.SessionID) != "codex":
			return fail("model is one of %s", strings.Join(taskModels, ", "))
		case r.Kind == deck.Elsewhere:
			return fail("%s runs in another terminal", r.Name)
		case r.Kind == deck.Live || m.resuming[r.SessionID]:
			return fail("%s is already running", r.Name)
		}
		m.resuming[r.SessionID] = true
		delete(m.ending, r.SessionID)
		m.setModel(r.SessionID, model)
		return tea.Batch(m.saveState(), forRequest(m.resume(r), req.ID, "resumed"))
	}
	if (req.Op == "handoff" || req.Op == "close") && r.SessionID == m.state.Assistant.ID {
		return fail("the assistant cannot do this to itself")
	}
	if req.Op == "handoff" && m.state.Agent(r.SessionID) == "codex" {
		return fail("handoff does not work for codex sessions")
	}
	if r.Kind != deck.Live || m.resuming[r.SessionID] {
		return fail("%s is not running", r.Name)
	}
	if req.Op == "close" {
		if m.userIn(r) {
			return fail("the user is in %s now; try again later", r.Name)
		}
		return m.suspend(r, req.ID)
	}
	if why := m.sendRefusal(r); why != "" {
		return fail("%s", why)
	}
	if req.Op == "send" {
		return m.typeIn(r, viaAssistant(req.Text), m.replyTyped(req.ID, r.Name, "sent"))
	}
	prompt, err := m.beginHandoff(r)
	if err != nil {
		return fail("%s", err)
	}
	return m.typeIn(r, prompt, m.handoffTyped(r.PaneID, m.replyTyped(req.ID, r.Name, "handed off")))
}

func (m *model) sendRefusal(r deck.Row) string {
	switch {
	case r.Kind != deck.Live || m.resuming[r.SessionID]:
		return r.Name + " is not running"
	case m.userIn(r):
		return "the user is in " + r.Name + " now; try again later"
	case r.Asking || m.in.Events[r.SessionID].Menu():
		return r.Name + " shows a menu; ask the user"
	case r.Busy || m.typing[r.PaneID]:
		return r.Name + " is working; try again when it waits"
	case m.handoffs[r.PaneID] != nil:
		return r.Name + " is handing off; try again later"
	}
	return ""
}

func (m *model) msgRequest(req ctl.Request) tea.Cmd {
	fail := func(format string, a ...any) tea.Cmd {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf(format, a...)})
	}
	from, ok := m.rowByID(req.Caller)
	if !ok {
		return fail("the calling session is not in deckhand")
	}
	var named []deck.Row
	for _, r := range m.active {
		if r.SessionID == req.Name {
			named = []deck.Row{r}
			break
		}
		if r.Name == req.Name && r.Kind == deck.Live {
			named = append(named, r)
		}
	}
	switch {
	case len(named) == 0:
		return fail("no running session %s", req.Name)
	case len(named) > 1:
		return fail("several sessions named %s; use the id", req.Name)
	}
	r := named[0]
	if r.SessionID == from.SessionID {
		return fail("this is your own session")
	}
	if why := m.sendRefusal(r); why != "" {
		return fail("%s", why)
	}
	return m.typeIn(r, "[from "+from.Name+"] "+req.Text, m.replyTyped(req.ID, r.Name, "sent"))
}

func (m *model) setCodex(id, cwd, name string) {
	if m.state.Codex == nil {
		m.state.Codex = map[string]store.Codex{}
	}
	m.state.Codex[id] = store.Codex{Cwd: cwd, Name: name, LastUsed: time.Now()}
}

func (m *model) setModel(id, model string) {
	if model == "" {
		return
	}
	if m.state.Models == nil {
		m.state.Models = map[string]string{}
	}
	m.state.Models[id] = model
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

func (m *model) replyTyped(id, name, out string) func(error) tea.Cmd {
	return func(err error) tea.Cmd {
		switch {
		case errors.Is(err, typing.ErrMenu):
			return m.answer(id, ctl.Reply{Err: name + " shows a menu; ask the user"})
		case err != nil:
			return m.answer(id, ctl.Reply{Err: typeError(name, err).Error()})
		}
		return m.answer(id, ctl.Reply{Out: out})
	}
}

func (m *model) listOut() string {
	var lines []string
	listed := map[string]bool{}
	for _, r := range slices.Concat(m.active, m.suspended) {
		if listed[r.SessionID] {
			continue
		}
		listed[r.SessionID] = true
		data, err := json.Marshal(m.listLineOf(r))
		if err == nil {
			lines = append(lines, string(data))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *model) listLineOf(r deck.Row) listLine {
	ev := m.in.Events[r.SessionID]
	return listLine{ID: r.SessionID, Name: r.Name, Folder: r.Cwd, State: rowState(r), Agent: m.state.Agent(r.SessionID), Account: accounts.Label(m.state.AccountFor(r.SessionID)), Model: m.modelLabel(r.SessionID), Need: ev.Need, Message: ev.Message}
}

func (m *model) modelLabel(id string) string {
	if model := m.modelFor(id); model != "" {
		return model
	}
	return "default"
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

const viaMark = "[via assistant]"

func viaAssistant(text string) string {
	for {
		rest, ok := strings.CutPrefix(strings.TrimSpace(text), viaMark)
		if !ok {
			return viaMark + " " + strings.TrimSpace(text)
		}
		text = rest
	}
}
