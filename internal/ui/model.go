package ui

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/accounts"
	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/events"
	"github.com/benteven/deckhand/internal/gh"
	"github.com/benteven/deckhand/internal/live"
	"github.com/benteven/deckhand/internal/paths"
	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/roles"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/tasks"
	"github.com/benteven/deckhand/internal/tmux"
	"github.com/benteven/deckhand/internal/transcript"
	"github.com/benteven/deckhand/internal/update"
)

const (
	tickEvery   = time.Second
	listEvery   = 5 * time.Second
	saveEvery   = 30 * time.Second
	agentWindow = 90 * time.Second
)

type tickMsg struct{}

type updateMsg string

const (
	prTTL      = 60 * time.Second
	prErrorTTL = 5 * time.Minute
	prParallel = 4
)

type prEntry struct {
	state   string
	checks  string
	checked time.Time
	until   time.Time
	pending bool
}

func prRecheck(state string, checked time.Time) time.Time {
	if state == "merged" || state == "closed" {
		return time.Unix(1<<62, 0)
	}
	return checked.Add(prTTL)
}

func loadedPRs(saved map[string]store.PRState) map[string]*prEntry {
	prs := make(map[string]*prEntry, len(saved))
	for url, s := range saved {
		prs[url] = &prEntry{state: s.State, checks: s.Checks, checked: s.Checked, until: prRecheck(s.State, s.Checked)}
	}
	return prs
}

