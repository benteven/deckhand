package ui

import (
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
	"uuid"

	tea "charm.land/bubbletea/v2"

	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/deck"
	"github.com/benteven/deckhand/internal/events"
	"github.com/benteven/deckhand/internal/paths"
	"github.com/benteven/deckhand/internal/transcript"
)

const (
	dayFormat       = "2006-01-02"
	assistantRetry  = 60 * time.Second
	handoffContext  = 60
	maxNotes        = 50
	repromptWait    = 2 * time.Second
	assistantPrompt = "deckhand started you again. Read instructions.md and your notes, and create your hourly round again with CronCreate."
)

type updatedMsg struct{ err error }

type pausedMsg struct {
	id  string
	err error
}

func nightlyDue(last string, now time.Time) bool {
	return now.Hour() >= 4 && last != now.Format(dayFormat)
}

func (m *model) isAssistant(id string) bool {
	return m.ops.AssistantID != "" && id == m.ops.AssistantID
}

func (m *model) setAssistantID(id string) {
	m.state.Assistant.ID = id
	if m.ops.Cfg.Assistant {
		m.ops.AssistantID = id
	}
}

func (m *model) firstPrompt(id string) string {
	if m.isAssistant(id) {
		return assistantPrompt
	}
	return ""
}

func (m *model) keepAssistant(now time.Time) tea.Cmd {
	if !m.ops.Cfg.Assistant || m.state.Assistant.Paused {
		return nil
	}
	id := m.state.Assistant.ID
	if id == "" {
		id = uuid.NewV4().String()
		m.setAssistantID(id)
		if m.state.Names == nil {
			m.state.Names = map[string]string{}
		}
		m.state.Names[id] = ctl.AssistantName
		if m.state.Assistant.UpdatedOn == "" {
			m.state.Assistant.UpdatedOn = now.Format(dayFormat)
		}
		m.assistantTry = now
		m.resuming[id] = true
		return tea.Batch(m.saveState(), inBackground(m.start(id, m.ops.AssistantDir, ctl.AssistantName, m.state.Account, assistantPrompt)))
	}
	r, ok := m.rowByID(id)
	if !ok || (r.Kind != deck.Live && r.Kind != deck.Elsewhere) {
		if m.resuming[id] || now.Sub(m.assistantTry) < assistantRetry {
			return nil
		}
		m.assistantTry = now
		m.resuming[id] = true
		delete(m.ending, id)
		if !slices.ContainsFunc(m.in.Files, func(f transcript.File) bool { return f.SessionID == id }) {
			return inBackground(m.start(id, m.ops.AssistantDir, ctl.AssistantName, m.state.AccountFor(id), assistantPrompt))
		}
		return inBackground(m.resume(deck.Row{SessionID: id, Cwd: m.ops.AssistantDir, Name: ctl.AssistantName}))
	}
	if nightlyDue(m.state.Assistant.UpdatedOn, now) && !m.updating {
		m.state.Assistant.UpdatedOn = now.Format(dayFormat)
		m.updating = true
		return tea.Batch(m.saveState(), func() tea.Msg {
			out, err := exec.Command(paths.ClaudeCommand(), "update").CombinedOutput()
			if err != nil {
				return updatedMsg{fmt.Errorf("claude update: %v: %s", err, strings.TrimSpace(string(out)))}
			}
			return updatedMsg{}
		})
	}
	if !m.restartDue || !m.assistantFree(r) || waitsForIdle(r) {
		return nil
	}
	m.restartDue = false
	if r.Summary.ContextPercent() < handoffContext {
		return m.restartRow(r)
	}
	prompt, err := m.beginHandoff(r)
	if err != nil {
		m.err = err
		return nil
	}
	m.handoffs[r.PaneID].fresh = true
	return m.typeIn(r.PaneID, prompt)
}

func inBackground(cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		msg, _ := cmd().(startedMsg)
		msg.background = true
		return msg
	}
}

