package deck

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/benteven/deckhand/internal/events"
	"github.com/benteven/deckhand/internal/live"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/textclean"
	"github.com/benteven/deckhand/internal/transcript"
)

type Kind int

const (
	Live Kind = iota
	Stale
	Elsewhere
	Suspended
)

type Row struct {
	SessionID       string
	Name            string
	Cwd             string
	PaneID          string
	PID             int
	Started         time.Time
	TranscriptPath  string
	Kind            Kind
	Summary         transcript.Summary
	Waiting         bool
	Busy            bool
	Asking          bool
	Job             string
	JobSession      string
	Remote          bool
	LastInteraction time.Time
	Agents          []transcript.Agent
	Num             int
	Assistant       bool
	Parent          string
	Orphan          string
}

type Inputs struct {
	State     store.State
	Panes     map[string]string
	Live      []live.Session
	Events    map[string]events.State
	Files     []transcript.File
	Summaries map[string]transcript.Summary
	Agents    map[string][]transcript.Agent
}

func Build(in Inputs) (active, suspended []Row) {
	liveByID := make(map[string]live.Session, len(in.Live))
	for _, s := range in.Live {
		liveByID[s.SessionID] = s
	}
	paths := make(map[string]string, len(in.Files))
	for _, f := range in.Files {
		paths[f.SessionID] = f.Path
	}
	seen := make(map[string]bool, len(in.State.Active))
	jobs := live.ParkedJobs(in.Live)

	for _, e := range in.State.Active {
		seen[e.SessionID] = true
		ls, isLive := liveByID[e.SessionID]
		src, parked := jobs[e.SessionID]
		job, jobSession := "", ""
		if parked {
			seen[src] = true
			ls.Status = liveByID[src].Status
			job, jobSession = ls.ParkedJob, src
		} else {
			src = e.SessionID
		}
		sum := in.Summaries[src]
		ev := in.Events[src]
		r := Row{
			SessionID:       e.SessionID,
			Name:            name(in.State, e.Name, ls, e.SessionID, sum),
			Cwd:             firstNonEmpty(ls.Cwd, e.Cwd, sum.Cwd),
			PaneID:          in.Panes[e.SessionID],
			Started:         startTime(ls.StartedAt),
			TranscriptPath:  paths[src],
			Kind:            Stale,
			Summary:         sum,
			Waiting:         ev.Waiting(),
			Busy:            isLive && ls.Status == "busy",
			Asking:          isLive && ls.Status == "waiting",
			Remote:          isLive && ls.BridgeID != "",
			LastInteraction: latest(e.LastInteraction, sum.PromptedAt),
			Agents:          cleanAgents(in.Agents[src]),
			Num:             e.Num,
			Job:             job,
			JobSession:      jobSession,
		}
		if r.PaneID != "" {
			r.Kind = Live
		}
		active = append(active, r)
	}

	for _, ls := range in.Live {
		if seen[ls.SessionID] || ls.Kind == "bg" {
			seen[ls.SessionID] = true
			continue
		}
		seen[ls.SessionID] = true
		sum := in.Summaries[ls.SessionID]
		active = append(active, Row{
			SessionID:       ls.SessionID,
			Name:            name(in.State, "", ls, ls.SessionID, sum),
			Cwd:             firstNonEmpty(ls.Cwd, sum.Cwd),
			PID:             ls.PID,
			Started:         startTime(ls.StartedAt),
			TranscriptPath:  paths[ls.SessionID],
			Kind:            Elsewhere,
			Summary:         sum,
			Waiting:         ls.Status == "idle",
			Busy:            ls.Status == "busy",
			Remote:          ls.BridgeID != "",
			LastInteraction: promptedAt(sum),
			Agents:          cleanAgents(in.Agents[ls.SessionID]),
		})
	}
	sort.SliceStable(active, func(i, j int) bool {
		if ei, ej := active[i].Kind == Elsewhere, active[j].Kind == Elsewhere; ei != ej {
			return ej
		}
		return active[i].LastInteraction.After(active[j].LastInteraction)
	})
	active = arrange(in.State, active)

	for _, f := range in.Files {
		if seen[f.SessionID] {
			continue
		}
		sum := in.Summaries[f.SessionID]
		last := sum.LastUsed
		if last.IsZero() {
			last = f.ModTime
		}
		suspended = append(suspended, Row{
			SessionID:       f.SessionID,
			Name:            name(in.State, "", live.Session{}, f.SessionID, sum),
			Cwd:             sum.Cwd,
			TranscriptPath:  f.Path,
			Kind:            Suspended,
			Summary:         sum,
			LastInteraction: last,
		})
	}
	sort.SliceStable(suspended, func(i, j int) bool {
		return suspended[i].LastInteraction.After(suspended[j].LastInteraction)
	})
	return active, suspended
}