func (m *model) savePRs() tea.Cmd {
	snap := map[string]store.PRState{}
	for url, e := range m.prs {
		if e.state != "" {
			snap[url] = store.PRState{State: e.state, Checks: e.checks, Checked: e.checked}
		}
	}
	sv, home := m.prSaver, m.home
	sv.mu.Lock()
	sv.prs = snap
	sv.mu.Unlock()
	return func() tea.Msg {
		sv.mu.Lock()
		defer sv.mu.Unlock()
		if err := store.SavePRs(home, sv.prs); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

type prStateMsg struct {
	url, state, checks string
	err                error
}

type paneProc struct {
	pane string
	pid  int
}

type pidStarts struct {
	mu sync.Mutex
	at map[paneProc]time.Time
}

func (s *pidStarts) get(k paneProc) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.at[k]; ok {
		return t
	}
	t, _, err := deck.ProcessStart(k.pid)
	if err != nil {
		return time.Time{}
	}
	s.at[k] = t
	return t
}

func (s *pidStarts) keep(keys map[paneProc]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.at {
		if !keys[k] {
			delete(s.at, k)
		}
	}
}

type refreshMsg struct {
	starts    map[int]time.Time
	panes     []tmux.Pane
	live      []live.Session
	events    map[string]events.State
	files     []transcript.File
	summaries map[string]transcript.Summary
	agents    map[string][]transcript.Agent
	folders   []string
	geo       geometry
	geoOK     bool
	accounts  []accounts.Account
	reviews   map[string]review.State
	requests  []ctl.Request
	err       error
}

const (
	modeActive = iota
	modeSuspended
)

type position struct {
	cursor   int
	selected string
}

type startedMsg struct {
	id, cwd, name, pane string
	err, warn           error
	rev                 *review.State
	req, out            string
	background          bool
}

type errMsg struct{ err error }

type quitFailedMsg struct{ err error }

type takenOverMsg struct {
	row deck.Row
	err error
}

type stopFailedMsg struct {
	entry store.ActiveEntry
	err   error
}

type slotWant struct {
	mu      sync.Mutex
	pane    string
	target  string
	preview bool
	review  string
}

type prSaver struct {
	mu  sync.Mutex
	prs map[string]store.PRState
}

type stateSaver struct {
	mu    sync.Mutex
	state store.State
}

type taskSaver struct {
	mu    sync.Mutex
	tasks []tasks.Task
}

type model struct {
	ops        deck.Ops
	reader     *transcript.Reader
	home       string
	claudeHome string
	state      store.State

	in       deck.Inputs
	folders  []string
	listedAt time.Time
	savedAt  time.Time
	loaded   bool

	windowWidth int
	dragSeen    int

	active    []deck.Row
	suspended []deck.Row
	selected  string
	cursor    int

	mode      int
	other     position
	query     string
	searching bool
	search    textinput.Model
	index     *transcript.Index
	text      textHits
	resuming  map[string]bool
	batch     map[string]bool
	prompt    *resumePrompt

	previewPane  string
	slot         string
	want         *slotWant
	saver        *stateSaver
	tasks        []tasks.Task
	taskSaver    *taskSaver
	prevSelected string

	focused       bool
	width, height int
	hint          string
	err           error

	version, latest string

	noGH    bool
	prs     map[string]*prEntry
	prSem   chan struct{}
	prSaver *prSaver

	ending   map[string]bool
	followed map[string]string
	starts   *pidStarts
	picker   *picker
	rename   *renameDialog
	arts     *artifactList
	prPick   *prPicker
	reviews  map[string]review.State

	startingReview map[string]bool
	findingPRs     map[string]bool
	prInfos        map[string]gh.PR
	takeover       *takeoverDialog
	quit           *quitDialog
	del            *deleteDialog
	restart        *restartDialog
	handoff        *handoffDialog
	help           *helpScreen
	accounts       *accountDialog
	cleanup        *cleanupDialog

	switches      map[string]bool
	continueAfter map[string]bool
	paneAccount   map[string]string
	loginPane     string
	loginName     string
	accountList   []accounts.Account

	cycle    *cycle
	handoffs map[string]*pendingHandoff

	agentMode bool
	assist    *assistantDialog
	agent     string
	gone      map[string]bool
	quitting  bool
	inDialog  bool
	requests  map[string]time.Time

	assistantTry  time.Time
	stopAssistant bool
	updating      bool
	restartDue    bool
	notes         []string
	turns         map[string]string
	stoppedAt     map[string]time.Time
	reprompt      time.Time
	typing        map[string]bool
}

func Run(version string) error {
	home := paths.DeckHome()
	cfg, err := store.LoadConfig(home)
	if err != nil {
		return err
	}
	state, err := store.Load(home)
	if err != nil {
		return err
	}
	taskList, err := tasks.Load(home)
	if err != nil {
		return err
	}
	saved, err := store.LoadPRs(home)
	if err != nil {
		saved = map[string]store.PRState{}
	}
	reader := transcript.NewReader(filepath.Join(home, "summaries.json"))
	search := textinput.New()
	search.Prompt = "/"
	styles := search.Styles()
	styles.Focused.Prompt = yellow
	styles.Blurred.Prompt = yellow
	search.SetStyles(styles)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	rules, assistantID := "", ""
	if cfg.Assistant {
		rules, assistantID = ctl.Rules(), state.Assistant.ID
	}
	m := &model{
		search:   search,
		index:    transcript.NewIndex(),
		resuming: map[string]bool{},
		batch:    map[string]bool{},
		ops: deck.Ops{
			T:         tmux.Client{Socket: paths.Socket(), Conf: filepath.Join(home, "tmux.conf")},
			Cfg:       cfg,
			HooksPath: filepath.Join(home, "hooks.json"),
			Home:      home,

			Exe:                exe,
			PluginDir:          filepath.Join(home, "plugin"),
			AssistantPluginDir: filepath.Join(home, "plugin-assistant"),
			AssistantDir:       filepath.Join(home, "assistant"),
			AssistantID:        assistantID,
			Rules:              rules,
		},
		reader:      reader,
		home:        home,
		claudeHome:  paths.ClaudeHome(),
		accountList: accounts.List(home, paths.ClaudeHome()),
		state:       state,
		want:        &slotWant{},
		saver:       &stateSaver{},
		tasks:       taskList,
		taskSaver:   &taskSaver{},
		starts:      &pidStarts{at: map[paneProc]time.Time{}},
		prs:         loadedPRs(saved),
		prSem:       make(chan struct{}, prParallel),
		prSaver:     &prSaver{},
		noGH:        !hasGH(),
		version:     version,
	}
	_, err = tea.NewProgram(m).Run()
	if serr := reader.Save(); err == nil {
		err = serr
	}
	return err
}

func (m *model) Init() tea.Cmd {
	home := m.home
	return tea.Batch(m.refresh(), m.dialogFlag(), m.nextCleanup(), func() tea.Msg { return updateMsg(update.Latest(home, time.Now())) })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.update(msg)
	if open := m.dialogOpen() || m.searching; open != m.inDialog {
		m.inDialog = open
		cmd = tea.Batch(cmd, m.dialogFlag())
	}
	return m, cmd
}

func (m *model) dialogFlag() tea.Cmd {
	flag := "0"
	if m.inDialog {
		flag = "1"
	}
	return m.tmux("set-option", "-p", "-t", tmux.ListPane, "@deck-dialog", flag)
}

func (m *model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case updateMsg:
		m.latest = string(msg)
		home := m.home
		return m, tea.Tick(time.Hour, func(now time.Time) tea.Msg { return updateMsg(update.Latest(home, now)) })
	case inspectedMsg:
		if p := m.picker; p != nil && p.input.Value() == msg.from {
			changed := msg.path != p.path || msg.state != p.state
			p.path, p.state, p.checked = msg.path, msg.state, msg.from
			p.filter()
			if changed && p.state == pathOK {
				p.cursor = 0
			}
		}
		return m, nil
	case ghostMsg:
		if p := m.picker; p != nil && p.input.Value() == msg.from {
			p.ghost, p.ghostOf = msg.to, msg.from
			p.filter()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.picker != nil {
			m.picker.input.SetWidth(max(m.width-4, 1))
		}
		m.search.SetWidth(max(m.width-2, 1))
	case tea.FocusMsg:
		m.focused = true
		m.prevSelected = m.selected
	case tea.BlurMsg:
		m.focused = false
	case cycleDoneMsg:
		return m, m.cycleDone(msg)
	case ctrlPollMsg:
		return m, m.ctrlPolled(msg)
	case tickMsg:
		if m.quitting {
			return m, nil
		}
		return m, m.refresh()
	case refreshMsg:
		if m.quitting {
			return m, nil
		}
		next := tea.Tick(tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
		if msg.err != nil {
			m.err = msg.err
			return m, next
		}
		prev := m.in.Events
		applied := m.apply(msg)
		return m, tea.Batch(next, m.dragged(msg.geo, msg.geoOK), applied, m.handleRequests(msg.requests), m.waitNotes(prev, msg.events), m.keepAssistant(time.Now()), m.pruneQueues(time.Now()), m.deliverTurns())
	case prStateMsg:
		e := m.prs[msg.url]
		if e == nil {
			e = &prEntry{}
			m.prs[msg.url] = e
		}
		e.pending = false
		if msg.err != nil {
			e.until = time.Now().Add(prErrorTTL)
		} else {
			e.state, e.checks, e.checked = msg.state, msg.checks, time.Now()
			e.until = prRecheck(e.state, e.checked)
			return m, m.savePRs()
		}
	case loginMsg:
		return m, m.loginStarted(msg)
	case accountRemovedMsg:
		return m, m.accountRemoved(msg)
	case textHitsMsg:
		return m, m.gotTextHits(msg)
	case startedMsg:
		return m, m.started(msg)
	case typedMsg:
		return m, m.typed(msg)
	case worktreeRemovedMsg:
		return m, m.worktreeRemoved(msg)
	case subCreatedMsg:
		return m, m.subCreated(msg)
	case taskCreatedMsg:
		return m, m.taskCreated(msg)
	case roleFoundMsg:
		return m, m.roleFound(msg)
	case queueNamedMsg:
		return m, m.queueNamed(msg)
	case cleanupMsg:
		return m, m.queueCleanup(msg.id)
	case restartedMsg:
		return m, m.restarted(msg)
	case updatedMsg:
		m.updated(msg)
	case pausedMsg:
		return m, m.paused(msg)
	case takenOverMsg:
		return m, m.tookOver(msg)
	case trashedMsg:
		return m, m.trashed(msg)
	case quitFailedMsg:
		m.quitting = false
		m.err = msg.err
		return m, m.refresh()
	case errMsg:
		m.err = msg.err
	case reviewFailedMsg:
		m.reviewFailed(msg)
	case stackMsg:
		return m, m.gotStack(msg)
	case prInfoMsg:
		m.gotPRInfo(msg)
	case stopFailedMsg:
		m.err = msg.err
		delete(m.ending, msg.entry.SessionID)
		if !slices.ContainsFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == msg.entry.SessionID }) {
			entry := msg.entry
			entry.Num = store.Claim(m.state.Active, entry.Num)
			m.state.Active = append(m.state.Active, entry)
		}
		m.rebuild()
		return m, m.saveState()
	case tea.MouseClickMsg:
		return m, m.click(msg.Mouse())
	case tea.MouseWheelMsg:
		return m, m.wheel(msg.Mouse())
	case tea.KeyPressMsg:
		if msg.Text == tmux.NextKey || msg.Text == tmux.PrevKey {
			return m, m.switchLive(msg.Text == tmux.NextKey)
		}
		if msg.Text == tmux.FamilyKey {
			return m, m.switchFamily()
		}
		if msg.Text == tmux.LastKey || msg.Text == tmux.BackKey {
			return m, m.toggleLast(msg.Text == tmux.BackKey)
		}
		m.hint = ""
		m.err = nil
		if m.quitting {
			return m, nil
		}
		if m.cleanup != nil {
			return m, m.cleanupKey(msg)
		}
		if m.prompt != nil {
			return m, m.promptKey(msg)
		}
		if m.takeover != nil {
			return m, m.takeoverKey(msg)
		}
		if m.quit != nil {
			return m, m.quitKey(msg)
		}
		if m.del != nil {
			return m, m.deleteKey(msg)
		}
		if m.restart != nil {
			return m, m.restartKey(msg)
		}
		if m.assist != nil {
			return m, m.assistantKey(msg)
		}
		if m.handoff != nil {
			return m, m.handoffKey(msg)
		}
		if m.help != nil {
			return m, m.helpKey(msg)
		}
		if m.accounts != nil {
			return m, m.accountsKey(msg)
		}
		if m.picker != nil {
			return m, m.pickerKey(msg)
		}
		if m.rename != nil {
			return m, m.renameKey(msg)
		}
		if m.arts != nil {
			return m, m.artifactsKey(msg)
		}
		if m.prPick != nil {
			return m, m.prPickKey(msg)
		}
		if m.searching {
			return m, m.searchKey(msg)
		}
		return m, m.key(msg)
	default:
		var cmd tea.Cmd
		switch {
		case m.picker != nil:
			m.picker.input, cmd = m.picker.input.Update(msg)
		case m.rename != nil:
			m.rename.input, cmd = m.rename.input.Update(msg)
		case m.searching:
			m.search, cmd = m.search.Update(msg)
		case m.accounts != nil && m.accounts.naming:
			m.accounts.input, cmd = m.accounts.input.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m *model) key(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		return m.move(-1)
	case "down", "j":
		return m.move(1)
	case "enter":
		return m.enter()
	case "tab":
		return m.toggleMode()
	case "<":
		return m.resizeList(-widthStep)
	case ">":
		return m.resizeList(widthStep)
	case "/":
		m.searching = true
		return m.search.Focus()
	case "esc":
		if m.query != "" {
			return m.clearSearch()
		}
		if m.mode == modeSuspended {
			return nil
		}
		m.selected = m.prevSelected
		m.syncCursor()
		return tea.Sequence(m.showSelected(), m.focus(tmux.Slot))
	case "n":
		m.picker = newPicker(m.folders, m.width)
		m.picker.account, m.picker.accounts = m.cursorAccount(), m.loggedIn()
		return tea.Batch(m.zoom(true), m.picker.input.Focus())
	case "s", "r":
		r, ok := m.current()
		if !ok || (msg.String() == "s" && r.Kind != deck.Live && r.Kind != deck.Stale) {
			return nil
		}
		if msg.String() == "s" && m.isAssistant(r.SessionID) {
			if r.Kind != deck.Live || m.resuming[r.SessionID] {
				return nil
			}
			return m.pauseAssistant(r)
		}
		if msg.String() == "s" && r.Kind == deck.Stale {
			return m.dropStale(r)
		}
		order := make([]string, 0, len(m.shown()))
		for _, s := range m.shown() {
			order = append(order, s.SessionID)
		}
		m.rename = newRenameDialog(r, msg.String() == "s", order)
		return m.rename.input.Focus()
	case "t":
		r, ok := m.current()
		if !ok {
			return nil
		}
		if r.Kind != deck.Elsewhere {
			return m.moveAccount(r)
		}
		m.takeover = &takeoverDialog{row: r}
		return m.zoom(true)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.jump(int(msg.String()[0] - '0'))
	case "d":
		r, ok := m.current()
		if !ok || r.Kind != deck.Suspended || m.resuming[r.SessionID] {
			return nil
		}
		if r.Assistant {
			m.hint = "The assistant cannot be deleted"
			return nil
		}
		m.del = &deleteDialog{row: r}
		return m.zoom(true)
	case "R":
		r, ok := m.current()
		if !ok || m.mode != modeActive || r.Kind != deck.Live || m.resuming[r.SessionID] {
			return nil
		}
		m.restart = &restartDialog{row: r}
		return m.zoom(true)
	case "g":
		if m.mode != modeActive {
			return nil
		}
		return m.toggleAgentMode()
	case "G":
		m.assist = &assistantDialog{on: !m.ops.Cfg.Assistant, dir: m.ops.AssistantDir}
		return m.zoom(true)
	case "c":
		if r, ok := m.current(); ok {
			return m.startHandoff(r)
		}
		return nil
	case "o":
		if r, ok := m.current(); ok {
			return m.openPRs(r)
		}
		return nil
	case "a":
		if r, ok := m.current(); ok {
			return m.openArtifacts(r)
		}
		return nil
	case "v":
		if r, ok := m.current(); ok {
			return m.openReview(r)
		}
		return nil
	case "q":
		text := fmt.Sprintf("deckhand closed, %s still running", sessionCount(len(m.liveRows())))
		return m.tmux("detach-client", "-E", "echo "+tmux.ShellQuote(text))
	case "Q":
		m.quit = &quitDialog{count: len(m.liveRows())}
		return m.zoom(true)
	case "A":
		return m.openAccounts()
	case "?":
		m.help = &helpScreen{}
		return m.zoom(true)
	case "U":
		exe, err := os.Executable()
		if err != nil {
			m.err = err
			return nil
		}
		m.hint = "Upgrading, the list reloads when done"
		t, version := m.ops.T, m.version
		return func() tea.Msg {
			exe, err := update.Install(exe, version)
			if err == nil {
				_, err = t.Run("run-shell", "-b", tmux.ShellQuote(exe)+" reload")
			}
			if err != nil {
				return errMsg{err}
			}
			return nil
		}
	}
	return nil
}

func (m *model) dialogOpen() bool {
	return m.cleanup != nil || m.prompt != nil || m.picker != nil || m.rename != nil || m.arts != nil || m.prPick != nil || m.takeover != nil || m.quit != nil || m.del != nil || m.restart != nil || m.assist != nil || m.handoff != nil || m.help != nil || m.accounts != nil
}

func (m *model) liveRows() []deck.Row {
	var rows []deck.Row
	for _, r := range m.active {
		if r.Kind == deck.Live {
			rows = append(rows, r)
		}
	}
	return rows
}

func (m *model) switchLive(next bool) tea.Cmd {
	if m.quitting || m.dialogOpen() {
		return nil
	}
	rows := m.liveRows()
	if len(rows) == 0 {
		return nil
	}
	i := slices.IndexFunc(rows, func(r deck.Row) bool { return r.PaneID == m.slot })
	if i < 0 {
		i = slices.IndexFunc(rows, func(r deck.Row) bool { return r.SessionID == m.selected })
	}
	step := 1
	if !next {
		step = -1
	}
	switch {
	case i >= 0:
		i = (i + step + len(rows)) % len(rows)
	case next:
		i = 0
	default:
		i = len(rows) - 1
	}
	return m.switchTo(rows[i])
}

func (m *model) switchFamily() tea.Cmd {
	if m.quitting || m.dialogOpen() {
		return nil
	}
	id := m.selected
	if i := slices.IndexFunc(m.active, func(r deck.Row) bool { return r.PaneID == m.slot && m.slot != "" }); i >= 0 {
		id = m.active[i].SessionID
	}
	family := slices.DeleteFunc(deck.Family(m.active, id), func(r deck.Row) bool { return r.Kind != deck.Live })
	if len(family) < 2 {
		return nil
	}
	i := slices.IndexFunc(family, func(r deck.Row) bool { return r.SessionID == id })
	return m.switchTo(family[(i+1)%len(family)])
}

func (m *model) jump(n int) tea.Cmd {
	if m.quitting || m.dialogOpen() {
		return nil
	}
	i := slices.IndexFunc(m.active, func(r deck.Row) bool {
		return r.Num == n && (r.Kind == deck.Live || r.Kind == deck.Stale)
	})
	if i < 0 {
		return nil
	}
	r := m.active[i]
	if r.Kind == deck.Live {
		return m.switchTo(r)
	}
	m.selectRow(r.SessionID)
	return tea.Sequence(m.showSelected(), m.focus(tmux.ListPane))
}

func (m *model) selectRow(id string) {
	m.setMode(modeActive)
	m.selected = id
	if !slices.ContainsFunc(m.shown(), func(s deck.Row) bool { return s.SessionID == id }) {
		m.resetSearch()
	}
	m.syncCursor()
}

func (m *model) switchTo(r deck.Row) tea.Cmd {
	m.cycle = nil
	m.selectRow(r.SessionID)
	return tea.Sequence(m.show(r.PaneID, "", false), m.focus(tmux.Slot))
}

func (m *model) openPRs(r deck.Row) tea.Cmd {
	var urls []string
	for _, pr := range visiblePRs(r.Summary.PRs, m.prState) {
		if linkable(pr.URL) {
			urls = append(urls, pr.URL)
		}
	}
	if len(urls) == 0 {
		return nil
	}
	return openURLs(urls)
}

func openURLs(urls []string) tea.Cmd {
	opener := os.Getenv("DECKHAND_OPEN")
	if opener == "" {
		opener = defaultOpener()
	}
	return func() tea.Msg {
		for _, u := range urls {
			if out, err := exec.Command(opener, u).CombinedOutput(); err != nil {
				return errMsg{fmt.Errorf("open %s: %w: %s", u, err, strings.TrimSpace(string(out)))}
			}
		}
		return nil
	}
}

func (m *model) stopAll() tea.Cmd {
	ops, preview, rows := m.ops, m.previewPane, m.liveRows()
	return func() tea.Msg {
		if preview != "" {
			ops.Show(preview)
		}
		var wg sync.WaitGroup
		for _, r := range rows {
			wg.Go(func() { ops.Stop(r.PaneID, r.Busy) })
		}
		wg.Wait()
		wait := fmt.Sprintf(`i=0; while tmux -L "%s" list-sessions >/dev/null 2>&1; do i=$((i+1)); [ $i -ge 50 ] && exit 1; sleep 0.1; done; echo "deckhand stopped"`, ops.T.Socket)
		if _, err := ops.T.Run("detach-client", "-E", "sh -c "+tmux.ShellQuote(wait)); err != nil {
			return quitFailedMsg{err}
		}
		if _, err := ops.T.Run("kill-server"); err != nil {
			return quitFailedMsg{err}
		}
		return nil
	}
}

func (m *model) withoutEnding(sessions []live.Session) []live.Session {
	if len(m.ending) == 0 {
		return sessions
	}
	return slices.DeleteFunc(slices.Clone(sessions), func(l live.Session) bool { return m.ending[l.SessionID] })
}

func (m *model) markEnding(id string) {
	if m.ending == nil {
		m.ending = map[string]bool{}
	}
	m.ending[id] = true
	m.in.Live = m.withoutEnding(m.in.Live)
}

func (m *model) confirmRename(d *renameDialog) tea.Cmd {
	if name := strings.TrimSpace(d.input.Value()); name != "" {
		if m.state.Names == nil {
			m.state.Names = map[string]string{}
		}
		m.state.Names[d.id] = name
	}
	if !d.suspend {
		m.rebuild()
		return tea.Batch(m.saveState(), m.refreshPreview())
	}
	i := slices.IndexFunc(m.shown(), func(r deck.Row) bool { return r.SessionID == d.id })
	if i < 0 || m.shown()[i].Kind != deck.Live || m.shown()[i].PaneID != d.pane {
		return m.saveState()
	}
	return m.suspend(m.shown()[i], "")
}

func (m *model) suspend(r deck.Row, req string) tea.Cmd {
	var entry store.ActiveEntry
	for _, e := range m.state.Active {
		if e.SessionID == r.SessionID {
			entry = e
		}
	}
	m.state.Active = slices.DeleteFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == r.SessionID })
	m.markEnding(r.SessionID)
	m.rebuild()
	drop := m.dropFromQueues(r.SessionID)
	ops, pane, busy, home := m.ops, r.PaneID, r.Busy, m.home
	sub := m.ownsWorktree(r.SessionID)
	stop := func() tea.Msg {
		err := ops.Stop(pane, busy)
		if req != "" {
			rep := ctl.Reply{Out: "closed"}
			if err != nil {
				rep = ctl.Reply{Err: err.Error()}
			}
			if aerr := ctl.Answer(home, req, rep); aerr != nil && err == nil {
				return errMsg{aerr}
			}
		}
		if err != nil {
			return stopFailedMsg{entry: entry, err: err}
		}
		if sub {
			return cleanupMsg{r.SessionID}
		}
		return nil
	}
	return tea.Batch(drop, m.saveState(), tea.Sequence(m.showSelected(), stop))
}

