package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"uuid"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/benteven/deckhand/internal/accounts"
	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/events"
	"github.com/benteven/deckhand/internal/live"
	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/textclean"
	"github.com/benteven/deckhand/internal/transcript"
)

type picker struct {
	input   textinput.Model
	folders []string
	shown   []string
	cursor  int
	path    string
	state   int
	checked string
	ghost   string
	ghostOf string

	account  string
	accounts []string
}

func pathLike(v string) bool {
	return v != "" && strings.ContainsRune("/~.", rune(v[0]))
}

func (p *picker) resolved() int {
	if !pathLike(p.input.Value()) {
		return pathNone
	}
	return p.state
}

func existingFolders(summaries map[string]transcript.Summary) []string {
	sums := make([]transcript.Summary, 0, len(summaries))
	for _, s := range summaries {
		if s.Cwd != "" {
			sums = append(sums, s)
		}
	}
	sort.Slice(sums, func(i, j int) bool { return sums[i].LastUsed.After(sums[j].LastUsed) })
	seen := map[string]bool{}
	folders := []string{}
	for _, s := range sums {
		if seen[s.Cwd] {
			continue
		}
		seen[s.Cwd] = true
		if info, err := os.Stat(s.Cwd); err == nil && info.IsDir() {
			folders = append(folders, s.Cwd)
		}
	}
	return folders
}

func newPicker(folders []string, width int) *picker {
	p := &picker{input: textinput.New(), folders: folders}
	p.input.Prompt = "> "
	p.input.Placeholder = "filter folders"
	p.input.ShowSuggestions = true
	p.input.SetWidth(max(width-4, 1))
	p.filter()
	return p
}

func (p *picker) filter() {
	terms := strings.Fields(strings.ToLower(p.input.Value()))
	p.shown = p.shown[:0]
	if p.resolved() == pathOK {
		p.shown = append(p.shown, p.path)
	}
	for _, f := range p.folders {
		if p.resolved() == pathOK && f == p.path {
			continue
		}
		hay := strings.ToLower(f)
		match := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				match = false
				break
			}
		}
		if match {
			p.shown = append(p.shown, f)
		}
	}
	p.cursor = min(p.cursor, max(len(p.shown)-1, 0))
	if p.ghost != "" && p.ghostOf == p.input.Value() {
		p.input.SetSuggestions([]string{p.ghost})
	} else {
		p.input.SetSuggestions(p.recentGhost())
	}
}

func (p *picker) recentGhost() []string {
	v := p.input.Value()
	if v == "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	for _, f := range p.folders {
		cands := []string{f}
		if home != "" && (f == home || strings.HasPrefix(f, home+"/")) {
			cands = append(cands, "~"+f[len(home):])
		}
		for _, c := range cands {
			if len(c) <= len(v) || !strings.EqualFold(c[:len(v)], v) {
				continue
			}
			rest := c[len(v):]
			if i := strings.Index(rest, "/"); i >= 0 {
				rest = rest[:i+1]
			}
			return []string{v + rest}
		}
	}
	return nil
}

