package reviewui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/paths"
	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/tmux"
)

type model struct {
	id, home   string
	t          tmux.Client
	st         review.State
	pr         review.PR
	src        review.Source
	err        error
	fp         string
	ready      bool
	loading    bool
	dirty      bool
	want       string
	asked      map[int]bool
	files      []review.File
	sel        int
	showFixed  bool
	path       string
	lines      []review.Line
	code       [][]span
	cur, top   int
	recenter   bool
	layout     [][]int
	layoutW    int
	w, h       int
	commenting bool
	input      textinput.Model
	pending    string
	status     string
	statusErr  bool
}

type loadedMsg struct {
	st           review.State
	pr           review.PR
	src          review.Source
	files        []review.File
	fp           string
	notesMissing bool
	err          error
}

type diffMsg struct {
	path  string
	lines []review.Line
	code  [][]span
	err   error
}

type tickMsg struct{}

type fpMsg string

type fetchedMsg struct{ err error }

type reloadMsg struct{}

type statusMsg struct {
	text string
	err  bool
}

func Run(id string) error {
	home := paths.DeckHome()
	input := textinput.New()
	input.Prompt = ""
	m := &model{
		id:    id,
		home:  home,
		t:     tmux.Client{Socket: paths.Socket(), Conf: filepath.Join(home, "tmux.conf")},
		input: input,
		asked: map[int]bool{},
	}
	_, err := tea.NewProgram(m).Run()
	return err
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.requestLoad(), m.fetch(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) fetch() tea.Cmd {
	home, id := m.home, m.id
	return func() tea.Msg {
		st, ok, err := review.Load(home, id)
		pr, has := st.PR()
		if err != nil || !ok || !has {
			return fetchedMsg{}
		}
		return fetchedMsg{err: review.Fetch(st.Cwd, pr)}
	}
}

func (m *model) requestLoad() tea.Cmd {
	if m.loading {
		m.dirty = true
		return nil
	}
	m.loading = true
	return m.load()
}

func (m *model) load() tea.Cmd {
	home, id := m.home, m.id
	return func() tea.Msg {
		st, ok, err := review.Load(home, id)
		if err == nil && !ok {
			err = fmt.Errorf("no review for %s", id)
		}
		if err != nil {
			return loadedMsg{err: err}
		}
		pr, ok := st.PR()
		if !ok {
			return loadedMsg{st: st, err: fmt.Errorf("review has no PR")}
		}
		src, err := review.Resolve(st.Cwd, pr)
		if err != nil {
			return loadedMsg{st: st, pr: pr, err: err}
		}
		fp := fingerprint(home, id, src.Cwd, pr)
		files, err := review.Changed(src)
		if err != nil {
			return loadedMsg{st: st, pr: pr, src: src, err: err}
		}
		path := review.NotesPath(home, id, pr.Number)
		notes, _ := review.LoadNotes(path)
		_, statErr := os.Stat(path)
		return loadedMsg{st: st, pr: pr, src: src, files: review.Arrange(files, notes), fp: fp, notesMissing: statErr != nil}
	}
}

func fingerprint(home, id, dir string, pr review.PR) string {
	status, _ := exec.Command("git", "--no-optional-locks", "-C", dir, "status", "--porcelain", "-z").Output()
	head, _ := exec.Command("git", "--no-optional-locks", "-C", dir, "rev-parse", "HEAD").Output()
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%s", status, head, mtime(filepath.Join(review.Dir(home, id), "review.json")), mtime(review.NotesPath(home, id, pr.Number)))
	fields := strings.Split(string(status), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		b.WriteString("|" + mtime(filepath.Join(dir, f[3:])))
		if f[0] == 'R' || f[0] == 'C' {
			i++
		}
	}
	return b.String()
}

func mtime(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
}

func (m *model) checkFP() tea.Cmd {
	home, id, dir := m.home, m.id, m.src.Cwd
	return func() tea.Msg {
		st, _, _ := review.Load(home, id)
		pr, _ := st.PR()
		if dir == "" {
			dir = st.Cwd
		}
		return fpMsg(fingerprint(home, id, dir, pr))
	}
}

func (m *model) diff() tea.Cmd {
	f, ok := m.selected()
	if !ok {
		m.lines, m.code, m.path = nil, nil, ""
		return nil
	}
	src := m.src
	return func() tea.Msg {
		lines, err := review.FileDiff(src, f)
		return diffMsg{path: f.Path, lines: lines, code: highlight(f.Path, lines), err: err}
	}
}

func (m *model) visible(f review.File) bool {
	return m.showFixed || f.Group < review.Tests
}

func (m *model) selected() (review.File, bool) {
	if m.sel < 0 || m.sel >= len(m.files) || !m.visible(m.files[m.sel]) {
		return review.File{}, false
	}
	return m.files[m.sel], true
}

func (m *model) firstVisible() int {
	return slices.IndexFunc(m.files, m.visible)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.SetWidth(max(m.w-12, 1))
		return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tea.ClearScreen() })
	case tickMsg:
		if m.loading {
			return m, tick()
		}
		return m, tea.Batch(m.checkFP(), tick())
	case fpMsg:
		if string(msg) != m.fp {
			return m, m.requestLoad()
		}
	case fetchedMsg:
		if msg.err != nil {
			m.status, m.statusErr = "fetch failed, the diff may be old: "+msg.err.Error(), true
		}
		return m, m.requestLoad()
	case reloadMsg:
		return m, m.requestLoad()
	case loadedMsg:
		return m, m.loaded(msg)
	case diffMsg:
		m.diffed(msg)
	case retryMsg:
		if msg.out.comment && msg.out.text != m.pending {
			return m, nil
		}
		return m, m.send(msg.out)
	case sentMsg:
		return m, m.sent(msg)
	case statusMsg:
		m.status, m.statusErr = msg.text, msg.err
	case tea.KeyPressMsg:
		if m.commenting {
			return m, m.commentKey(msg)
		}
		return m, m.key(msg)
	default:
		if m.commenting && m.pending == "" {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}

