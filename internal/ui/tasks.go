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

	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/roles"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/tasks"
)

type taskCreatedMsg struct {
	req                  ctl.Request
	id, cwd, name, model string
	dir, repo, branch    string
	err                  error
}

const (
	taskRole  = "task"
	taskModel = "opus"
)

var (
	taskModels   = []string{"fable", "opus", "sonnet"}
	taskPrios    = []string{"urgent", "high", "normal", "low"}
	taskStatuses = []string{"todo", "working", "needs-you", "waiting"}
	taskFields   = []string{"title", "prio", "deadline", "status", "need", "waits-on", "pr", "project"}
)

type taskLine struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Prio     string `json:"prio"`
	Deadline string `json:"deadline,omitempty"`
	From     string `json:"from,omitempty"`
	Context  string `json:"context,omitempty"`
	Project  string `json:"project,omitempty"`
	Session  string `json:"session,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Dir      string `json:"dir,omitempty"`
	Need     string `json:"need,omitempty"`
	WaitsOn  string `json:"waits_on,omitempty"`
	PR       string `json:"pr,omitempty"`
}

func (m *model) taskOf(sessionID string) (int, bool) {
	i := slices.IndexFunc(m.tasks, func(t tasks.Task) bool { return sessionID != "" && t.Session == sessionID })
	return i, i >= 0
}

func (m *model) projectOf(sessionID string) bool {
	return slices.Contains(m.state.Projects, sessionID)
}

func (m *model) taskRequest(req ctl.Request) tea.Cmd {
	fail := func(format string, a ...any) tea.Cmd {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf(format, a...)})
	}
	assistant := m.isAssistant(req.Caller)
	now := time.Now()
	switch req.Op {
	case "task-new":
		if !assistant {
			return fail("only the assistant can do this")
		}
		t := tasks.Task{ID: tasks.NewID(m.tasks), From: req.Args["from"], Context: req.Text, Prio: "normal", Status: "todo", Created: now}
		if _, err := m.setTaskFields(&t, req.Args); err != nil {
			return fail("%s", err)
		}
		t.Log(now, "created")
		m.tasks = append(m.tasks, t)
		return tea.Batch(m.saveTasks(), m.answer(req.ID, ctl.Reply{Out: t.ID}))
	case "task-list":
		if !assistant && !m.projectOf(req.Caller) {
			return fail("only the assistant or a project can do this")
		}
		return m.answer(req.ID, ctl.Reply{Out: m.taskListOut(assistant, req.Caller)})
	case "task-start":
		return m.startTask(req)
	case "task-update", "task-done":
		i, err := m.taskFor(req, assistant)
		if err != nil {
			return fail("%s", err)
		}
		t := m.tasks[i]
		out := "updated"
		if req.Op == "task-done" {
			if t.Status == "done" {
				return fail("task %s is done already", t.ID)
			}
			t.Status, t.Summary, out = "done", req.Args["summary"], "done"
			t.Log(now, "done")
		} else {
			if _, ok := req.Args["project"]; ok && !assistant {
				return fail("only the assistant can change the project")
			}
			changes, err := m.setTaskFields(&t, req.Args)
			if err != nil {
				return fail("%s", err)
			}
			t.Log(now, strings.Join(changes, ", "))
		}
		m.tasks[i] = t
		return tea.Batch(m.saveTasks(), m.answer(req.ID, ctl.Reply{Out: out}))
	}
	return fail("unknown request %s", req.Op)
}

func (m *model) taskFor(req ctl.Request, assistant bool) (int, error) {
	if assistant {
		if req.Target == "" {
			return 0, errors.New("give the task ID")
		}
		i := slices.IndexFunc(m.tasks, func(t tasks.Task) bool { return t.ID == req.Target })
		if i < 0 {
			return 0, fmt.Errorf("no task %s", req.Target)
		}
		return i, nil
	}
	i, ok := m.taskOf(req.Caller)
	if !ok {
		return 0, errors.New("only the assistant or a task session can do this")
	}
	if req.Target != "" && req.Target != m.tasks[i].ID {
		return 0, fmt.Errorf("you are task %s, not %s", m.tasks[i].ID, req.Target)
	}
	return i, nil
}

func (m *model) setTaskFields(t *tasks.Task, args map[string]string) ([]string, error) {
	var changes []string
	for _, key := range taskFields {
		v, ok := args[key]
		if !ok {
			continue
		}
		switch key {
		case "title":
			if strings.TrimSpace(v) == "" {
				return nil, errors.New("the title cannot be empty")
			}
			t.Title = v
		case "prio":
			if !slices.Contains(taskPrios, v) {
				return nil, fmt.Errorf("prio is one of %s", strings.Join(taskPrios, ", "))
			}
			t.Prio = v
		case "deadline":
			if _, err := time.Parse(time.DateOnly, v); v != "" && err != nil {
				return nil, errors.New("give the deadline as YYYY-MM-DD")
			}
			t.Deadline = v
		case "status":
			if v == "done" {
				return nil, errors.New("use deckhand task done")
			}
			if !slices.Contains(taskStatuses, v) {
				return nil, fmt.Errorf("status is one of %s", strings.Join(taskStatuses, ", "))
			}
			t.Status = v
		case "need":
			t.Need = v
		case "waits-on":
			t.WaitsOn = v
		case "pr":
			t.PR = v
		case "project":
			id := ""
			if v != "" {
				var err error
				if id, err = m.projectNamed(v); err != nil {
					return nil, err
				}
			}
			if t.Session != "" && id != t.Project {
				return nil, fmt.Errorf("task %s already runs in its project", t.ID)
			}
			t.Project = id
		}
		changes = append(changes, key+" "+v)
	}
	return changes, nil
}

func (m *model) projectNamed(name string) (string, error) {
	var found []string
	for _, id := range m.state.Projects {
		if m.sessionName(id) == name {
			found = append(found, id)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no project named %s; mark it with deckhand project mark ID", name)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%d projects are named %s", len(found), name)
}

func (m *model) sessionName(id string) string {
	if r, ok := m.rowByID(id); ok {
		return r.Name
	}
	return m.state.Names[id]
}

func (m *model) taskListOut(all bool, caller string) string {
	open := slices.DeleteFunc(slices.Clone(m.tasks), func(t tasks.Task) bool {
		return t.Status == "done" || (!all && t.Project != caller)
	})
	tasks.Sort(open)
	var lines []string
	for _, t := range open {
		line := taskLine{ID: t.ID, Title: t.Title, Status: t.Status, Prio: t.Prio, Deadline: t.Deadline, From: t.From, Context: t.Context,
			Session: t.Session, Branch: t.Branch, Dir: t.Dir, Need: t.Need, WaitsOn: t.WaitsOn, PR: t.PR}
		if t.Project != "" {
			line.Project = m.sessionName(t.Project)
		}
		if data, err := json.Marshal(line); err == nil {
			lines = append(lines, string(data))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *model) projectRequest(req ctl.Request) tea.Cmd {
	fail := func(format string, a ...any) tea.Cmd {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf(format, a...)})
	}
	if !m.isAssistant(req.Caller) {
		return fail("only the assistant can do this")
	}
	switch req.Op {
	case "project-mark":
		if _, ok := m.rowByID(req.Target); !ok {
			return fail("no session %s", req.Target)
		}
		if m.isAssistant(req.Target) {
			return fail("the assistant cannot be a project")
		}
		if !m.projectOf(req.Target) {
			m.state.Projects = append(m.state.Projects, req.Target)
		}
		return tea.Batch(m.saveState(), m.answer(req.ID, ctl.Reply{Out: "marked"}))
	case "project-unmark":
		if !m.projectOf(req.Target) {
			return fail("%s is not a project", req.Target)
		}
		m.state.Projects = slices.DeleteFunc(m.state.Projects, func(id string) bool { return id == req.Target })
		return tea.Batch(m.saveState(), m.answer(req.ID, ctl.Reply{Out: "unmarked"}))
	case "project-list":
		var lines []string
		for _, id := range m.state.Projects {
			line := listLine{ID: id, Name: m.state.Names[id], State: "gone"}
			if r, ok := m.rowByID(id); ok {
				line = m.listLineOf(r)
			}
			if data, err := json.Marshal(line); err == nil {
				lines = append(lines, string(data))
			}
		}
		return m.answer(req.ID, ctl.Reply{Out: strings.Join(lines, "\n")})
	}
	return fail("unknown request %s", req.Op)
}

func (m *model) startTask(req ctl.Request) tea.Cmd {
	fail := func(format string, a ...any) tea.Cmd {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf(format, a...)})
	}
	i := slices.IndexFunc(m.tasks, func(t tasks.Task) bool { return t.ID == req.Target })
	if i < 0 {
		return fail("no task %s", req.Target)
	}
	t := m.tasks[i]
	switch {
	case t.Project == "" || t.Project != req.Caller:
		return fail("only the project of task %s can start it", t.ID)
	case t.Status == "done":
		return fail("task %s is done", t.ID)
	case t.Session != "":
		return fail("task %s has a session already", t.ID)
	}
	model := req.Args["model"]
	if model != "" && !slices.Contains(taskModels, model) {
		return fail("model is one of %s", strings.Join(taskModels, ", "))
	}
	k := slices.IndexFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == req.Caller })
	if k < 0 {
		return fail("the project is not active in deckhand")
	}
	slug := taskSlug(t.Title)
	name, alt := cutSlug(slug, 40), cutSlug(slug, 39-len(t.ID))+"-"+t.ID
	if !ctl.ValidName(alt) {
		return fail("task ID %s does not fit a worktree name", t.ID)
	}
	if !ctl.ValidName(name) || m.nameRunning(name) || slices.ContainsFunc(m.state.Subs, func(s store.Subsession) bool { return s.Name == name }) {
		name = alt
	}
	id := uuid.NewV4().String()
	m.tasks[i].Session = id
	cwd, home := m.state.Active[k].Cwd, m.home
	return func() tea.Msg {
		if repo := ctl.TopLevel(cwd); repo != "" && ctl.Taken(repo, name) {
			name = alt
		}
		dir, repo, branch, err := ctl.CreateFrom(cwd, name, "")
		if err == nil && model == "" {
			model = taskModel
			if r, rerr := roles.Find(home, repo, taskRole); rerr == nil && r.Model != "" {
				model = r.Model
			}
		}
		return taskCreatedMsg{req: req, id: id, cwd: cwd, name: name, model: model, dir: dir, repo: repo, branch: branch, err: err}
	}
}

func (m *model) taskCreated(msg taskCreatedMsg) tea.Cmd {
	req := msg.req
	i, ok := m.taskOf(msg.id)
	if !ok {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf("task %s is gone", req.Target)})
	}
	if msg.err != nil {
		m.tasks[i].Session = ""
		return m.answer(req.ID, ctl.Reply{Err: msg.err.Error()})
	}
	project := req.Caller
	m.state.Subs = append(m.state.Subs, store.Subsession{ID: msg.id, Parent: project, Name: msg.name, Dir: msg.dir, Repo: msg.repo, Branch: msg.branch})
	if m.state.Models == nil {
		m.state.Models = map[string]string{}
	}
	if m.state.Roles == nil {
		m.state.Roles = map[string]string{}
	}
	m.state.Models[msg.id], m.state.Roles[msg.id] = msg.model, taskRole
	t := &m.tasks[i]
	t.Repo, t.Branch, t.Dir, t.Status, t.Need = msg.repo, msg.branch, msg.dir, "working", ""
	t.Log(time.Now(), fmt.Sprintf("started on %s with %s", msg.branch, msg.model))
	launch := deck.Launch{Cwd: msg.dir, SessionID: msg.id, Name: msg.name, Account: m.state.AccountFor(project), Prompt: taskPrompt(*t), ForkOf: project, ForkCwd: msg.cwd}
	out := fmt.Sprintf("started task %s as %s in %s on branch %s", t.ID, msg.name, msg.dir, msg.branch)
	return tea.Batch(m.saveState(), m.saveTasks(), forRequest(m.launch(launch), req.ID, out))
}

func taskPrompt(t tasks.Task) string {
	deadline := t.Deadline
	if deadline == "" {
		deadline = "none"
	}
	return fmt.Sprintf("deckhand started you as task %s: %s. Context: %s. Prio %s, deadline %s. You work in %s on branch %s.", t.ID, t.Title, t.Context, t.Prio, deadline, t.Dir, t.Branch)
}

func taskSlug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
			continue
		}
		dash = true
	}
	if b.Len() == 0 {
		return taskRole
	}
	return b.String()
}

func cutSlug(slug string, n int) string {
	if len(slug) > n {
		slug = slug[:n]
	}
	return strings.TrimRight(slug, "-")
}