func (m *model) pickerKey(msg tea.KeyPressMsg) tea.Cmd {
	p := m.picker
	switch msg.String() {
	case "esc":
		m.picker = nil
		return m.zoom(false)
	case "enter":
		if len(p.shown) == 0 || p.resolved() == pathMissing || (pathLike(p.input.Value()) && p.checked != p.input.Value()) {
			return nil
		}
		m.picker = nil
		return m.start(uuid.NewV4().String(), p.shown[p.cursor], "", p.account, "")
	case "ctrl+a":
		if len(p.accounts) > 1 {
			p.account = nextAccount(p.accounts, p.account)
		}
		return nil
	case "up":
		p.cursor = max(p.cursor-1, 0)
		return nil
	case "down":
		p.cursor = min(p.cursor+1, max(len(p.shown)-1, 0))
		return nil
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.filter()
	return tea.Batch(cmd, inspect(p.input.Value()), ghost(p.input.Value()))
}

func (p *picker) view(w, h int) string {
	body := []string{normal.Render("New session"), p.input.View(), ""}
	if len(p.accounts) > 1 {
		body[0] += gray.Render(" on ") + orange.Render(accounts.Label(p.account)) + gray.Render(" (ctrl-a changes)")
	}
	room := max(h-1-len(body), 1)
	start := max(p.cursor-room+1, 0)
	home, _ := os.UserHomeDir()
	for i := start; i < len(p.shown) && i < start+room; i++ {
		f := textclean.Clean(p.shown[i])
		if home != "" && (f == home || strings.HasPrefix(f, home+"/")) {
			f = "~" + f[len(home):]
		}
		bar := selectionBar(i == p.cursor)
		if i == 0 && p.resolved() == pathOK {
			bar += "Start in "
			if avail := w - ansi.StringWidth(bar); ansi.StringWidth(f) > avail {
				f = ansi.TruncateLeft(f, ansi.StringWidth(f)-max(avail, 1)+1, "…")
			}
		}
		body = append(body, ansi.Truncate(bar+f, w, "…"))
	}
	switch {
	case p.resolved() == pathMissing:
		body = append(body, gray.Render("no such folder"))
	case len(p.shown) == 0:
		body = append(body, gray.Render("No folders match."))
	}
	hint := gray.Render("↑↓ move  tab accept") + "\n" + gray.Render("enter start  esc cancel")
	return page(body, hint, h)
}

var reverse = lipgloss.NewStyle().Reverse(true)

type renameDialog struct {
	input    textinput.Model
	id, pane string
	name     string
	suspend  bool
	busy     bool
	fresh    bool
	order    []string
	kind     deck.Kind
}

func newRenameDialog(r deck.Row, suspend bool, order []string) *renameDialog {
	d := &renameDialog{input: textinput.New(), id: r.SessionID, pane: r.PaneID, name: r.Name, suspend: suspend, busy: r.Busy, fresh: true, order: order, kind: r.Kind}
	d.input.Prompt = ""
	d.input.SetValue(r.Name)
	d.input.CursorEnd()
	return d
}

func (m *model) renameKey(msg tea.KeyPressMsg) tea.Cmd {
	d := m.rename
	switch msg.String() {
	case "esc":
		m.rename = nil
		return nil
	case "enter":
		m.rename = nil
		return m.confirmRename(d)
	}
	if d.fresh && msg.Text != "" {
		d.input.SetValue("")
	}
	d.fresh = false
	var cmd tea.Cmd
	d.input, cmd = d.input.Update(msg)
	return cmd
}

func (d *renameDialog) line(w int) string {
	bar := selectionBar(true)
	d.input.SetWidth(max(w-3, 1))
	if d.fresh {
		return ansi.Truncate(bar+reverse.Render(ansi.Truncate(d.name, max(w-2, 1), "…")), w, "")
	}
	return ansi.Truncate(bar+d.input.View(), w, "")
}

func (d *renameDialog) hint(w int) string {
	text := "Rename: enter to save, esc to cancel"
	if d.suspend {
		text = "Suspend: enter to confirm, esc to cancel"
	}
	if ansi.StringWidth(text) > w {
		text = strings.Replace(text, ", ", ",\n", 1)
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = gray.Render(ansi.Truncate(l, w, "…"))
	}
	if d.suspend && d.busy {
		lines = append(lines, yellow.Render(ansi.Truncate("Busy: suspending stops the turn", w, "…")))
	}
	return strings.Join(lines, "\n")
}

type resumePrompt struct{ count int }

func (p *resumePrompt) view(w, h int) string {
	body := []string{normal.Render(ansi.Truncate(fmt.Sprintf("Resume %s from before? (y/n)", sessionCount(p.count)), w, "…"))}
	return page(body, gray.Render(ansi.Truncate("y resume all  n keep them gray", w, "")), h)
}

func sessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

type takeoverDialog struct{ row deck.Row }

func (d *takeoverDialog) view(w, h int) string {
	body := []string{
		normal.Render(ansi.Truncate(`Take over "`+d.row.Name+`"`, w, "…")),
		gray.Render(ansi.Truncate("Stop it in the other terminal and open it here? (y/n)", w, "…")),
	}
	if d.row.Busy {
		body = append(body, "", yellow.Render(ansi.Truncate("This session is busy. Taking over stops the turn in progress.", w, "…")))
	}
	return page(body, gray.Render(ansi.Truncate("y take over  n cancel", w, "")), h)
}

func (m *model) takeoverKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		r := m.takeover.row
		m.takeover = nil
		if m.resuming[r.SessionID] {
			return m.zoom(false)
		}
		m.resuming[r.SessionID] = true
		m.markEnding(r.SessionID)
		m.rebuild()
		ops := m.ops
		stop := func() tea.Msg {
			return takenOverMsg{row: r, err: ops.StopOutside(r.PID, r.Started, 5*time.Second)}
		}
		return tea.Sequence(m.zoom(false), m.showSelected(), stop)
	case "n", "esc":
		m.takeover = nil
		return m.zoom(false)
	}
	return nil
}

