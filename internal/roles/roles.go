package roles

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

type Role struct {
	Name        string
	Description string
	Model       string
	Queue       string
	Body        string
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       string `yaml:"model"`
	Deckhand    string `yaml:"deckhand"`
	Queue       string `yaml:"queue"`
}

func parse(path string, needMark bool) (Role, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Role{}, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	var fm frontmatter
	body := text
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		head, tail, found := strings.Cut(rest, "\n---")
		if found {
			if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
				return Role{}, fmt.Errorf("%s: %w", path, err)
			}
			_, body, _ = strings.Cut(tail, "\n")
		}
	}
	if needMark && fm.Deckhand != "role" {
		return Role{}, os.ErrNotExist
	}
	name := fm.Name
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	return Role{
		Name:        name,
		Description: fm.Description,
		Model:       fm.Model,
		Queue:       fm.Queue,
		Body:        strings.TrimSpace(body),
	}, nil
}

func repoPath(repo, name string) string {
	return filepath.Join(repo, ".claude", "agents", name+".md")
}

func homePath(home, name string) string {
	return filepath.Join(home, "roles", name+".md")
}

func Find(home, repo, name string) (Role, error) {
	if name == "" || name != filepath.Base(name) {
		return Role{}, fmt.Errorf("invalid role name %q", name)
	}
	if repo != "" {
		if r, err := parse(repoPath(repo, name), true); err == nil {
			return r, nil
		}
	}
	r, err := parse(homePath(home, name), false)
	if err != nil {
		return Role{}, fmt.Errorf("role %q not found", name)
	}
	return r, nil
}

func List(home, repo string) []Role {
	byName := map[string]Role{}
	collect := func(dir string, needMark bool) {
		files, _ := filepath.Glob(filepath.Join(dir, "*.md"))
		for _, f := range files {
			if r, err := parse(f, needMark); err == nil {
				r.Name = strings.TrimSuffix(filepath.Base(f), ".md")
				byName[r.Name] = r
			}
		}
	}
	collect(filepath.Join(home, "roles"), false)
	if repo != "" {
		collect(filepath.Join(repo, ".claude", "agents"), true)
	}
	out := make([]Role, 0, len(byName))
	for _, r := range byName {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
