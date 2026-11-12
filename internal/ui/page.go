package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/tasks"
	"github.com/benteven/deckhand/internal/tmux"
	"github.com/benteven/deckhand/internal/typing"
	"github.com/benteven/deckhand/internal/web"
)

const screenLines = 30

var errAnswerMenu = errors.New("answer the menu")

type pageMsg struct{ action web.Action }

type webStoppedMsg struct {
	server *web.Server
	err    error
}

type menuShot struct {
	choices []string
	screen  string
}

type pageShotMsg struct {
	menus map[string]menuShot
	chat  []web.ChatLine
}

type pageAnswer struct{ id, text string }

func (m *model) startWeb() tea.Cmd {
	if m.web != nil || m.send == nil {
		return nil
	}
	token, err := web.Token(m.home)
	if err != nil {
		m.err = fmt.Errorf("the task page cannot start: %w", err)
		return nil
	}
	send := m.send
	s := web.New(m.ops.Cfg.WebPort, token, func(a web.Action) { send(pageMsg{a}) })
	m.web, m.published = s, nil
	return func() tea.Msg { return webStoppedMsg{server: s, err: s.ListenAndServe()} }
}

func (m *model) stopWeb() {
	if m.web != nil {
		m.web.Close()
		m.web = nil
	}
	m.pageAnswers = nil
}

func (m *model) webStopped(msg webStoppedMsg) {
	if msg.server == m.web {
		m.web = nil
	}
	if msg.err != nil && !errors.Is(msg.err, http.ErrServerClosed) {
		m.err = fmt.Errorf("the task page cannot start: %w", msg.err)
	}
}

func (m *model) pageTick() tea.Cmd {
	if m.web == nil {
		return nil
	}
	answers := m.deliverAnswers()
	if m.shooting {
		return answers
	}
	panes := map[string]string{}
	for _, t := range m.tasks {
		if t.Status == "done" || t.Session == "" || !m.in.Events[t.Session].Menu() {
			continue
		}
		if r, ok := m.rowByID(t.Session); ok && r.Kind == deck.Live {
			panes[t.Session] = r.PaneID
		}
	}
	m.shooting = true
	t, chatFile := m.ops.T, filepath.Join(m.home, "chat.json")
	return tea.Batch(answers, func() tea.Msg {
		shots := map[string]menuShot{}
		for id, pane := range panes {
			if out, err := t.Run("capture-pane", "-p", "-J", "-t", pane); err == nil {
				shots[id] = menuShot{choices: typing.Choices(out), screen: screenTail(out)}
			}
		}
		var chat []web.ChatLine
		if data, err := os.ReadFile(chatFile); err == nil {
			json.Unmarshal(data, &chat)
		}
		return pageShotMsg{menus: shots, chat: chat}
	})
}

func screenTail(out string) string {
	lines := strings.Split(strings.TrimRight(out, " \n"), "\n")
	return strings.Join(lines[max(len(lines)-screenLines, 0):], "\n")
}

func (m *model) pageShot(msg pageShotMsg) {
	m.shooting = false
	m.menus, m.chat = msg.menus, msg.chat
	m.publishPage()
}

func (m *model) publishPage() {
	if m.web == nil {
		return
	}
	v := m.pageView()
	data, err := json.Marshal(v)
	if err != nil || bytes.Equal(data, m.published) {
		return
	}
	m.published = data
	m.web.Publish(v)
}

func (m *model) pageView() web.View {
	open := slices.DeleteFunc(slices.Clone(m.tasks), func(t tasks.Task) bool { return t.Status == "done" })
	tasks.Sort(open)
	v := web.View{Chat: m.chat, Waiting: m.waitingSessions()}
	for _, t := range open {
		tv := web.TaskView{Task: t}
		if t.Project != "" {
			tv.ProjectName = m.sessionName(t.Project)
		}
		if shot, ok := m.menus[t.Session]; ok && t.Session != "" && m.in.Events[t.Session].Menu() {
			tv.Menu, tv.Screen = shot.choices, shot.screen
		}
		if tasks.Group(t) == 0 {
			v.Count++
		}
		v.Tasks = append(v.Tasks, tv)
	}
	v.Count += len(v.Waiting)
	return v
}

