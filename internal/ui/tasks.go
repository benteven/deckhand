package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"uuid"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/gh"
	"github.com/benteven/deckhand/internal/roles"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/tasks"
	"github.com/benteven/deckhand/internal/typing"
)

type taskCreatedMsg struct {
	req                  ctl.Request
	id, cwd, name, model string
	dir, repo, branch    string
	err                  error
}

const (
	taskRole     = "task"
	taskModel    = "opus"
	projectModel = "opus"
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
		var told tea.Cmd
		before := t.Status
		if req.Op == "task-done" {
			if t.Status == "done" {
				return fail("task %s is done already", t.ID)
			}
			t.Status, t.Summary, out = "done", req.Args["summary"], "done"
			t.Log(now, "done")
			told = m.taskSummary(t, now)
		} else {
			if _, ok := req.Args["project"]; ok && !assistant {
				return fail("only the assistant can change the project")
			}
			changes, err := m.setTaskFields(&t, req.Args)
			if err != nil {
				return fail("%s", err)
			}
			t.Log(now, strings.Join(changes, ", "))
			if before != "needs-you" && t.Status == "needs-you" && m.ops.Cfg.Assistant {
				told = m.notifyNeed(t)
			}
		}
		m.tasks[i] = t
		return tea.Batch(m.saveTasks(), m.answer(req.ID, ctl.Reply{Out: out}), told)
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
		r, ok := m.rowByID(req.Target)
		if !ok {
			return fail("no session %s", req.Target)
		}
		if m.isAssistant(req.Target) {
			return fail("the assistant cannot be a project")
		}
		home, cwd := m.home, r.Cwd
		return func() tea.Msg {
			role, err := roles.Find(home, ctl.TopLevel(cwd), projectRole)
			return roleFoundMsg{req: req, role: role, err: err}
		}
	case "project-unmark":
		if !m.projectOf(req.Target) {
			return fail("%s is not a project", req.Target)
		}
		m.state.Projects = slices.DeleteFunc(m.state.Projects, func(id string) bool { return id == req.Target })
		if m.state.Roles[req.Target] == projectRole {
			delete(m.state.Roles, req.Target)
			delete(m.state.Models, req.Target)
		}
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

func (m *model) projectMarked(msg roleFoundMsg) tea.Cmd {
	id := msg.req.Target
	if _, ok := m.rowByID(id); !ok {
		return m.answer(msg.req.ID, ctl.Reply{Err: fmt.Sprintf("no session %s", id)})
	}
	model := projectModel
	if msg.err == nil && msg.role.Model != "" {
		model = msg.role.Model
	}
	if m.state.Roles == nil {
		m.state.Roles = map[string]string{}
	}
	if m.state.Models == nil {
		m.state.Models = map[string]string{}
	}
	m.state.Roles[id], m.state.Models[id] = projectRole, model
	var told tea.Cmd
	if !m.projectOf(id) {
		m.state.Projects = append(m.state.Projects, id)
		text := strings.TrimSpace("[deckhand] you are a project now.\n\n" + msg.role.Body)
		m.tellProject(id, text+"\n\n"+projectNotesText(m.notesPath(id)))
		told = m.deliverProjectNotes()
	}
	return tea.Batch(m.saveState(), m.answer(msg.req.ID, ctl.Reply{Out: "marked"}), told)
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
	case t.Session != "" || m.taskStarts[t.ID]:
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
	if m.taskStarts == nil {
		m.taskStarts = map[string]bool{}
	}
	m.taskStarts[t.ID] = true
	cwd, home := m.state.Active[k].Cwd, m.home
	return func() tea.Msg {
		if repo := ctl.TopLevel(cwd); repo != "" && ctl.Taken(repo, name) {
			name = alt
		}
		dir, repo, branch, err := ctl.CreateFrom(cwd, name)
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
	delete(m.taskStarts, req.Target)
	if msg.err != nil {
		return m.answer(req.ID, ctl.Reply{Err: msg.err.Error()})
	}
	i := slices.IndexFunc(m.tasks, func(t tasks.Task) bool { return t.ID == req.Target })
	switch {
	case i < 0:
		return tea.Batch(dropWorktree(msg.repo, msg.name, msg.branch), m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf("task %s is gone", req.Target)}))
	case m.tasks[i].Status == "done":
		return tea.Batch(dropWorktree(msg.repo, msg.name, msg.branch), m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf("task %s is done", req.Target)}))
	}
	m.tasks[i].Session = msg.id
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
	launch := deck.Launch{Cwd: msg.dir, SessionID: msg.id, Name: msg.name, Account: m.state.AccountFor(project), Prompt: taskPrompt(*t), ForkOf: project, ForkCwd: msg.cwd, ForkAccount: m.state.AccountFor(project)}
	out := fmt.Sprintf("started task %s as %s in %s on branch %s", t.ID, msg.name, msg.dir, msg.branch)
	start := forRequest(m.launch(launch), req.ID, out)
	return tea.Batch(m.saveState(), m.saveTasks(), func() tea.Msg {
		msg, _ := start().(startedMsg)
		msg.taskStart = true
		return msg
	})
}

