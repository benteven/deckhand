package tasks

import (
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/benteven/deckhand/internal/store"
)

const historyLimit = 30

type Entry struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

type Task struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	From     string    `json:"from"`
	Context  string    `json:"context"`
	Project  string    `json:"project"`
	Session  string    `json:"session"`
	Repo     string    `json:"repo"`
	Branch   string    `json:"branch"`
	Dir      string    `json:"dir"`
	Prio     string    `json:"prio"`
	Deadline string    `json:"deadline"`
	Status   string    `json:"status"`
	Need     string    `json:"need"`
	WaitsOn  string    `json:"waits_on"`
	PR       string    `json:"pr"`
	Summary  string    `json:"summary"`
	History  []Entry   `json:"history"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`

	SummaryAsked time.Time `json:"summary_asked,omitzero"`
	CleanupAsked time.Time `json:"cleanup_asked,omitzero"`
}

func Load(home string) ([]Task, error) {
	var ts []Task
	if err := store.ReadJSON(filepath.Join(home, "tasks.json"), &ts); err != nil {
		return nil, err
	}
	return ts, nil
}

func Save(home string, ts []Task) error {
	if ts == nil {
		ts = []Task{}
	}
	return store.WriteJSON(home, "tasks.json", ts)
}

func (t Task) Open() bool {
	return t.Status != "done"
}

func Group(t Task) int {
	switch t.Status {
	case "needs-you":
		return 0
	case "todo":
		return 1
	case "waiting":
		return 3
	case "done":
		return 4
	}
	return 2
}

func prioRank(p string) int {
	switch p {
	case "urgent":
		return 0
	case "high":
		return 1
	case "low":
		return 3
	}
	return 2
}

func Sort(ts []Task) {
	sort.SliceStable(ts, func(i, j int) bool {
		a, b := ts[i], ts[j]
		if ga, gb := Group(a), Group(b); ga != gb {
			return ga < gb
		}
		if pa, pb := prioRank(a.Prio), prioRank(b.Prio); pa != pb {
			return pa < pb
		}
		if a.Deadline != b.Deadline {
			if a.Deadline == "" {
				return false
			}
			if b.Deadline == "" {
				return true
			}
			return a.Deadline < b.Deadline
		}
		return a.Created.Before(b.Created)
	})
}

func NewID(ts []Task) string {
	max := 0
	for _, t := range ts {
		if len(t.ID) > 1 && t.ID[0] == 't' {
			if n, err := strconv.Atoi(t.ID[1:]); err == nil && n > max {
				max = n
			}
		}
	}
	return "t" + strconv.Itoa(max+1)
}

func (t *Task) Log(now time.Time, text string) {
	t.History = append(t.History, Entry{At: now, Text: text})
	if len(t.History) > historyLimit {
		t.History = t.History[len(t.History)-historyLimit:]
	}
	t.Updated = now
}