func (m *model) refreshPreview() tea.Cmd {
	if r, ok := m.current(); ok && r.Kind != deck.Live && m.slot == m.previewPane {
		return m.showSelected()
	}
	return nil
}

func (m *model) endDead(panes []tmux.Pane, slotPane string) tea.Cmd {
	var kills, drops []tea.Cmd
	inSlot := false
	for _, p := range panes {
		if !p.Dead || p.SessionID == "" || m.resuming[p.SessionID] {
			continue
		}
		before := len(m.state.Active)
		m.state.Active = slices.DeleteFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == p.SessionID })
		if len(m.state.Active) == before {
			continue
		}
		inSlot = inSlot || p.ID == slotPane
		m.markEnding(p.SessionID)
		drops = append(drops, m.dropFromQueues(p.SessionID))
		if m.isAssistant(p.SessionID) {
			kills = append(kills, m.assistantDied(p.ID))
			continue
		}
		kills = append(kills, m.tmux("kill-pane", "-t", p.ID))
	}
	if len(kills) == 0 {
		return nil
	}
	moved := m.rebuild()
	var seq []tea.Cmd
	if inSlot || moved {
		seq = append(seq, m.showSelected())
	}
	seq = append(seq, kills...)
	return tea.Batch(tea.Batch(drops...), m.saveState(), tea.Sequence(seq...))
}

