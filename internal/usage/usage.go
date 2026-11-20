package usage

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"
)

const Env = "DECKHAND_ACCOUNT"

type Window struct {
	Used   float64 `json:"used_percentage"`
	Resets int64   `json:"resets_at"`
}

type Usage struct {
	Five *Window   `json:"five_hour,omitempty"`
	Week *Window   `json:"seven_day,omitempty"`
	Seen time.Time `json:"seen"`
}

func (w *Window) used(now time.Time) float64 {
	if w == nil || !time.Unix(w.Resets, 0).After(now) {
		return 0
	}
	return w.Used
}

func (u Usage) FiveUsed(now time.Time) float64 { return u.Five.used(now) }
func (u Usage) WeekUsed(now time.Time) float64 { return u.Week.used(now) }

func (u Usage) LimitedUntil(now time.Time) (time.Time, bool) {
	var until time.Time
	for _, w := range []*Window{u.Five, u.Week} {
		if w.used(now) >= 100 && time.Unix(w.Resets, 0).After(until) {
			until = time.Unix(w.Resets, 0)
		}
	}
	return until, !until.IsZero()
}

func file(dir, account string) string {
	return filepath.Join(dir, account+".json")
}

func Load(dir, account string) (Usage, bool) {
	data, err := os.ReadFile(file(dir, account))
	if err != nil {
		return Usage{}, false
	}
	var u Usage
	if json.Unmarshal(data, &u) != nil || u.Five == nil && u.Week == nil {
		return Usage{}, false
	}
	return u, true
}

func save(dir, account string, u Usage) error {
	data, err := json.Marshal(u)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".usage-*")
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
	return os.Rename(tmp.Name(), file(dir, account))
}

func StatusLine(dir, account, settings string, stdin io.Reader, stdout io.Writer, now time.Time) error {
	in, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	var status struct {
		Limits *Usage `json:"rate_limits"`
	}
	if account != "" && filepath.Base(account) == account && json.Unmarshal(in, &status) == nil && status.Limits != nil && (status.Limits.Five != nil || status.Limits.Week != nil) {
		status.Limits.Seen = now
		if err := save(dir, account, *status.Limits); err != nil {
			return err
		}
	}
	command := userCommand(settings)
	if command == "" {
		return nil
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(in), stdout, os.Stderr
	return cmd.Run()
}

type statusLine struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Padding *int   `json:"padding,omitempty"`
}

func readStatusLine(settings string) statusLine {
	var s struct {
		StatusLine statusLine `json:"statusLine"`
	}
	if data, err := os.ReadFile(settings); err == nil {
		json.Unmarshal(data, &s)
	}
	return s.StatusLine
}

func userCommand(settings string) string {
	if s := readStatusLine(settings); s.Type == "command" {
		return s.Command
	}
	return ""
}

func Setting(command, settings string) any {
	return statusLine{Type: "command", Command: command, Padding: readStatusLine(settings).Padding}
}

func Pick(names []string, use map[string]Usage, limited func(string) bool, now time.Time) (string, bool) {
	var free []string
	for _, n := range names {
		if !limited(n) && use[n].FiveUsed(now) < 95 && !slices.Contains(free, n) {
			free = append(free, n)
		}
	}
	slices.SortStableFunc(free, func(a, b string) int {
		return cmp.Or(cmp.Compare(use[a].FiveUsed(now), use[b].FiveUsed(now)), cmp.Compare(use[a].WeekUsed(now), use[b].WeekUsed(now)))
	})
	if len(free) == 0 {
		return "", false
	}
	return free[0], true
}