func dropWorktree(repo, name, branch string) tea.Cmd {
	return func() tea.Msg {
		var err error
		if _, serr := os.Stat(ctl.Worktree(repo, name)); serr == nil {
			err = ctl.Remove(repo, name, deck.Trash)
		} else {
			err = ctl.Prune(repo)
		}
		if err == nil {
			err = ctl.DeleteBranch(repo, branch)
		}
		if err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *model) taskStartFailed(i int, id string) tea.Cmd {
	t := &m.tasks[i]
	t.Session, t.Repo, t.Branch, t.Dir = "", "", "", ""
	if t.Status != "done" {
		t.Status = "todo"
	}
	var drop tea.Cmd
	if sub, ok := m.state.SubOf(id); ok {
		drop = dropWorktree(sub.Repo, sub.Name, sub.Branch)
	}
	m.state.Subs = slices.DeleteFunc(m.state.Subs, func(s store.Subsession) bool { return s.ID == id })
	delete(m.state.Models, id)
	delete(m.state.Roles, id)
	delete(m.state.Accounts, id)
	delete(m.state.Fallbacks, id)
	return tea.Batch(m.saveState(), m.saveTasks(), drop)
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

type roleFoundMsg struct {
	req  ctl.Request
	role roles.Role
	err  error
}

const projectRole = "project"

func (m *model) helperRole(id string) bool {
	role := m.state.Roles[id]
	return role != "" && role != taskRole && role != projectRole
}

func (m *model) ownsWorktree(id string) bool {
	_, ok := m.state.SubOf(id)
	return ok && !m.helperRole(id)
}

func (m *model) roleRequest(req ctl.Request) tea.Cmd {
	fail := func(format string, a ...any) tea.Cmd {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf(format, a...)})
	}
	switch req.Op {
	case "role-list":
		if !m.projectOf(req.Caller) {
			return fail("only a project can do this")
		}
		r, ok := m.rowByID(req.Caller)
		if !ok {
			return fail("the project is not in deckhand")
		}
		home, cwd := m.home, r.Cwd
		return func() tea.Msg {
			var lines []string
			for _, role := range roles.List(home, ctl.TopLevel(cwd)) {
				if role.Name != taskRole && role.Name != projectRole {
					lines = append(lines, role.Name+"\t"+role.Model+"\t"+role.Description)
				}
			}
			if err := ctl.Answer(home, req.ID, ctl.Reply{Out: strings.Join(lines, "\n")}); err != nil {
				return errMsg{err}
			}
			return nil
		}
	case "role-start":
		t, err := m.roleTask(req)
		if err != nil {
			return fail("%s", err)
		}
		home := m.home
		return func() tea.Msg {
			r, err := roles.Find(home, t.Repo, req.Name)
			return roleFoundMsg{req: req, role: r, err: err}
		}
	case "role-model":
		if m.state.Roles[req.Caller] == "" {
			return fail("only a role session can do this")
		}
		model := req.Args["model"]
		if !slices.Contains(taskModels, model) {
			return fail("model is one of %s", strings.Join(taskModels, ", "))
		}
		r, ok := m.rowByID(req.Caller)
		switch {
		case !ok || r.Kind != deck.Live || m.resuming[r.SessionID]:
			return fail("this session is not running in deckhand")
		case m.userIn(r):
			return fail("the user is in %s now; try again later", r.Name)
		}
		if m.state.Models == nil {
			m.state.Models = map[string]string{}
		}
		m.state.Models[req.Caller] = model
		if m.continueAfter == nil {
			m.continueAfter = map[string]bool{}
		}
		m.continueAfter[req.Caller] = true
		restart := m.restartRow(r)
		if restart == nil {
			delete(m.continueAfter, req.Caller)
			return tea.Batch(m.saveState(), fail("could not restart; the model changes at the next start"))
		}
		return tea.Sequence(m.saveState(), m.answer(req.ID, ctl.Reply{Out: "restarting on " + model}), restart)
	}
	return fail("unknown request %s", req.Op)
}

