package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Prefix         string   `json:"prefix"`
	ClaudeArgs     []string `json:"claude_args"`
	ListWidth      int      `json:"list_width"`
	Assistant      bool     `json:"assistant"`
	AssistantModel string   `json:"assistant_model"`
	WebPort        int      `json:"web_port"`
}

const (
	DefaultListWidth      = 36
	DefaultAssistantModel = "sonnet"
	DefaultWebPort        = 7317
	MinListWidth          = 24
	MaxListWidth          = 100
)

func ClampListWidth(w int) int {
	return min(max(w, MinListWidth), MaxListWidth)
}

func SaveListWidth(dir string, w int) error {
	return saveConfigKey(dir, "list_width", json.RawMessage(strconv.Itoa(w)))
}

func SaveAssistant(dir string, on bool) error {
	return saveConfigKey(dir, "assistant", json.RawMessage(strconv.FormatBool(on)))
}

func saveConfigKey(dir, key string, value json.RawMessage) error {
	path := filepath.Join(dir, "config.json")
	raw := map[string]json.RawMessage{}
	if err := ReadJSON(path, &raw); err != nil {
		return err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	raw[key] = value
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type ActiveEntry struct {
	SessionID       string    `json:"session_id"`
	Cwd             string    `json:"cwd"`
	Name            string    `json:"name"`
	LastInteraction time.Time `json:"last_interaction"`
	Num             int       `json:"num"`
}

type Assistant struct {
	ID        string `json:"id"`
	Paused    bool   `json:"paused"`
	UpdatedOn string `json:"updated_on"`
}

type Subsession struct {
	ID     string `json:"id"`
	Parent string `json:"parent"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

type State struct {
	Names     map[string]string    `json:"names"`
	Active    []ActiveEntry        `json:"active"`
	Account   string               `json:"account,omitempty"`
	Accounts  map[string]string    `json:"accounts,omitempty"`
	Limits    map[string]time.Time `json:"limits,omitempty"`
	Fallbacks map[string]string    `json:"fallbacks,omitempty"`
	Models    map[string]string    `json:"models,omitempty"`
	Roles     map[string]string    `json:"roles,omitempty"`
	Projects  []string             `json:"projects,omitempty"`
	Assistant Assistant            `json:"assistant"`
	Subs      []Subsession         `json:"subs,omitempty"`
	Cleanups  []string             `json:"cleanups,omitempty"`
	Queues    map[string][]string  `json:"queues,omitempty"`
}

func (s State) SubOf(id string) (Subsession, bool) {
	for _, sub := range s.Subs {
		if sub.ID == id {
			return sub, true
		}
	}
	return Subsession{}, false
}

func (s State) AccountFor(sessionID string) string {
	if a, ok := s.Accounts[sessionID]; ok {
		return a
	}
	return s.Account
}

func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func LoadConfig(dir string) (Config, error) {
	c := Config{Prefix: "C-b", ListWidth: DefaultListWidth, AssistantModel: DefaultAssistantModel, WebPort: DefaultWebPort}
	if err := ReadJSON(filepath.Join(dir, "config.json"), &c); err != nil {
		return Config{Prefix: "C-b", ListWidth: DefaultListWidth, AssistantModel: DefaultAssistantModel, WebPort: DefaultWebPort}, err
	}
	if c.WebPort <= 0 || c.WebPort > 65535 {
		c.WebPort = DefaultWebPort
	}
	if c.AssistantModel == "" {
		c.AssistantModel = DefaultAssistantModel
	}
	if c.Prefix == "" || strings.EqualFold(c.Prefix, "C-j") || strings.EqualFold(c.Prefix, "C-k") {
		c.Prefix = "C-b"
	}
	if c.ListWidth == 0 {
		c.ListWidth = DefaultListWidth
	}
	c.ListWidth = ClampListWidth(c.ListWidth)
	return c, nil
}

func Load(dir string) (State, error) {
	var s State
	if err := ReadJSON(filepath.Join(dir, "state.json"), &s); err != nil {
		return State{}, err
	}
	if s.Names == nil {
		s.Names = map[string]string{}
	}
	for i := range s.Active {
		if s.Active[i].Num == 0 {
			s.Active[i].Num = Claim(s.Active, 0)
		}
	}
	return s, nil
}

func Claim(active []ActiveEntry, want int) int {
	taken := func(n int) bool {
		return slices.ContainsFunc(active, func(e ActiveEntry) bool { return e.Num == n })
	}
	if want > 0 && !taken(want) {
		return want
	}
	for n := 1; n <= 9; n++ {
		if !taken(n) {
			return n
		}
	}
	return 0
}

func (s State) Save(dir string) error {
	return WriteJSON(dir, "state.json", s)
}

type PRState struct {
	State   string    `json:"state"`
	Checks  string    `json:"checks,omitempty"`
	Checked time.Time `json:"checked"`
}

func LoadPRs(dir string) (map[string]PRState, error) {
	prs := map[string]PRState{}
	err := ReadJSON(filepath.Join(dir, "prs.json"), &prs)
	return prs, err
}

func SavePRs(dir string, prs map[string]PRState) error {
	return WriteJSON(dir, "prs.json", prs)
}

func WriteJSON(dir, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+"-*")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}