func (m *model) waitingSessions() []web.SessionView {
	var out []web.SessionView
	for _, r := range m.active {
		id := r.SessionID
		if r.Kind != deck.Live || m.isAssistant(id) || m.ending[id] {
			continue
		}
		if _, task := m.taskOf(id); task {
			continue
		}
		if _, sub := m.state.SubOf(id); sub {
			continue
		}
		ev := m.in.Events[id]
		if !waits(ev) {
			continue
		}
		need := strings.Join(strings.Fields(ev.Message), " ")
		if need == "" {
			need = "its turn ended"
		}
		out = append(out, web.SessionView{ID: id, Name: r.Name, Need: need})
	}
	return out
}

func replyTo(a web.Action, err error) tea.Cmd {
	if a.Reply != nil {
		a.Reply <- err
	}
	return nil
}

func (m *model) pageAction(a web.Action) tea.Cmd {
	if m.quitting {
		return replyTo(a, errors.New("deckhand is quitting"))
	}
	switch a.Kind {
	case "answer":
		return m.pageAnswer(a)
	case "choice":
		return m.pageChoice(a)
	case "open":
		return m.pageOpen(a)
	case "done":
		return m.pageDone(a)
	case "chat":
		return replyTo(a, errors.New("the chat is not ready yet"))
	}
	return replyTo(a, fmt.Errorf("unknown action %s", a.Kind))
}

func (m *model) pageTarget(a web.Action) (string, error) {
	if a.Task == "" {
		if a.Session == "" {
			return "", errors.New("give a task or a session")
		}
		if !slices.ContainsFunc(m.waitingSessions(), func(s web.SessionView) bool { return s.ID == a.Session }) {
			return "", errors.New("the session does not wait for you")
		}
		return a.Session, nil
	}
	i := slices.IndexFunc(m.tasks, func(t tasks.Task) bool { return t.ID == a.Task })
	if i < 0 {
		return "", fmt.Errorf("no task %s", a.Task)
	}
	if m.tasks[i].Session == "" {
		return "", fmt.Errorf("task %s has no session", a.Task)
	}
	return m.tasks[i].Session, nil
}

func (m *model) userIn(r deck.Row) bool {
	return m.slot == r.PaneID && !m.focused
}

func (m *model) typeReady(r deck.Row) bool {
	return r.Kind == deck.Live && !m.resuming[r.SessionID] && m.handoffs[r.PaneID] == nil && m.userAway(r) && m.canType(r)
}

func (m *model) pageAnswer(a web.Action) tea.Cmd {
	id, err := m.pageTarget(a)
	if err != nil {
		return replyTo(a, err)
	}
	if strings.TrimSpace(a.Text) == "" {
		return replyTo(a, errors.New("the answer is empty"))
	}
	r, ok := m.rowByID(id)
	if m.in.Events[id].Menu() || (ok && r.Asking) {
		return replyTo(a, errAnswerMenu)
	}
	if !ok || r.Kind != deck.Live {
		return replyTo(a, errors.New("the session is not running"))
	}
	if !m.typeReady(r) {
		m.pageAnswers = append(m.pageAnswers, pageAnswer{id: id, text: a.Text})
		return replyTo(a, nil)
	}
	return m.typeIn(r, a.Text, func(err error) tea.Cmd {
		if errors.Is(err, typing.ErrMenu) {
			err = errAnswerMenu
		} else if err != nil {
			err = typeError(r.Name, err)
		}
		return replyTo(a, err)
	})
}

func (m *model) deliverAnswers() tea.Cmd {
	pending := m.pageAnswers
	m.pageAnswers = nil
	var cmds []tea.Cmd
	for _, p := range pending {
		r, ok := m.rowByID(p.id)
		if !ok || r.Kind != deck.Live {
			m.err = fmt.Errorf("%s stopped before the page answer was typed", m.sessionName(p.id))
			continue
		}
		if !m.typeReady(r) {
			m.pageAnswers = append(m.pageAnswers, p)
			continue
		}
		cmds = append(cmds, m.typeIn(r, p.text, func(err error) tea.Cmd {
			switch {
			case errors.Is(err, typing.ErrMenu):
				m.pageAnswers = append(m.pageAnswers, p)
			case err != nil:
				m.err = typeError(r.Name, err)
			}
			return nil
		}))
	}
	return tea.Batch(cmds...)
}

