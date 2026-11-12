package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

type State struct {
	Event    string    `json:"event"`
	At       time.Time `json:"at"`
	PromptAt time.Time `json:"prompt_at"`
	Agents   []string  `json:"agents,omitempty"`
	Need     string    `json:"need,omitempty"`
	Message  string    `json:"message,omitempty"`
}

func (s State) Waiting() bool {
	return s.Event == "Notification" || s.Event == "Stop"
}

func (s State) Menu() bool {
	return s.Event == "Notification" && (s.Need == "permission_prompt" || s.Need == "elicitation_dialog")
}

func path(dir, sessionID string) (string, error) {
	if sessionID == "" || sessionID != filepath.Base(sessionID) || strings.HasPrefix(sessionID, ".") {
		return "", fmt.Errorf("invalid session id %q", sessionID)
	}
	return filepath.Join(dir, sessionID+".json"), nil
}

func Read(dir, sessionID string) (State, error) {
	p, err := path(dir, sessionID)
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

func Record(dir, sessionID, event string, now time.Time) error {
	return update(dir, sessionID, func(s *State) bool { return s.record(event, now) })
}

func (s *State) record(event string, now time.Time) bool {
	if now.Before(s.At) {
		if event != "UserPromptSubmit" || !now.After(s.PromptAt) {
			return false
		}
		s.PromptAt = now
		return true
	}
	s.Event = event
	s.At = now
	s.Need = ""
	s.Message = ""
	if event == "UserPromptSubmit" {
		s.PromptAt = now
	}
	return true
}

func update(dir, sessionID string, change func(*State) bool) error {
	p, err := path(dir, sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(strings.TrimSuffix(p, ".json")+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	s, err := Read(dir, sessionID)
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if err != nil {
		if !errors.As(err, &syntaxErr) && !errors.As(err, &typeErr) {
			return err
		}
		s = State{}
	}
	if !change(&s) {
		return nil
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".event-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

type hookInput struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Source    string `json:"source"`
	AgentID   string `json:"agent_id"`
	Message   string `json:"message"`
	NotifType string `json:"notification_type"`
	Tasks     *[]struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"background_tasks"`
}

func RunHook(dir string, stdin io.Reader, now time.Time) error {
	var in hookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return err
	}
	if in.SessionID == "" || in.Event == "" {
		return errors.New("hook input missing session_id or hook_event_name")
	}
	return update(dir, in.SessionID, func(s *State) bool { return s.hook(in, now) })
}

func (s *State) hook(in hookInput, now time.Time) bool {
	switch in.Event {
	case "SubagentStart":
		if in.AgentID == "" || slices.Contains(s.Agents, in.AgentID) {
			return false
		}
		s.Agents = append(s.Agents, in.AgentID)
		return true
	case "SubagentStop":
		before := len(s.Agents)
		s.Agents = slices.DeleteFunc(s.Agents, func(id string) bool { return id == in.AgentID })
		return len(s.Agents) != before
	case "SessionStart", "SessionEnd":
		if in.Source == "compact" || len(s.Agents) == 0 {
			return false
		}
		s.Agents = nil
		return true
	}
	if !s.record(in.Event, now) {
		return false
	}
	if in.Event == "Notification" {
		s.Need = in.NotifType
		s.Message = in.Message
	}
	if in.Event == "Stop" && in.Tasks != nil {
		s.Agents = nil
		for _, t := range *in.Tasks {
			if t.Type == "subagent" && t.Status == "running" && t.ID != "" {
				s.Agents = append(s.Agents, t.ID)
			}
		}
	}
	return true
}

func HookSettings(exe string) []byte {
	command := "'" + strings.ReplaceAll(exe, "'", `'\''`) + "' hook"
	entry := []map[string]any{{"hooks": []map[string]string{{"type": "command", "command": command}}}}
	hooks := map[string]any{}
	for _, e := range []string{"UserPromptSubmit", "PostToolUse", "Notification", "Stop", "SubagentStart", "SubagentStop", "SessionStart", "SessionEnd"} {
		hooks[e] = entry
	}
	data, _ := json.Marshal(map[string]any{"hooks": hooks})
	return data
}

func Remove(dir, sessionID string) error {
	p, err := path(dir, sessionID)
	if err != nil {
		return err
	}
	for _, f := range []string{p, strings.TrimSuffix(p, ".json") + ".lock"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