func arrange(state store.State, rows []Row) []Row {
	isActive := make(map[string]bool, len(rows))
	for _, r := range rows {
		isActive[r.SessionID] = true
	}
	children := map[string][]int{}
	child := map[string]bool{}
	byID := make(map[string]int, len(rows))
	for i, r := range rows {
		byID[r.SessionID] = i
	}
	for _, sub := range state.Subs {
		i, ok := byID[sub.ID]
		if !ok {
			continue
		}
		if isActive[sub.Parent] && sub.Parent != sub.ID {
			rows[i].Parent = sub.Parent
			children[sub.Parent] = append(children[sub.Parent], i)
			child[sub.ID] = true
		} else {
			rows[i].Orphan = name(state, "", live.Session{}, sub.Parent, transcript.Summary{})
		}
	}
	out := make([]Row, 0, len(rows))
	emit := func(i int) {
		out = append(out, rows[i])
		for _, c := range children[rows[i].SessionID] {
			out = append(out, rows[c])
		}
	}
	if i, ok := byID[state.Assistant.ID]; ok && state.Assistant.ID != "" && !child[state.Assistant.ID] {
		rows[i].Assistant = true
		emit(i)
	}
	for i, r := range rows {
		if child[r.SessionID] || rows[i].Assistant {
			continue
		}
		emit(i)
	}
	return out
}

func Family(active []Row, id string) []Row {
	parent := id
	for _, r := range active {
		if r.SessionID == id && r.Parent != "" {
			parent = r.Parent
		}
	}
	var out []Row
	for _, r := range active {
		if r.SessionID == parent {
			out = append([]Row{r}, out...)
		} else if r.Parent == parent {
			out = append(out, r)
		}
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

func name(state store.State, entryName string, ls live.Session, id string, sum transcript.Summary) string {
	liveName := ""
	if ls.NameSource == "user" {
		liveName = ls.Name
	}
	var picked string
	for _, c := range []string{state.Names[id], liveName, sum.Title, entryName, sum.AITitle} {
		if clean(c) != "" {
			picked = c
			break
		}
	}
	if picked == "" {
		picked = cut(clean(sum.LastPrompt), 40)
	}
	if picked = clean(picked); picked != "" {
		return picked
	}
	return cut(id, 8)
}

func clean(s string) string {
	return strings.Join(strings.Fields(textclean.Clean(s)), " ")
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func startTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func promptedAt(sum transcript.Summary) time.Time {
	if sum.PromptedAt.IsZero() {
		return sum.LastUsed
	}
	return sum.PromptedAt
}

func latest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if t.After(out) {
			out = t
		}
	}
	return out
}

func cleanAgents(agents []transcript.Agent) []transcript.Agent {
	var out []transcript.Agent
	for _, a := range agents {
		if a.Label = clean(a.Label); a.Label != "" {
			out = append(out, a)
		}
	}
	return out
}

func Filter(rows []Row, query string) []Row {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return rows
	}
	var out []Row
	for _, r := range rows {
		hay := haystack(r)
		match := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				match = false
				break
			}
		}
		if match {
			out = append(out, r)
		}
	}
	return out
}

func haystack(r Row) string {
	parts := []string{r.Name, r.Summary.AITitle, r.Summary.LastPrompt, r.Cwd, r.Summary.Branch}
	for _, pr := range r.Summary.PRs {
		parts = append(parts, fmt.Sprintf("#%d", pr.Number))
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}