func (m *model) loaded(msg loadedMsg) tea.Cmd {
	m.loading = false
	var again tea.Cmd
	if m.dirty {
		m.dirty = false
		again = m.requestLoad()
	}
	if m.want != "" && msg.pr.URL != m.want {
		return again
	}
	m.want = ""
	m.ready = true
	m.st, m.pr, m.src, m.err, m.fp = msg.st, msg.pr, msg.src, msg.err, msg.fp
	var ask tea.Cmd
	if msg.err == nil && msg.notesMissing && !slices.Contains(m.st.Asked, m.pr.Number) && !m.asked[m.pr.Number] {
		m.asked[m.pr.Number] = true
		ask = m.send(outbound{text: review.NotesPrompt(m.pr, m.src, review.NotesPath(m.home, m.id, m.pr.Number)), notes: m.pr.Number})
	}
	keep := ""
	if f, ok := m.selected(); ok {
		keep = f.Path
	}
	m.files = msg.files
	m.sel = slices.IndexFunc(m.files, func(f review.File) bool { return f.Path == keep && m.visible(f) })
	if m.sel < 0 {
		m.sel = m.firstVisible()
	}
	return tea.Batch(again, ask, m.diff())
}

func (m *model) markAsked(pr int) tea.Cmd {
	home, id := m.home, m.id
	return func() tea.Msg {
		_, err := review.Update(home, id, func(s *review.State) bool {
			if slices.Contains(s.Asked, pr) {
				return false
			}
			s.Asked = append(s.Asked, pr)
			return true
		})
		if err != nil {
			return statusMsg{text: err.Error(), err: true}
		}
		return nil
	}
}

func (m *model) diffed(msg diffMsg) {
	f, ok := m.selected()
	if !ok || f.Path != msg.path {
		return
	}
	if msg.err != nil {
		m.lines, m.code, m.path = nil, nil, msg.path
		m.status, m.statusErr = msg.err.Error(), true
		return
	}
	var at review.Line
	if msg.path == m.path && m.cur < len(m.lines) {
		at = m.lines[m.cur]
	}
	m.lines, m.code, m.layout = msg.lines, msg.code, nil
	if msg.path != m.path {
		m.path, m.cur, m.top = msg.path, 0, 0
		if i := slices.IndexFunc(m.lines, func(l review.Line) bool { return l.Kind != review.Same }); i >= 0 {
			m.cur, m.recenter = i, true
		}
		return
	}
	if i := slices.IndexFunc(m.lines, func(l review.Line) bool { return l.Kind == at.Kind && l.New == at.New && l.Old == at.Old }); i >= 0 {
		m.cur = i
	}
	m.cur = min(m.cur, max(len(m.lines)-1, 0))
}

