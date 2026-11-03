package review

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Repo   string `json:"repo"`
	Title  string `json:"title"`
	Base   string `json:"base"`
	Head   string `json:"head"`
}

type State struct {
	Source  string `json:"source"`
	Cwd     string `json:"cwd"`
	PRs     []PR   `json:"prs"`
	Current int    `json:"current"`
	Asked   []int  `json:"asked"`
}

func (s State) PR() (PR, bool) {
	if s.Current < 0 || s.Current >= len(s.PRs) {
		return PR{}, false
	}
	return s.PRs[s.Current], true
}

func Dir(home, id string) string {
	return filepath.Join(home, "reviews", id)
}

func NotesPath(home, id string, pr int) string {
	return filepath.Join(Dir(home, id), fmt.Sprintf("pr-%d.json", pr))
}

func Load(home, id string) (State, bool, error) {
	data, err := os.ReadFile(filepath.Join(Dir(home, id), "review.json"))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false, err
	}
	return s, true, nil
}

func (s State) Save(home, id string) error {
	dir := Dir(home, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".review-*")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, "review.json"))
}

func Update(home, id string, change func(*State) bool) (State, error) {
	dir := Dir(home, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return State{}, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "review.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return State{}, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return State{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	s, _, err := Load(home, id)
	if err != nil {
		return State{}, err
	}
	if !change(&s) {
		return s, nil
	}
	return s, s.Save(home, id)
}

func LoadAll(home string) map[string]State {
	out := map[string]State{}
	entries, _ := os.ReadDir(filepath.Join(home, "reviews"))
	for _, e := range entries {
		if st, ok, err := Load(home, e.Name()); err == nil && ok {
			out[e.Name()] = st
		}
	}
	return out
}

type Note struct {
	Path  string `json:"path"`
	Group string `json:"group"`
	Note  string `json:"note"`
}

func LoadNotes(path string) ([]Note, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v struct {
		Files []Note `json:"files"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v.Files, nil
}