func (m *model) tookOver(msg takenOverMsg) tea.Cmd {
	delete(m.ending, msg.row.SessionID)
	if msg.err != nil {
		delete(m.resuming, msg.row.SessionID)
		m.err = msg.err
		m.rebuild()
		return nil
	}
	return m.resume(msg.row)
}

type quitDialog struct{ count int }

func (d *quitDialog) view(w, h int) string {
	body := []string{normal.Render(ansi.Truncate(fmt.Sprintf("Stop %s and quit? (y/n)", sessionCount(d.count)), w, "…"))}
	return page(body, gray.Render(ansi.Truncate("y stop and quit  n cancel", w, "")), h)
}

func (m *model) quitKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		m.quit = nil
		m.quitting = true
		return m.stopAll()
	case "n", "esc":
		m.quit = nil
		return m.zoom(false)
	}
	return nil
}

type deleteDialog struct{ row deck.Row }

type trashedMsg struct {
	id  string
	err error
}

func (d *deleteDialog) view(w, h int) string {
	body := []string{
		normal.Render(ansi.Truncate(`Delete "`+d.row.Name+`"? (y/n)`, w, "…")),
		gray.Render(ansi.Truncate("Moves it to the Trash.", w, "…")),
	}
	return page(body, gray.Render(ansi.Truncate("y delete  n cancel", w, "")), h)
}

func (m *model) deleteKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		id := m.del.row.SessionID
		m.del = nil
		i := slices.IndexFunc(m.suspended, func(r deck.Row) bool { return r.SessionID == id })
		live := slices.ContainsFunc(m.in.Live, func(l live.Session) bool { return l.SessionID == id })
		active := slices.ContainsFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == id })
		if i < 0 || live || active || m.resuming[id] || m.ending[id] {
			m.hint = "not suspended any more, not deleted"
			return m.zoom(false)
		}
		return tea.Sequence(m.zoom(false), m.trash(m.suspended[i]))
	case "n", "esc":
		m.del = nil
		return m.zoom(false)
	}
	return nil
}

func (m *model) trash(r deck.Row) tea.Cmd {
	eventsDir, home := filepath.Join(m.home, "events"), m.home
	return func() tea.Msg {
		if err := deck.TrashSession(r.SessionID, r.TranscriptPath); err != nil {
			return errMsg{err}
		}
		os.RemoveAll(review.Dir(home, r.SessionID))
		return trashedMsg{id: r.SessionID, err: events.Remove(eventsDir, r.SessionID)}
	}
}

func (m *model) trashed(msg trashedMsg) tea.Cmd {
	if m.gone == nil {
		m.gone = map[string]bool{}
	}
	m.gone[msg.id] = true
	m.err = msg.err
	delete(m.state.Names, msg.id)
	delete(m.state.Accounts, msg.id)
	delete(m.in.Summaries, msg.id)
	m.in.Files = m.withoutGone(m.in.Files)
	m.rebuild()
	return tea.Batch(m.saveState(), m.showSelected())
}

func (m *model) withoutGone(files []transcript.File) []transcript.File {
	if len(m.gone) == 0 {
		return files
	}
	out := make([]transcript.File, 0, len(files))
	for _, f := range files {
		if !m.gone[f.SessionID] {
			out = append(out, f)
		}
	}
	return out
}

type restartDialog struct{ row deck.Row }

type restartedMsg struct {
	row       deck.Row
	id, pane  string
	err, warn error

	stopFailed bool
}

func (d *restartDialog) view(w, h int) string {
	body := []string{normal.Render(ansi.Truncate(`Restart "`+d.row.Name+`"? (y/n)`, w, "…"))}
	if d.row.Busy {
		body = append(body, "", yellow.Render(ansi.Truncate("This session is busy. Restarting stops the turn in progress.", w, "…")))
	}
	return page(body, gray.Render(ansi.Truncate("y restart  n cancel", w, "")), h)
}