func (m *model) pageChoice(a web.Action) tea.Cmd {
	id, err := m.pageTarget(a)
	if err != nil {
		return replyTo(a, err)
	}
	if a.Choice < 1 || a.Choice > 9 {
		return replyTo(a, errors.New("the choice is a number from 1 to 9"))
	}
	r, ok := m.rowByID(id)
	if !ok || r.Kind != deck.Live {
		return replyTo(a, errors.New("the session is not running"))
	}
	if !m.in.Events[id].Menu() && !r.Asking {
		return replyTo(a, errors.New("the session shows no menu"))
	}
	if m.userIn(r) || m.typing[r.PaneID] {
		return replyTo(a, errors.New("the session is in use in deckhand; try again soon"))
	}
	t, pane, n := m.ops.T, r.PaneID, a.Choice
	return func() tea.Msg {
		out, err := t.Run("capture-pane", "-p", "-J", "-t", pane)
		if err != nil {
			return replyTo(a, err)
		}
		if len(typing.Choices(out)) < n {
			return replyTo(a, errors.New("the session shows no such choice"))
		}
		_, err = t.Run("send-keys", "-t", pane, strconv.Itoa(n))
		return replyTo(a, err)
	}
}

func (m *model) pageOpen(a web.Action) tea.Cmd {
	id, err := m.pageTarget(a)
	if err != nil {
		return replyTo(a, err)
	}
	r, ok := m.rowByID(id)
	if !ok {
		return replyTo(a, errors.New("the session is not in the list"))
	}
	if m.dialogOpen() {
		return replyTo(a, errors.New("close the dialog in deckhand first"))
	}
	m.agent = ""
	var show tea.Cmd
	if r.Kind == deck.Live {
		show = m.switchTo(r)
	} else {
		mode := modeActive
		if slices.ContainsFunc(m.suspended, func(s deck.Row) bool { return s.SessionID == id }) {
			mode = modeSuspended
		}
		m.setMode(mode)
		m.selected = id
		if !slices.ContainsFunc(m.shown(), func(s deck.Row) bool { return s.SessionID == id }) {
			m.resetSearch()
		}
		m.syncCursor()
		show = tea.Sequence(m.showSelected(), m.focus(tmux.ListPane))
	}
	replyTo(a, nil)
	return tea.Batch(show, m.raiseTerminal())
}

func (m *model) raiseTerminal() tea.Cmd {
	if runtime.GOOS != "darwin" {
		return nil
	}
	t := m.ops.T
	return func() tea.Msg {
		app := os.Getenv("TERM_PROGRAM")
		if app == "" || app == "tmux" {
			out, _ := t.Run("show-environment", "-g", "TERM_PROGRAM")
			app = strings.TrimPrefix(strings.TrimSpace(out), "TERM_PROGRAM=")
		}
		if app == "Apple_Terminal" {
			app = "Terminal"
		}
		if app == "" || app == "tmux" {
			return nil
		}
		if out, err := exec.Command("open", "-a", app).CombinedOutput(); err != nil {
			return errMsg{fmt.Errorf("open -a %s: %w: %s", app, err, strings.TrimSpace(string(out)))}
		}
		return nil
	}
}

func (m *model) pageDone(a web.Action) tea.Cmd {
	i := slices.IndexFunc(m.tasks, func(t tasks.Task) bool { return a.Task != "" && t.ID == a.Task })
	if i < 0 {
		return replyTo(a, fmt.Errorf("no task %s", a.Task))
	}
	t := m.tasks[i]
	if t.Status == "done" {
		return replyTo(a, fmt.Errorf("task %s is done already", t.ID))
	}
	now := time.Now()
	t.Status = "done"
	t.Log(now, "done")
	m.tasks[i] = t
	m.publishPage()
	replyTo(a, nil)
	return tea.Batch(m.saveTasks(), m.taskSummary(t, now))
}
