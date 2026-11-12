package ctl

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const excludeLine = "/.claude/worktrees/"

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

func ValidName(s string) bool { return validName.MatchString(s) }

func Worktree(repo, name string) string {
	return filepath.Join(repo, ".claude", "worktrees", name)
}

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		if text := strings.TrimSpace(string(out)); text != "" {
			return "", errors.New(text[strings.LastIndex(text, "\n")+1:])
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func Create(cwd, name string) (dir, repo, branch string, err error) {
	out, err := git(cwd, "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		return "", "", "", fmt.Errorf("%s is not in a git repo", cwd)
	}
	repo, prefix, _ := strings.Cut(out, "\n")
	branch = name
	wt := Worktree(repo, name)
	if err := exclude(repo); err != nil {
		return "", "", "", err
	}
	_, missing := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if _, err := git(repo, "worktree", "add", "-b", branch, wt, "HEAD"); err != nil {
		if missing != nil {
			git(repo, "branch", "-D", branch)
		}
		return "", "", "", err
	}
	dir = filepath.Join(wt, prefix)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		dir = wt
	}
	return dir, repo, branch, nil
}

func exclude(repo string) error {
	common, err := git(repo, "rev-parse", "--git-common-dir")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repo, common)
	}
	path := filepath.Join(common, "info", "exclude")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if slices.Contains(strings.Split(string(data), "\n"), excludeLine) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	line := excludeLine + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		line = "\n" + line
	}
	_, werr := f.WriteString(line)
	return errors.Join(werr, f.Close())
}

func Remove(repo, name string, trash func(...string) error) error {
	if !ValidName(name) {
		return fmt.Errorf("not a subsession name: %s", name)
	}
	if err := trash(Worktree(repo, name)); err != nil {
		return err
	}
	_, err := git(repo, "worktree", "prune")
	return err
}

func FirstPrompt(parent, dir, branch, task string) string {
	return fmt.Sprintf("You are a subsession of %s, started by deckhand. You work in your own git worktree at %s, on branch %s. Report to %s with SendMessage when you finish or need a decision.\n\nTask:\n%s", parent, dir, branch, parent, task)
}