func (m *model) move(d int) tea.Cmd {
	if m.agentMode {
		return m.moveAgent(d)
	}
	return m.selectIndex(m.cursor + d)
}

func (m *model) selectIndex(i int) tea.Cmd {
	rows := m.shown()
	if len(rows) == 0 {
		return nil
	}
	m.cursor = min(max(i, 0), len(rows)-1)
	m.selected = rows[m.cursor].SessionID
	m.agent = ""
	return m.showSelected()
}

func (m *model) shown() []deck.Row {
	if m.mode == modeSuspended {
		return m.shownSuspended()
	}
	return deck.Filter(m.active, m.query)
}

func (m *model) setMode(mode int) bool {
	if m.mode == mode {
		return false
	}
	cur := position{m.cursor, m.selected}
	m.cursor, m.selected = m.other.cursor, m.other.selected
	m.agent = ""
	m.other = cur
	m.mode = mode
	m.resetSearch()
	m.syncCursor()
	return true
}

func (m *model) resetSearch() {
	m.query = ""
	m.searching = false
	m.search.Reset()
	m.search.Blur()
}

func (m *model) toggleMode() tea.Cmd {
	next := modeSuspended
	if m.mode == modeSuspended {
		next = modeActive
	}
	m.setMode(next)
	return m.showSelected()
}