func (m *model) roleTask(req ctl.Request) (tasks.Task, error) {
	i := slices.IndexFunc(m.tasks, func(t tasks.Task) bool { return t.ID == req.Target })
	if i < 0 {
		return tasks.Task{}, fmt.Errorf("no task %s", req.Target)
	}
	t := m.tasks[i]
	switch {
	case t.Project == "" || t.Project != req.Caller:
		return t, fmt.Errorf("only the project of task %s can start its roles", t.ID)
	case t.Status == "done":
		return t, fmt.Errorf("task %s is done", t.ID)
	case t.Session == "" || t.Dir == "":
		return t, fmt.Errorf("task %s has no session yet", t.ID)
	case req.Name == taskRole || req.Name == projectRole:
		return t, fmt.Errorf("%s is not a role to start", req.Name)
	case m.nameRunning(m.roleName(t, req.Name)):
		return t, fmt.Errorf("a session named %s is running", m.roleName(t, req.Name))
	}
	return t, nil
}

func (m *model) roleName(t tasks.Task, role string) string {
	slug := cutSlug(taskSlug(t.Title), 40)
	if sub, ok := m.state.SubOf(t.Session); ok {
		slug = sub.Name
	}
	return slug + "-" + role
}

func (m *model) roleFound(msg roleFoundMsg) tea.Cmd {
	req := msg.req
	if msg.err != nil {
		return m.answer(req.ID, ctl.Reply{Err: fmt.Sprintf("no role %s: %s", req.Name, msg.err)})
	}
	t, err := m.roleTask(req)
	if err != nil {
		return m.answer(req.ID, ctl.Reply{Err: err.Error()})
	}
	name := m.roleName(t, req.Name)
	m.state.Subs = slices.DeleteFunc(m.state.Subs, func(s store.Subsession) bool { return s.Parent == t.Project && s.Name == name })
	id := uuid.NewV4().String()
	m.state.Subs = append(m.state.Subs, store.Subsession{ID: id, Parent: t.Project, Name: name, Dir: t.Dir, Repo: t.Repo, Branch: t.Branch})
	if m.state.Roles == nil {
		m.state.Roles = map[string]string{}
	}
	m.state.Roles[id] = req.Name
	if msg.role.Model != "" {
		if m.state.Models == nil {
			m.state.Models = map[string]string{}
		}
		m.state.Models[id] = msg.role.Model
	}
	prompt := fmt.Sprintf("deckhand started you as the %s of task %s: %s. You work in %s on branch %s.", req.Name, t.ID, t.Title, t.Dir, t.Branch)
	launch := deck.Launch{Cwd: t.Dir, SessionID: id, Name: name, Account: m.state.AccountFor(t.Session), Prompt: prompt, ForkOf: t.Session, ForkCwd: t.Dir, ForkAccount: m.state.AccountFor(t.Session)}
	out := fmt.Sprintf("started %s for task %s as %s in %s", req.Name, t.ID, name, t.Dir)
	return tea.Batch(m.saveState(), forRequest(m.launch(launch), req.ID, out))
}

const (
	mergedLog    = "PR merged; asked for a summary"
	askedLog     = "asked about the worktree"
	projectIdle  = 10 * time.Minute
	closeBackoff = 5 * time.Minute
)

type taskClosedMsg struct {
	id      string
	failed  []store.ActiveEntry
	removed bool
	ask     bool
	err     error
}

func (m *model) taskTick(now time.Time) tea.Cmd {
	return tea.Batch(m.askSummaries(), m.deliverProjectNotes(), m.closeDoneTasks(now), m.suspendIdleProjects(now))
}

func (m *model) notesPath(id string) string {
	name := m.sessionName(id)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		name = id
	}
	return filepath.Join(m.home, "projects", name, "notes.md")
}

func projectNotesText(path string) string {
	return fmt.Sprintf("Your project notes file is %s. Read it when you start. Deckhand adds the summary of each finished task to it.", path)
}