func (m *model) updated(msg updatedMsg) {
	m.updating = false
	m.restartDue = true
	if msg.err != nil {
		m.err = msg.err
	}
}

func (m *model) freshAssistant(r deck.Row, file string) tea.Cmd {
	prompt := fmt.Sprintf("%s Then read %s. It is your handoff note from before the restart. Continue from it.", assistantPrompt, file)
	return m.relaunch(r, uuid.NewV4().String(), prompt)
}

func (m *model) pauseAssistant(r deck.Row) tea.Cmd {
	m.state.Assistant.Paused = true
	m.restartDue = false
	m.resuming[r.SessionID] = true
	m.markEnding(r.SessionID)
	delete(m.in.Panes, r.SessionID)
	m.rebuild()
	ops, pane, busy, id := m.ops, r.PaneID, r.Busy, r.SessionID
	return tea.Batch(m.saveState(), tea.Sequence(m.showSelected(), func() tea.Msg {
		return pausedMsg{id: id, err: ops.Stop(pane, busy)}
	}))
}

func (m *model) paused(msg pausedMsg) tea.Cmd {
	delete(m.resuming, msg.id)
	if msg.err != nil {
		m.err = msg.err
	}
	m.rebuild()
	return m.showSelected()
}

func (m *model) assistantDied(pane string) tea.Cmd {
	t := m.ops.T
	return func() tea.Msg {
		out, _ := t.Run("capture-pane", "-p", "-J", "-S", "-100", "-t", pane)
		t.Run("kill-pane", "-t", pane)
		lines := strings.Split(out, "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if line := strings.TrimSpace(lines[i]); line != "" && !strings.HasPrefix(line, "Pane is dead") {
				return errMsg{fmt.Errorf("the assistant stopped: %s", line)}
			}
		}
		return errMsg{fmt.Errorf("the assistant stopped")}
	}
}

func waits(ev events.State) bool {
	return ev.Event == "Notification" || (ev.Event == "Stop" && !ev.PromptAt.IsZero())
}

func (m *model) waitNotes(prev, next map[string]events.State) tea.Cmd {
	if !m.ops.Cfg.Assistant {
		return nil
	}
	for id, ev := range next {
		before, known := prev[id]
		if !known || !waits(ev) || waits(before) || m.isAssistant(id) || m.ending[id] {
			continue
		}
		r, ok := m.rowByID(id)
		if !ok || r.Kind != deck.Live || (m.slot == r.PaneID && !m.focused) {
			continue
		}
		text := ev.Message
		if text == "" {
			text = "its turn ended"
		}
		m.notes = append(m.notes, fmt.Sprintf("[deckhand] %s (%s) waits for the user: %s", r.Name, id, text))
	}
	if over := len(m.notes) - maxNotes; over > 0 {
		m.notes = m.notes[over:]
	}
	return m.deliverNotes()
}

func (m *model) assistantFree(r deck.Row) bool {
	id := r.SessionID
	return m.isAssistant(id) && r.Kind == deck.Live && !m.resuming[id] && m.handoffs[r.PaneID] == nil && m.userAway(r)
}

func (m *model) userAway(r deck.Row) bool {
	return !(m.slot == r.PaneID && !m.focused) && !m.in.Events[r.SessionID].Menu()
}

func (m *model) deliverNotes() tea.Cmd {
	reprompt := !m.reprompt.IsZero() && time.Since(m.reprompt) >= repromptWait
	r, ok := m.rowByID(m.state.Assistant.ID)
	if (len(m.notes) == 0 && !reprompt) || !ok || !m.assistantFree(r) {
		return nil
	}
	notes := m.notes
	if reprompt {
		notes = append([]string{assistantPrompt}, notes...)
		m.reprompt = time.Time{}
	}
	m.notes = nil
	return m.typeIn(r.PaneID, strings.Join(notes, " "))
}