func (m *model) restartKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		r := m.restart.row
		m.restart = nil
		return tea.Sequence(m.zoom(false), m.restartRow(r))
	case "n", "esc":
		m.restart = nil
		return m.zoom(false)
	}
	return nil
}

func (m *model) restartRow(r deck.Row) tea.Cmd {
	return m.relaunch(r, "", m.firstPrompt(r.SessionID))
}

func (m *model) relaunch(r deck.Row, fresh, prompt string) tea.Cmd {
	account := m.state.AccountFor(r.SessionID)
	if m.resuming[r.SessionID] {
		return nil
	}
	if !accounts.LoggedIn(m.home, account) {
		m.err = fmt.Errorf("account %s is not logged in, press A to log in", account)
		return nil
	}
	if fresh == "" && m.continueAfter[r.SessionID] {
		delete(m.continueAfter, r.SessionID)
		if prompt == "" {
			prompt = "continue"
		} else {
			prompt += " Then continue."
		}
	}
	m.resuming[r.SessionID] = true
	m.markEnding(r.SessionID)
	delete(m.in.Panes, r.SessionID)
	m.rebuild()
	ops, from := m.ops, m.paneAccount[r.PaneID]
	name := m.state.Names[r.SessionID]
	id := r.SessionID
	if r.JobSession != "" {
		id = r.JobSession
	}
	if fresh != "" {
		if m.isAssistant(r.SessionID) {
			ops.AssistantID = fresh
		}
		id = fresh
	}
	start := m.starter(ops, r.SessionID)
	run := func() tea.Msg {
		if r.Job != "" {
			if err := ops.StopJob(r.Job, from); err != nil {
				return restartedMsg{row: r, err: err, stopFailed: true}
			}
		}
		if err := ops.Stop(r.PaneID, r.Busy); err != nil {
			return restartedMsg{row: r, err: err, stopFailed: true}
		}
		pane, warn, err := start(deck.Launch{Cwd: r.Cwd, SessionID: id, Name: name, Account: account, Prompt: prompt, Resume: fresh == ""})
		return restartedMsg{row: r, id: id, pane: pane, err: err, warn: warn}
	}
	return tea.Sequence(m.showSelected(), run)
}

func (m *model) restarted(msg restartedMsg) tea.Cmd {
	id := msg.row.SessionID
	delete(m.resuming, id)
	delete(m.ending, id)
	if msg.err != nil {
		m.err = msg.err
		if m.in.Panes == nil {
			m.in.Panes = map[string]string{}
		}
		if msg.stopFailed {
			m.in.Panes[id] = msg.row.PaneID
		}
		m.rebuild()
		return tea.Batch(m.showSelected(), m.refresh())
	}
	if m.in.Panes == nil {
		m.in.Panes = map[string]string{}
	}
	late := id == m.state.Assistant.ID && m.stopAssistant
	if msg.warn != nil {
		m.err = msg.warn
	}
	var moved tea.Cmd
	if msg.id != id {
		m.moveSession(id, msg.id)
		id, moved = msg.id, m.saveTasks()
	}
	m.in.Panes[id] = msg.pane
	m.rebuild()
	if late {
		return tea.Batch(m.saveState(), moved, m.stopLateAssistant(id))
	}
	return tea.Batch(m.saveState(), moved, m.showSelected())
}

func (m *model) moveSession(from, to string) {
	if m.isAssistant(from) {
		m.setAssistantID(to)
	}
	for i := range m.state.Active {
		if m.state.Active[i].SessionID == from {
			m.state.Active[i].SessionID = to
		}
	}
	if n, ok := m.state.Names[from]; ok {
		m.state.Names[to] = n
		delete(m.state.Names, from)
	}
	if a, ok := m.state.Accounts[from]; ok {
		m.state.Accounts[to] = a
		delete(m.state.Accounts, from)
	}
	if a, ok := m.state.Fallbacks[from]; ok {
		m.state.Fallbacks[to] = a
		delete(m.state.Fallbacks, from)
	}
	if m.selected == from {
		m.selected = to
	}
	m.remapSessionRefs(from, to)
}

