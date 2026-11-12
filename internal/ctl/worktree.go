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
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if text := strings.TrimSpace(string(exit.Stderr)); text != "" {
				return "", errors.New(text[strings.LastIndex(text, "\n")+1:])
			}
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func TopLevel(dir string) string {
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return top
}

func Create(cwd, name string) (dir, repo, branch string, err error) {
	return add(cwd, name, func(string) (string, error) { return "HEAD", nil })
}

func CreateFrom(cwd, name, base string) (dir, repo, branch string, err error) {
	return add(cwd, name, func(repo string) (string, error) {
		if base == "" {
			base = defaultBranch(repo)
		}
		if _, err := git(repo, "fetch", "--quiet", "origin", base); err != nil {
			return "", fmt.Errorf("git fetch origin %s: %w", base, err)
		}
		return "origin/" + base, nil
	})
}

func defaultBranch(repo string) string {
	ref, err := git(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if b, ok := strings.CutPrefix(ref, "origin/"); err == nil && ok && b != "" {
		return b
	}
	return "main"
}

func Taken(repo, name string) bool {
	if _, err := os.Lstat(Worktree(repo, name)); err == nil {
		return true
	}
	_, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

func add(cwd, name string, from func(repo string) (string, error)) (dir, repo, branch string, err error) {
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
	start, err := from(repo)
	if err != nil {
		return "", "", "", err
	}
	_, missing := git(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	if _, err := git(repo, "worktree", "add", "--no-track", "-b", branch, wt, start); err != nil {
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
	if !filepath.IsAbs(repo) {
		return fmt.Errorf("not an absolute repo path: %q", repo)
	}
	if err := trash(Worktree(repo, name)); err != nil {
		return err
	}
	return Prune(repo)
}

func Prune(repo string) error {
	_, err := git(repo, "worktree", "prune")
	return err
}

func DeleteBranch(repo, branch string) error {
	_, err := git(repo, "branch", "-D", branch)
	return err
}

func FirstPrompt(parent, dir, branch, task string) string {
	return fmt.Sprintf("You are a subsession of %s, started by deckhand. You work in your own git worktree at %s, on branch %s. Report to %s with SendMessage when you finish or need a decision.\n\nTask:\n%s", parent, dir, branch, parent, task)
}