func (m *model) clearSearch() tea.Cmd {
	m.resetSearch()
	m.syncCursor()
	return m.showSelected()
}

func (m *model) searchKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		return m.clearSearch()
	case "enter":
		m.searching = false
		m.search.Blur()
		return nil
	case "up":
		return m.move(-1)
	case "down":
		return m.move(1)
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	if value := m.search.Value(); value != m.query {
		m.query = value
		m.cursor, m.selected = 0, ""
		m.syncCursor()
		cmd = tea.Batch(cmd, m.showSelected(), m.searchText())
	}
	return cmd
}

func (m *model) enter() tea.Cmd {
	r, ok := m.current()
	if !ok {
		return nil
	}
	m.agent = ""
	switch r.Kind {
	case deck.Elsewhere:
		m.hint = "running in another terminal"
		return nil
	case deck.Live:
		return tea.Sequence(m.show(r.PaneID, "", false), m.focus(tmux.Slot))
	case deck.Suspended, deck.Stale:
		if m.resuming[r.SessionID] {
			return nil
		}
		m.resuming[r.SessionID] = true
		delete(m.ending, r.SessionID)
		if m.isAssistant(r.SessionID) && m.state.Assistant.Paused {
			m.state.Assistant.Paused = false
			m.assistantTry = time.Now()
			return tea.Batch(m.saveState(), m.resume(r))
		}
		return m.resume(r)
	}
	return nil
}

func (m *model) started(msg startedMsg) tea.Cmd {
	delete(m.resuming, msg.id)
	inBatch := m.batch[msg.id]
	delete(m.batch, msg.id)
	if msg.err != nil {
		if msg.req != "" {
			return m.answer(msg.req, ctl.Reply{Err: msg.err.Error()})
		}
		m.err = msg.err
		if msg.background {
			return nil
		}
		if inBatch {
			return m.finishBatch()
		}
		return m.zoom(false)
	}
	if msg.warn != nil {
		m.err = msg.warn
	}
	if msg.rev != nil {
		m.reviewStarted(msg.id, *msg.rev)
	}
	if msg.req == "" && !msg.background {
		m.setMode(modeActive)
	}
	entry := store.ActiveEntry{SessionID: msg.id, Cwd: msg.cwd, Name: msg.name, LastInteraction: time.Now()}
	if i := slices.IndexFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == msg.id }); i >= 0 {
		if inBatch {
			entry.LastInteraction = m.state.Active[i].LastInteraction
		}
		entry.Num = m.state.Active[i].Num
		m.state.Active = slices.Delete(m.state.Active, i, i+1)
	}
	entry.Num = store.Claim(m.state.Active, entry.Num)
	m.state.Active = append(m.state.Active, entry)
	if m.in.Panes == nil {
		m.in.Panes = map[string]string{}
	}
	m.in.Panes[msg.id] = msg.pane
	if msg.id == m.state.Assistant.ID && m.stopAssistant {
		m.rebuild()
		return tea.Batch(m.saveState(), m.stopLateAssistant(msg.id))
	}
	if msg.req != "" {
		m.rebuild()
		return tea.Batch(m.saveState(), m.answer(msg.req, ctl.Reply{Out: msg.out}))
	}
	if msg.background {
		m.rebuild()
		return m.saveState()
	}
	if inBatch {
		m.rebuild()
		return tea.Batch(m.saveState(), m.finishBatch())
	}
	m.selected = msg.id
	m.rebuild()
	if !slices.ContainsFunc(m.shown(), func(r deck.Row) bool { return r.SessionID == msg.id }) {
		m.resetSearch()
		m.selected = msg.id
		m.syncCursor()
	}
	return tea.Batch(m.saveState(), tea.Sequence(m.zoom(false), m.show(msg.pane, "", false), m.focus(tmux.Slot)))
}

func (m *model) finishBatch() tea.Cmd {
	if len(m.batch) > 0 {
		return nil
	}
	i := slices.IndexFunc(m.active, func(r deck.Row) bool { return r.Kind == deck.Live })
	if i < 0 {
		return nil
	}
	m.resetSearch()
	m.selected = m.active[i].SessionID
	m.syncCursor()
	return tea.Sequence(m.showSelected(), m.focus(tmux.Slot))
}

func (m *model) staleRows() []deck.Row {
	var rows []deck.Row
	for _, r := range m.active {
		if r.Kind == deck.Stale && !m.isAssistant(r.SessionID) {
			rows = append(rows, r)
		}
	}
	return rows
}