func (m *model) askSummaries() tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tasks {
		if t.Status == "done" || t.Session == "" || t.PR == "" || m.asking[t.ID] {
			continue
		}
		if e := m.prs[t.PR]; e == nil || e.state != "merged" || !t.SummaryAsked.IsZero() {
			continue
		}
		r, ok := m.rowByID(t.Session)
		if !ok || !m.typeReady(r) {
			continue
		}
		if m.asking == nil {
			m.asking = map[string]bool{}
		}
		id := t.ID
		m.asking[id] = true
		text := fmt.Sprintf("[deckhand] PR %s is merged. Write a summary of five lines or fewer with deckhand task done --summary TEXT.", t.PR)
		cmds = append(cmds, m.typeIn(r, text, func(err error) tea.Cmd {
			delete(m.asking, id)
			if errors.Is(err, typing.ErrMenu) {
				return nil
			}
			if err != nil {
				m.err = typeError(r.Name, err)
			}
			k := slices.IndexFunc(m.tasks, func(t tasks.Task) bool { return t.ID == id })
			if k < 0 || err != nil && !errors.Is(err, typing.ErrPasted) {
				return nil
			}
			now := time.Now()
			m.tasks[k].SummaryAsked = now
			m.tasks[k].Log(now, mergedLog)
			return m.saveTasks()
		}))
	}
	return tea.Batch(cmds...)
}

func (m *model) taskSummary(t tasks.Task, now time.Time) tea.Cmd {
	summary := relayText(t.Summary)
	if summary == "" || t.Project == "" {
		return nil
	}
	m.tellProject(t.Project, fmt.Sprintf("[deckhand] task %s \"%s\" is done: %s", t.ID, t.Title, summary))
	path, entry := m.notesPath(t.Project), fmt.Sprintf("## %s %s\n%s\n", now.Format(time.DateOnly), t.Title, summary)
	return tea.Batch(m.deliverProjectNotes(), func() tea.Msg {
		if err := appendNotes(path, entry); err != nil {
			return errMsg{err}
		}
		return nil
	})
}

func relayText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	for {
		cut := text
		for _, mark := range []string{"[deckhand]", viaMark, strings.TrimSpace(pageMark)} {
			cut = strings.TrimSpace(strings.TrimPrefix(cut, mark))
		}
		if cut == text {
			return text
		}
		text = cut
	}
}

