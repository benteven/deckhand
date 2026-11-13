package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type PRLink struct {
	Number int
	URL    string
	Repo   string
}

type Summary struct {
	SessionID      string
	Cwd            string
	Branch         string
	Model          string
	Title          string
	AITitle        string
	LastPrompt     string
	ContextTokens  int
	PRs            []PRLink
	Artifacts      []string
	ArtifactTitles map[string]string
	Limit          Limit
	LastUsed       time.Time
	PromptedAt     time.Time
}

func (s Summary) ContextWindow() int {
	if strings.Contains(s.Model, "[1m]") || s.ContextTokens > 200000 {
		return 1000000
	}
	return 200000
}

func (s Summary) ContextPercent() int {
	return s.ContextTokens * 100 / s.ContextWindow()
}

type File struct {
	Path      string
	SessionID string
	ModTime   time.Time
}

func List(claudeHome string) ([]File, error) {
	paths, err := filepath.Glob(filepath.Join(claudeHome, "projects", "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(paths))
	seen := map[string]int{}
	for _, p := range paths {
		id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if !ValidID(id) {
			continue
		}
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		f := File{Path: p, SessionID: id, ModTime: info.ModTime()}
		if i, ok := seen[id]; ok {
			if better(f, files[i]) {
				files[i] = f
			}
			continue
		}
		seen[id] = len(files)
		files = append(files, f)
	}
	return files, nil
}

func inWorktree(path string) bool {
	return strings.Contains(filepath.Base(filepath.Dir(path)), "--claude-worktrees-")
}

func better(a, b File) bool {
	if inWorktree(a.Path) != inWorktree(b.Path) {
		return !inWorktree(a.Path)
	}
	return a.ModTime.After(b.ModTime)
}

func ValidID(id string) bool {
	return id != "" && id == filepath.Base(id) && !strings.HasPrefix(id, ".")
}

const cacheVersion = 6

type entry struct {
	Version int
	Size    int64
	ModTime time.Time
	Offset  int64
	Calls   map[string]artifactCall
	Pages   map[string]string
	Summary Summary
}

type Reader struct {
	mu      sync.Mutex
	path    string
	entries map[string]*entry
}

func NewReader(cachePath string) *Reader {
	r := &Reader{path: cachePath, entries: map[string]*entry{}}
	if data, err := os.ReadFile(cachePath); err == nil {
		var loaded map[string]*entry
		if json.Unmarshal(data, &loaded) == nil && loaded != nil {
			r.entries = loaded
		}
	}
	return r
}

func (r *Reader) Save() error {
	r.mu.Lock()
	data, err := json.Marshal(r.entries)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".summaries-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}

func (r *Reader) Summary(path string) (Summary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return Summary{}, err
	}
	e := r.entries[path]
	if e == nil || e.Version != cacheVersion || info.Size() < e.Offset || info.Size() < e.Size || info.ModTime().Before(e.ModTime) {
		e = &entry{Version: cacheVersion}
	}
	if e.Offset < info.Size() {
		if err := scan(path, e); err != nil {
			return Summary{}, err
		}
	}
	e.Size = info.Size()
	e.ModTime = info.ModTime()
	r.entries[path] = e
	s := e.Summary
	s.ArtifactTitles = maps.Clone(s.ArtifactTitles)
	s.SessionID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if s.LastUsed.IsZero() {
		s.LastUsed = info.ModTime()
	}
	return s, nil
}

const maxCandidates = 8

var (
	aiTitleMark    = []byte(`"type":"ai-title"`)
	titleMark      = []byte(`"type":"custom-title"`)
	lastPromptMark = []byte(`"type":"last-prompt"`)
	prLinkMark     = []byte(`"type":"pr-link"`)
	assistantMark  = []byte(`"type":"assistant"`)
	humanMark      = []byte(`"kind":"human"`)
	artifactMark   = []byte(`/artifact/`)
	artifactRe     = regexp.MustCompile(`https://claude\.ai/(?:code/)?artifact/[A-Za-z0-9-]+`)
)

type record struct {
	Type       string `json:"type"`
	AITitle    string `json:"aiTitle"`
	Title      string `json:"customTitle"`
	LastPrompt string `json:"lastPrompt"`
	PRNumber   int    `json:"prNumber"`
	PRURL      string `json:"prUrl"`
	PRRepo     string `json:"prRepository"`
	Cwd        string `json:"cwd"`
	GitBranch  string `json:"gitBranch"`
	Timestamp  string `json:"timestamp"`
	Origin     origin `json:"origin"`
	Attachment struct {
		Type   string `json:"type"`
		Origin origin `json:"origin"`
	} `json:"attachment"`
	Message struct {
		Model string `json:"model"`
		Usage struct {
			Input         int `json:"input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type origin struct {
	Kind string `json:"kind"`
}

func (r record) human() bool {
	switch r.Type {
	case "user":
		return r.Origin.Kind == "human"
	case "attachment":
		return r.Attachment.Type == "queued_command" && r.Attachment.Origin.Kind == "human"
	}
	return false
}

func scan(path string, e *entry) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(e.Offset, io.SeekStart); err != nil {
		return err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	var pending []byte
	var candidates []string
	for {
		chunk, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			pending = append(pending, chunk...)
			continue
		}
		line := chunk
		if pending != nil {
			line = append(pending, chunk...)
			pending = nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		e.Offset += int64(len(line))
		if bytes.Contains(line, assistantMark) {
			if len(candidates) == maxCandidates {
				candidates = candidates[1:]
			}
			candidates = append(candidates, string(line))
		}
		if bytes.Contains(line, humanMark) {
			var rec record
			if json.Unmarshal(line, &rec) == nil && rec.human() {
				if t, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
					e.Summary.PromptedAt = t
				}
			}
		}
		apply(e, line)
	}
	applyAssistant(e, candidates)
	return nil
}

func apply(e *entry, line []byte) {
	switch {
	case bytes.Contains(line, aiTitleMark):
		var rec record
		if json.Unmarshal(line, &rec) == nil && rec.Type == "ai-title" {
			e.Summary.AITitle = rec.AITitle
		}
	case bytes.Contains(line, titleMark):
		var rec record
		if json.Unmarshal(line, &rec) == nil && rec.Type == "custom-title" {
			e.Summary.Title = rec.Title
		}
	case bytes.Contains(line, lastPromptMark):
		var rec record
		if json.Unmarshal(line, &rec) == nil && rec.Type == "last-prompt" {
			e.Summary.LastPrompt = rec.LastPrompt
		}
	case bytes.Contains(line, prLinkMark):
		var rec record
		if json.Unmarshal(line, &rec) == nil && rec.Type == "pr-link" {
			addPR(&e.Summary, PRLink{Number: rec.PRNumber, URL: rec.PRURL, Repo: rec.PRRepo})
		}
	}
	noteLimit(e, line)
	noteToolCalls(e, line)
	applyToolResults(e, line)
	if bytes.Contains(line, artifactMark) {
		applyListTitles(e, line)
	}
}

func applyAssistant(e *entry, candidates []string) {
	var rec record
	found := false
	for i := len(candidates) - 1; i >= 0 && !found; i-- {
		rec = record{}
		found = json.Unmarshal([]byte(candidates[i]), &rec) == nil && rec.Type == "assistant"
	}
	if !found {
		return
	}
	s := &e.Summary
	s.Cwd = rec.Cwd
	s.Branch = rec.GitBranch
	s.Model = rec.Message.Model
	u := rec.Message.Usage
	s.ContextTokens = u.Input + u.CacheRead + u.CacheCreation
	if t, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil {
		s.LastUsed = t
	}
}

func addPR(s *Summary, p PRLink) {
	for _, existing := range s.PRs {
		if existing.URL == p.URL {
			return
		}
	}
	s.PRs = append(s.PRs, p)
}

func addArtifact(s *Summary, url string) {
	for _, existing := range s.Artifacts {
		if existing == url {
			return
		}
	}
	s.Artifacts = append(s.Artifacts, url)
}