func (m *model) promptKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y":
		m.prompt = nil
		cmds := []tea.Cmd{m.zoom(false)}
		for _, r := range m.staleRows() {
			if m.resuming[r.SessionID] {
				continue
			}
			m.resuming[r.SessionID] = true
			m.batch[r.SessionID] = true
			delete(m.ending, r.SessionID)
			cmds = append(cmds, m.resume(r))
		}
		return tea.Sequence(cmds...)
	case "n", "esc":
		m.prompt = nil
		return tea.Sequence(m.zoom(false), m.showSelected())
	}
	return nil
}

func (m *model) syncCwds() bool {
	changed := false
	for _, l := range m.in.Live {
		i := slices.IndexFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == l.SessionID })
		if i >= 0 && l.Cwd != "" && m.state.Active[i].Cwd != l.Cwd {
			m.state.Active[i].Cwd = l.Cwd
			changed = true
		}
	}
	return changed
}

func (m *model) dropStale(r deck.Row) tea.Cmd {
	m.state.Active = slices.DeleteFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == r.SessionID })
	m.rebuild()
	return tea.Batch(m.saveState(), m.showSelected())
}

func (m *model) refresh() tea.Cmd {
	now := time.Now()
	ids := make([]string, 0, len(m.state.Active))
	for _, e := range m.state.Active {
		ids = append(ids, e.SessionID)
	}
	files, folders, agents := m.in.Files, m.folders, m.in.Agents
	list := files == nil || now.Sub(m.listedAt) >= listEvery
	if list {
		m.listedAt = now
	}
	save := now.Sub(m.savedAt) >= saveEvery
	if save {
		m.savedAt = now
	}
	t, reader, home, claudeHome, starts := m.ops.T, m.reader, m.home, m.claudeHome, m.starts
	return func() tea.Msg {
		panes, err := t.Panes()
		if err != nil {
			return refreshMsg{err: err}
		}
		msg := refreshMsg{panes: panes, files: files, folders: folders, events: map[string]events.State{}, summaries: map[string]transcript.Summary{}}
		msg.geo, msg.geoOK = sampleGeometry(t)
		msg.live, _ = live.Running(claudeHome)
		for _, job := range live.ParkedJobs(msg.live) {
			ids = append(ids, job)
		}
		msg.starts = map[int]time.Time{}
		keys := map[paneProc]bool{}
		for _, p := range panes {
			if !p.Dead && p.SessionID != "" && p.PID > 0 {
				k := paneProc{p.ID, p.PID}
				keys[k] = true
				msg.starts[p.PID] = starts.get(k)
			}
		}
		starts.keep(keys)
		for _, id := range ids {
			if ev, err := events.Read(filepath.Join(home, "events"), id); err == nil {
				msg.events[id] = ev
			}
		}
		if list {
			if listed, err := transcript.List(claudeHome); err == nil {
				msg.files = listed
			}
		}
		for _, f := range msg.files {
			if s, err := reader.Summary(f.Path); err == nil {
				msg.summaries[f.SessionID] = s
			}
		}
		if list {
			msg.folders = existingFolders(msg.summaries)
		}
		msg.agents = agents
		if list {
			msg.agents = runningAgents(ids, msg.events, msg.live, msg.files)
		}
		msg.accounts = accounts.List(home, claudeHome)
		msg.reviews = review.LoadAll(home)
		msg.requests = ctl.Pending(home)
		if save {
			reader.Save()
		}
		return msg
	}
}

func runningAgents(ids []string, evs map[string]events.State, sessions []live.Session, files []transcript.File) map[string][]transcript.Agent {
	paths := make(map[string]string, len(files))
	for _, f := range files {
		paths[f.SessionID] = f.Path
	}
	agents := map[string][]transcript.Agent{}
	for _, id := range ids {
		if running := evs[id].Agents; len(running) > 0 {
			agents[id] = transcript.AgentDescriptions(paths[id], running)
		}
	}
	since := time.Now().Add(-agentWindow)
	for _, l := range sessions {
		if slices.Contains(ids, l.SessionID) {
			continue
		}
		if running := transcript.RecentAgents(paths[l.SessionID], since); len(running) > 0 {
			agents[l.SessionID] = running
		}
	}
	return agents
}

func (m *model) followClear(msg refreshMsg) tea.Cmd {
	var cmds []tea.Cmd
	present := map[string]bool{}
	for _, p := range msg.panes {
		present[p.ID] = true
	}
	for id := range m.followed {
		if !present[id] {
			delete(m.followed, id)
		}
	}
	for i, p := range msg.panes {
		if want, ok := m.followed[p.ID]; ok {
			if p.SessionID == want || p.Dead {
				delete(m.followed, p.ID)
			} else {
				msg.panes[i].SessionID = want
			}
			continue
		}
		if p.Dead || p.SessionID == "" || p.PID <= 0 {
			continue
		}
		var next live.Session
		for _, l := range msg.live {
			if l.PID == p.PID && !m.ending[l.SessionID] && l.StartedAt >= next.StartedAt {
				next = l
			}
		}
		k := slices.IndexFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == p.SessionID })
		if next.SessionID == "" || next.SessionID == p.SessionID || k < 0 || !startedAfter(next.StartedAt, msg.starts[p.PID]) {
			continue
		}
		if slices.ContainsFunc(m.state.Active, func(e store.ActiveEntry) bool { return e.SessionID == next.SessionID }) {
			continue
		}
		old := p.SessionID
		entry := &m.state.Active[k]
		if m.projectOf(old) {
			if n := m.sessionName(old); n != "" {
				if m.state.Names == nil {
					m.state.Names = map[string]string{}
				}
				m.state.Names[next.SessionID] = n
			}
		}
		entry.SessionID, entry.Name = next.SessionID, ""
		m.remapSubs(old, next.SessionID)
		if m.isAssistant(old) {
			m.setAssistantID(next.SessionID)
			m.reprompt = time.Now()
			if n, ok := m.state.Names[old]; ok {
				m.state.Names[next.SessionID] = n
			}
		}
		if next.Cwd != "" {
			entry.Cwd = next.Cwd
		}
		if m.selected == old {
			m.selected = next.SessionID
		}
		if m.prevSelected == old {
			m.prevSelected = next.SessionID
		}
		if m.followed == nil {
			m.followed = map[string]string{}
		}
		m.followed[p.ID] = next.SessionID
		m.markEnding(old)
		msg.panes[i].SessionID = next.SessionID
		delete(msg.events, old)
		if m.arts != nil && m.arts.id == old {
			m.arts = nil
		}
		if m.rename != nil && m.rename.pane == p.ID {
			m.rename = nil
		}
		if m.restart != nil && m.restart.row.PaneID == p.ID {
			m.restart = nil
		}
		if m.handoff != nil && m.handoff.row.PaneID == p.ID {
			m.handoff = nil
		}
		if !m.dialogOpen() {
			cmds = append(cmds, m.zoom(false))
		}
		eventsDir := filepath.Join(m.home, "events")
		cmds = append(cmds, m.tmux("set-option", "-p", "-t", p.ID, "@deck-session", next.SessionID), m.saveState(), m.saveTasks(), func() tea.Msg {
			events.Remove(eventsDir, old)
			return nil
		})
	}
	return tea.Batch(cmds...)
}