func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	m.status, m.statusErr = "", false
	switch msg.String() {
	case "j", "down":
		m.moveLine(1)
	case "k", "up":
		m.moveLine(-1)
	case "ctrl+d", "pgdown":
		m.page(1)
	case "ctrl+u", "pgup":
		m.page(-1)
	case "n":
		m.nextChange(1)
	case "N":
		m.nextChange(-1)
	case "]":
		return m.moveFile(1)
	case "[":
		return m.moveFile(-1)
	case "J":
		return m.movePR(1)
	case "K":
		return m.movePR(-1)
	case "t":
		m.showFixed = !m.showFixed
		if _, ok := m.selected(); !ok {
			m.sel = m.firstVisible()
			return m.diff()
		}
	case "tab":
		return m.focusClaude("")
	case "ctrl+j":
		return m.focusClaude("C-j")
	case "ctrl+k":
		return m.focusClaude("C-k")
	case "ctrl+q":
		return m.focusClaude("C-q")
	case "c":
		if len(m.lines) == 0 || m.err != nil || m.path == "" {
			return nil
		}
		m.commenting = true
		return m.input.Focus()
	}
	return nil
}

func (m *model) moveLine(d int) {
	m.cur = min(max(m.cur+d, 0), max(len(m.lines)-1, 0))
}

func (m *model) page(d int) {
	w := m.diffWidth()
	for left := m.room() / 2; left > 0; {
		next := m.cur + d
		if next < 0 || next >= len(m.lines) {
			return
		}
		m.cur = next
		left -= len(m.rowStarts(next, w))
	}
}

func (m *model) nextChange(d int) {
	start := func(i int) bool {
		return m.lines[i].Kind != review.Same && (i == 0 || m.lines[i-1].Kind == review.Same)
	}
	for i := m.cur + d; i >= 0 && i < len(m.lines); i += d {
		if start(i) {
			m.cur, m.recenter = i, true
			return
		}
	}
}

func (m *model) moveFile(d int) tea.Cmd {
	for i := m.sel + d; i >= 0 && i < len(m.files); i += d {
		if m.visible(m.files[i]) {
			m.sel = i
			return m.diff()
		}
	}
	return nil
}

func (m *model) movePR(d int) tea.Cmd {
	next := m.st.Current + d
	if next < 0 || next >= len(m.st.PRs) {
		return nil
	}
	m.st.Current = next
	m.pr = m.st.PRs[next]
	m.want = m.pr.URL
	m.files, m.lines, m.code, m.path, m.sel = nil, nil, nil, "", -1
	home, id, want := m.home, m.id, m.want
	save := func() tea.Msg {
		_, err := review.Update(home, id, func(s *review.State) bool {
			i := slices.IndexFunc(s.PRs, func(p review.PR) bool { return p.URL == want })
			if i < 0 || s.Current == i {
				return false
			}
			s.Current = i
			return true
		})
		if err != nil {
			return statusMsg{text: err.Error(), err: true}
		}
		return reloadMsg{}
	}
	return tea.Sequence(save, m.fetch())
}

func (m *model) commentKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.pending == "" {
		m.status, m.statusErr = "", false
	}
	switch msg.String() {
	case "esc":
		m.commenting, m.pending = false, ""
		m.status, m.statusErr = "", false
		m.input.SetValue("")
		m.input.Blur()
		return nil
	case "enter":
		text := m.input.Value()
		f, ok := m.selected()
		if text == "" || !ok || f.Path != m.path || m.cur >= len(m.lines) || m.pending != "" {
			return nil
		}
		m.pending = review.CommentPrompt(m.pr, m.src, m.path, m.lines[m.cur], text)
		return m.send(outbound{text: m.pending, comment: true})
	}
	if m.pending != "" {
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}