func appendNotes(path, entry string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(entry)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (m *model) tellProject(id, text string) {
	if m.projectNotes == nil {
		m.projectNotes = map[string][]string{}
	}
	m.projectNotes[id] = append(m.projectNotes[id], text)
}

func (m *model) deliverProjectNotes() tea.Cmd {
	var cmds []tea.Cmd
	for id, notes := range m.projectNotes {
		r, ok := m.rowByID(id)
		if !ok || !m.typeReady(r) {
			continue
		}
		delete(m.projectNotes, id)
		cmds = append(cmds, m.typeOrHold(r, strings.Join(notes, "\n"), func() {
			m.projectNotes[id] = append(notes, m.projectNotes[id]...)
		}))
	}
	return tea.Batch(cmds...)
}

func (m *model) closeDoneTasks(now time.Time) tea.Cmd {
	var cmds []tea.Cmd
	for _, t := range m.tasks {
		if t.Status != "done" || t.Session == "" || m.closing[t.Session] || !m.ownsWorktree(t.Session) || !t.CleanupAsked.IsZero() || now.Before(m.closeRetry[t.Session]) {
			continue
		}
		if m.cleanup != nil && m.cleanup.sub.ID == t.Session {
			continue
		}
		cmds = append(cmds, m.closeTask(t))
	}
	return tea.Batch(cmds...)
}

func (m *model) closeTask(t tasks.Task) tea.Cmd {
	sub, _ := m.state.SubOf(t.Session)
	var rows []deck.Row
	for _, s := range m.state.Subs {
		if s.Dir != sub.Dir {
			continue
		}
		r, ok := m.rowByID(s.ID)
		switch {
		case ok && r.Kind == deck.Live && r.Job == "" && !m.resuming[s.ID] && !m.userIn(r):
			rows = append(rows, r)
		case m.running(s.ID) || m.in.Panes[s.ID] != "":
			return nil
		}
	}
	if m.closing == nil {
		m.closing = map[string]bool{}
	}
	m.closing[sub.ID] = true
	var cmds []tea.Cmd
	entries := make([]store.ActiveEntry, len(rows))
	for k, r := range rows {
		var drop tea.Cmd
		entries[k], drop = m.detach(r)
		cmds = append(cmds, drop)
	}
	ops, pr, branch, noGH := m.ops, t.PR, t.Branch, m.noGH
	merged := m.merged(t)
	run := func() tea.Msg {
		msg := taskClosedMsg{id: sub.ID}
		for k, r := range rows {
			if err := ops.Stop(r.PaneID, r.Busy); err != nil {
				msg.failed, msg.err = append(msg.failed, entries[k]), err
			}
		}
		if msg.err != nil {
			return msg
		}
		if !merged && pr != "" && !noGH {
			state, _, err := gh.State(pr)
			merged = err == nil && state == "merged"
		}
		if merged = merged && !noGH; merged {
			info, err := gh.Info(pr)
			head, herr := ctl.Head(sub.Repo, branch)
			merged = err == nil && herr == nil && info.Head == branch && info.HeadOid == head
		}
		if !merged {
			msg.ask = true
			return msg
		}
		if _, err := os.Stat(ctl.Worktree(sub.Repo, sub.Name)); err == nil {
			if msg.err = ctl.Remove(sub.Repo, sub.Name, deck.Trash); msg.err != nil {
				return msg
			}
		} else if msg.err = ctl.Prune(sub.Repo); msg.err != nil {
			return msg
		}
		msg.removed = true
		msg.err = ctl.DeleteBranch(sub.Repo, sub.Branch)
		return msg
	}
	return tea.Batch(append(cmds, m.saveState(), tea.Sequence(m.showSelected(), run))...)
}

func (m *model) taskClosed(msg taskClosedMsg) tea.Cmd {
	for _, entry := range msg.failed {
		m.restoreActive(entry)
	}
	if msg.err != nil {
		m.err = msg.err
		delete(m.closing, msg.id)
		if m.closeRetry == nil {
			m.closeRetry = map[string]time.Time{}
		}
		m.closeRetry[msg.id] = time.Now().Add(closeBackoff)
	} else {
		delete(m.closeRetry, msg.id)
	}
	if msg.ask {
		if i, ok := m.taskOf(msg.id); ok {
			now := time.Now()
			m.tasks[i].CleanupAsked = now
			m.tasks[i].Log(now, askedLog)
		}
		m.rebuild()
		return tea.Batch(m.saveTasks(), m.askCleanup(msg.id))
	}
	if !msg.removed {
		m.rebuild()
		return m.saveState()
	}
	delete(m.closing, msg.id)
	m.state.Cleanups = slices.DeleteFunc(m.state.Cleanups, func(id string) bool { return id == msg.id })
	return m.worktreeRemoved(worktreeRemovedMsg{id: msg.id})
}

func (m *model) merged(t tasks.Task) bool {
	return m.prs[t.PR] != nil && m.prs[t.PR].state == "merged"
}

func (m *model) suspendIdleProjects(now time.Time) tea.Cmd {
	if !m.ops.Cfg.Assistant {
		return nil
	}
	var cmds []tea.Cmd
	for id := range m.idleSince {
		if !m.projectOf(id) {
			delete(m.idleSince, id)
		}
	}
	for _, id := range m.state.Projects {
		r, ok := m.rowByID(id)
		if !ok || r.Kind != deck.Live || !m.projectIdle(r) {
			delete(m.idleSince, id)
			continue
		}
		if m.idleSince == nil {
			m.idleSince = map[string]time.Time{}
		}
		since, seen := m.idleSince[id]
		if !seen {
			m.idleSince[id] = now
			continue
		}
		if now.Sub(since) >= projectIdle {
			delete(m.idleSince, id)
			cmds = append(cmds, m.suspend(r, ""))
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) projectIdle(r deck.Row) bool {
	id := r.SessionID
	if m.resuming[id] || r.Busy || r.Asking || !m.userAway(r) || m.typing[r.PaneID] || m.handoffs[r.PaneID] != nil || len(m.projectNotes[id]) > 0 {
		return false
	}
	if slices.ContainsFunc(m.tasks, func(t tasks.Task) bool { return t.Project == id && slices.Contains(taskStatuses, t.Status) }) {
		return false
	}
	return !slices.ContainsFunc(m.state.Subs, func(s store.Subsession) bool { return s.Parent == id && m.running(s.ID) })
}