func startedAfter(startedAt int64, procStart time.Time) bool {
	if procStart.IsZero() {
		return false
	}
	if startedAt < 1e11 {
		startedAt *= 1000
	}
	return startedAt >= procStart.UnixMilli()-2000
}

func (m *model) apply(msg refreshMsg) tea.Cmd {
	followed := m.followClear(msg)
	m.in.Panes = map[string]string{}
	m.paneAccount = map[string]string{}
	slotPane, slotReview := "", ""
	loginDead, loginGone := false, m.loginPane != ""
	for _, p := range msg.panes {
		m.paneAccount[p.ID] = p.Account
		if p.ID == m.loginPane {
			loginDead, loginGone = p.Dead, false
		}
		if p.Role == "preview" {
			m.previewPane = p.ID
		}
		if p.Target == strings.TrimPrefix(tmux.Slot, "=") {
			slotPane = p.ID
			if p.Role == tmux.ReviewRole {
				slotReview = p.Review
			}
		}
		if !p.Dead && p.SessionID != "" && !m.resuming[p.SessionID] {
			m.in.Panes[p.SessionID] = p.ID
		}
	}
	if slotReview != "" {
		slotPane = m.in.Panes[slotReview]
	}
	liveIDs := map[string]bool{}
	for _, l := range msg.live {
		liveIDs[l.SessionID] = true
	}
	for id := range m.ending {
		if !liveIDs[id] {
			delete(m.ending, id)
		}
	}
	m.in.Live = m.withoutEnding(msg.live)
	m.in.Events = msg.events
	m.in.Files = m.withoutGone(msg.files)
	m.in.Summaries = msg.summaries
	m.in.Agents = msg.agents
	m.folders = msg.folders
	m.accountList = msg.accounts

	cmds := m.syncReviews(msg.panes, msg.reviews)
	cmds = append(cmds, followed, m.endDead(msg.panes, slotPane), m.endLogin(loginDead, loginGone))
	if m.syncCwds() {
		cmds = append(cmds, m.saveState())
	}
	if m.rebuild() && m.loaded {
		cmds = append(cmds, m.showSelected())
	}
	if !m.loaded {
		m.loaded = true
		m.slot = slotPane
		i := slices.IndexFunc(m.active, func(r deck.Row) bool { return r.PaneID != "" && r.PaneID == slotPane })
		if i >= 0 {
			m.cursor, m.selected = i, m.active[i].SessionID
		}
		m.prevSelected = m.selected
		cmds = append(cmds, m.indexText(""))
		if n := len(m.staleRows()); n > 0 {
			m.prompt = &resumePrompt{count: n}
		} else if i < 0 {
			cmds = append(cmds, m.showSelected())
		}
	}
	if m.dialogOpen() && m.rename == nil && m.arts == nil && m.prPick == nil {
		cmds = append(cmds, m.zoom(true))
	}
	if r, ok := m.current(); m.agent != "" && (!ok || m.agentIndex(r) < 0) {
		m.agent = ""
		cmds = append(cmds, m.showSelected())
	}
	cmds = append(cmds, m.fetchPRs(), m.advanceHandoffs(time.Now()), m.advanceLimits(time.Now()), m.advanceSwitches())
	return tea.Batch(cmds...)
}

func (m *model) rebuild() (moved bool) {
	before := m.selected
	m.in.State = m.state
	if !m.ops.Cfg.Assistant {
		m.in.State.Assistant = store.Assistant{}
	}
	m.active, m.suspended = deck.Build(m.in)
	m.labelReviews()
	if d := m.rename; d != nil {
		keepOrder(m.active, d.order)
		keepOrder(m.suspended, d.order)
	}
	m.syncCursor()
	if d := m.rename; d != nil {
		i := slices.IndexFunc(m.shown(), func(r deck.Row) bool { return r.SessionID == d.id })
		if i < 0 {
			m.rename = nil
		} else if r := m.shown()[i]; r.Kind != d.kind || r.PaneID != d.pane {
			m.rename = nil
		} else {
			d.busy = r.Busy
		}
	}
	if a := m.arts; a != nil && !slices.ContainsFunc(m.shown(), func(r deck.Row) bool { return r.SessionID == a.id }) {
		m.arts = nil
	}
	if p := m.prPick; p != nil && !slices.ContainsFunc(m.shown(), func(r deck.Row) bool { return r.SessionID == p.id }) {
		m.prPick = nil
	}
	return m.selected != before
}

func keepOrder(rows []deck.Row, order []string) {
	pos := func(r deck.Row) int {
		if i := slices.Index(order, r.SessionID); i >= 0 {
			return i
		}
		return len(order)
	}
	sort.SliceStable(rows, func(i, j int) bool { return pos(rows[i]) < pos(rows[j]) })
}

func (m *model) syncCursor() {
	rows := m.shown()
	if i := slices.IndexFunc(rows, func(r deck.Row) bool { return r.SessionID == m.selected }); i >= 0 {
		m.cursor = i
		return
	}
	m.cursor = min(m.cursor, len(rows)-1)
	m.cursor = max(m.cursor, 0)
	m.selected = ""
	if len(rows) > 0 {
		m.selected = rows[m.cursor].SessionID
	}
}

func (m *model) current() (deck.Row, bool) {
	rows := m.shown()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return deck.Row{}, false
	}
	return rows[m.cursor], true
}

func previewTarget(r deck.Row) string {
	return r.TranscriptPath + "\t" + r.Name
}

func (m *model) showSelected() tea.Cmd {
	if m.loginPane != "" {
		return nil
	}
	r, ok := m.current()
	if k := m.agentIndex(r); ok && k >= 0 {
		return m.show(m.previewPane, agentTarget(r, visibleAgents(r)[k]), true)
	}
	switch {
	case !ok:
		return m.show(m.previewPane, "", true)
	case r.Kind == deck.Live:
		return m.show(r.PaneID, "", false)
	default:
		return m.show(m.previewPane, previewTarget(r), true)
	}
}