func (m *model) remapSessionRefs(from, to string) {
	for i := range m.state.Subs {
		if m.state.Subs[i].ID == from {
			m.state.Subs[i].ID = to
		}
		if m.state.Subs[i].Parent == from {
			m.state.Subs[i].Parent = to
		}
	}
	for i, id := range m.state.Cleanups {
		if id == from {
			m.state.Cleanups[i] = to
		}
	}
	if m.cleanup != nil && m.cleanup.sub.ID == from {
		m.cleanup.sub.ID = to
	}
	for i := range m.tasks {
		if m.tasks[i].Session == from {
			m.tasks[i].Session = to
		}
		if m.tasks[i].Project == from {
			m.tasks[i].Project = to
		}
	}
	for i, id := range m.state.Projects {
		if id == from {
			m.state.Projects[i] = to
		}
	}
	for _, links := range []map[string]string{m.state.Models, m.state.Roles, m.turns} {
		if v, ok := links[from]; ok {
			links[to] = v
			delete(links, from)
		}
	}
	if v, ok := m.projectNotes[from]; ok {
		m.projectNotes[to] = v
		delete(m.projectNotes, from)
	}
	if m.closing[from] {
		m.closing[to] = true
		delete(m.closing, from)
	}
	if v, ok := m.closeRetry[from]; ok {
		m.closeRetry[to] = v
		delete(m.closeRetry, from)
	}
	for i := range m.pageAnswers {
		if m.pageAnswers[i].id == from {
			m.pageAnswers[i].id = to
		}
	}
	for _, q := range m.state.Queues {
		for i, id := range q {
			if id == from {
				q[i] = to
			}
		}
	}
}

const cleanupGuard = time.Second

type cleanupDialog struct {
	sub     store.Subsession
	lastKey time.Time
}

type worktreeRemovedMsg struct {
	id  string
	err error
}

func (d *cleanupDialog) view(w, h int) string {
	body := []string{
		normal.Render(ansi.Truncate("Remove the worktree "+ctl.Worktree(d.sub.Repo, d.sub.Name)+" and all its files? (y/n)", w, "…")),
		gray.Render(ansi.Truncate("The branch "+d.sub.Branch+" stays.", w, "…")),
	}
	return page(body, gray.Render(ansi.Truncate("y remove  n keep", w, "")), h)
}

func (m *model) cleanupKey(msg tea.KeyPressMsg) tea.Cmd {
	key, now := msg.String(), time.Now()
	quiet := now.Sub(m.cleanup.lastKey) >= cleanupGuard
	m.cleanup.lastKey = now
	if !quiet || (key != "y" && key != "n" && key != "esc") {
		return nil
	}
	sub := m.cleanup.sub
	m.cleanup = nil
	m.state.Cleanups = slices.DeleteFunc(m.state.Cleanups, func(id string) bool { return id == sub.ID })
	cmds := []tea.Cmd{m.saveState()}
	if name, ok := m.runningIn(sub.Dir); key == "y" && ok {
		m.err = fmt.Errorf("%s is running; the worktree stays", name)
	} else if key == "y" {
		cmds = append(cmds, func() tea.Msg {
			return worktreeRemovedMsg{id: sub.ID, err: ctl.Remove(sub.Repo, sub.Name, deck.Trash)}
		})
	}
	if next := m.nextCleanup(); next != nil {
		cmds = append(cmds, next)
	} else if !m.dialogOpen() {
		cmds = append(cmds, m.zoom(false))
	}
	return tea.Batch(cmds...)
}

func (m *model) worktreeRemoved(msg worktreeRemovedMsg) tea.Cmd {
	if msg.err != nil {
		m.err = msg.err
		return nil
	}
	dir := ""
	if sub, ok := m.state.SubOf(msg.id); ok {
		dir = sub.Dir
	}
	m.state.Subs = slices.DeleteFunc(m.state.Subs, func(s store.Subsession) bool {
		return s.ID == msg.id || (s.Dir == dir && m.helperRole(s.ID))
	})
	m.rebuild()
	return m.saveState()
}

func (m *model) runningIn(dir string) (string, bool) {
	for _, s := range m.state.Subs {
		if s.Dir != dir {
			continue
		}
		if m.running(s.ID) {
			return s.Name, true
		}
	}
	return "", false
}

func (m *model) running(id string) bool {
	r, ok := m.rowByID(id)
	return m.resuming[id] || ok && (r.Kind == deck.Live || r.Kind == deck.Elsewhere || r.Job != "")
}