func (m *model) show(pane, target string, preview bool) tea.Cmd {
	m.slot = pane
	w := m.want
	w.mu.Lock()
	w.pane, w.target, w.preview, w.review = pane, target, preview, m.reviewOfPane(pane)
	w.mu.Unlock()
	ops, file := m.ops, filepath.Join(m.home, "preview-target")
	return func() tea.Msg {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.preview {
			if err := os.WriteFile(file, []byte(w.target), 0o644); err != nil {
				return errMsg{err}
			}
		}
		if w.pane == "" {
			return nil
		}
		var err error
		if w.review != "" {
			exe, xerr := os.Executable()
			if xerr != nil {
				return errMsg{xerr}
			}
			err = ops.ShowReview(exe, w.review, w.pane)
		} else {
			err = ops.Show(w.pane)
		}
		if err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *model) focus(target string) tea.Cmd {
	ops := m.ops
	return func() tea.Msg {
		if err := ops.Focus(target); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *model) zoom(on bool) tea.Cmd {
	t := m.ops.T
	return func() tea.Msg {
		flag, err := t.Run("display-message", "-p", "-t", tmux.ListPane, "#{window_zoomed_flag}")
		if err != nil {
			return errMsg{err}
		}
		if (strings.TrimSpace(flag) == "1") == on {
			return nil
		}
		if _, err := t.Run("resize-pane", "-Z", "-t", tmux.ListPane); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *model) tmux(args ...string) tea.Cmd {
	t := m.ops.T
	return func() tea.Msg {
		if _, err := t.Run(args...); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *model) saveState() tea.Cmd {
	sv, home := m.saver, m.home
	sv.mu.Lock()
	st := m.state
	st.Names, st.Accounts, st.Limits, st.Fallbacks = maps.Clone(st.Names), maps.Clone(st.Accounts), maps.Clone(st.Limits), maps.Clone(st.Fallbacks)
	st.Models, st.Roles = maps.Clone(st.Models), maps.Clone(st.Roles)
	st.Active, st.Subs, st.Cleanups = slices.Clone(st.Active), slices.Clone(st.Subs), slices.Clone(st.Cleanups)
	st.Projects = slices.Clone(st.Projects)
	st.Queues = maps.Clone(st.Queues)
	for name, q := range st.Queues {
		st.Queues[name] = slices.Clone(q)
	}
	sv.state = st
	sv.mu.Unlock()
	return func() tea.Msg {
		sv.mu.Lock()
		defer sv.mu.Unlock()
		if err := sv.state.Save(home); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *model) saveTasks() tea.Cmd {
	sv, home := m.taskSaver, m.home
	sv.mu.Lock()
	sv.tasks = make([]tasks.Task, len(m.tasks))
	for i, t := range m.tasks {
		t.History = slices.Clone(t.History)
		sv.tasks[i] = t
	}
	sv.mu.Unlock()
	return func() tea.Msg {
		sv.mu.Lock()
		defer sv.mu.Unlock()
		if err := tasks.Save(home, sv.tasks); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

func (m *model) start(id, cwd, name, account, prompt string) tea.Cmd {
	return m.launch(deck.Launch{Cwd: cwd, SessionID: id, Name: name, Account: account, Prompt: prompt})
}

func (m *model) launch(l deck.Launch) tea.Cmd {
	if l.Account != m.state.Account {
		if m.state.Accounts == nil {
			m.state.Accounts = map[string]string{}
		}
		m.state.Accounts[l.SessionID] = l.Account
	}
	l.Account = m.launchAccount(l.SessionID, l.Account)
	start := m.starter(m.ops, l.SessionID)
	return func() tea.Msg {
		pane, warn, err := start(l)
		return startedMsg{id: l.SessionID, cwd: l.Cwd, name: l.Name, pane: pane, err: err, warn: warn}
	}
}

func (m *model) starter(ops deck.Ops, id string) func(deck.Launch) (pane string, warn, err error) {
	home, role, model := m.home, m.state.Roles[id], m.modelFor(id)
	return func(l deck.Launch) (string, error, error) {
		l.Model = model
		var warn error
		if role != "" {
			if r, err := roles.Find(home, ctl.TopLevel(l.Cwd), role); err == nil {
				l.System = r.Body
			} else {
				warn = fmt.Errorf("role %s not found; started without it", role)
			}
		}
		pane, err := ops.Start(l)
		return pane, warn, err
	}
}

func (m *model) resume(r deck.Row) tea.Cmd {
	start, account := m.starter(m.ops, r.SessionID), m.launchAccount(r.SessionID, m.state.AccountFor(r.SessionID))
	name, home := m.state.Names[r.SessionID], m.home
	sub, isSub := m.state.SubOf(r.SessionID)
	queued := slices.Contains(m.state.Cleanups, r.SessionID)
	prompt := m.firstPrompt(r.SessionID)
	return func() tea.Msg {
		if isSub && queued {
			return startedMsg{id: r.SessionID, err: fmt.Errorf("answer the worktree question for %s first", sub.Name)}
		}
		if _, err := os.Stat(sub.Dir); isSub && err != nil {
			return startedMsg{id: r.SessionID, err: fmt.Errorf("the worktree %s was removed", sub.Dir)}
		}
		var rev *review.State
		if st, ok, _ := review.Load(home, r.SessionID); ok {
			rev = &st
		}
		pane, warn, err := start(deck.Launch{Cwd: r.Cwd, SessionID: r.SessionID, Name: name, Account: account, Prompt: prompt, Resume: true})
		return startedMsg{id: r.SessionID, cwd: r.Cwd, name: r.Name, pane: pane, err: err, warn: warn, rev: rev}
	}
}

func hasGH() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

func (m *model) fetchPRs() tea.Cmd {
	if m.noGH {
		return nil
	}
	now := time.Now()
	var cmds []tea.Cmd
	for _, r := range m.visibleRows() {
		for _, pr := range r.Summary.PRs {
			e := m.prs[pr.URL]
			if pr.URL == "" || (e != nil && (e.pending || now.Before(e.until))) {
				continue
			}
			if e == nil {
				e = &prEntry{}
				m.prs[pr.URL] = e
			}
			e.pending = true
			url, sem := pr.URL, m.prSem
			cmds = append(cmds, func() tea.Msg {
				sem <- struct{}{}
				defer func() { <-sem }()
				state, checks, err := gh.State(url)
				return prStateMsg{url: url, state: state, checks: checks, err: err}
			})
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) prState(url string) prStatus {
	if e := m.prs[url]; e != nil && e.state != "" {
		return prStatus{e.state, e.checks}
	}
	if m.noGH {
		return prStatus{state: "unknown"}
	}
	return prStatus{}
}
