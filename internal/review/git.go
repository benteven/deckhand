package review

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Source struct {
	Cwd, MergeBase, Head string
	CheckedOut           bool
}

type LineKind int

const (
	Same LineKind = iota
	Add
	Del
)

type Line struct {
	Kind     LineKind
	New, Old int
	Text     string
}

func git(cwd string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", cwd}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func Fetch(cwd string, pr PR) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", cwd, "fetch", "-q", "origin", pr.Base, pr.Head)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	cmd.WaitDelay = 3 * time.Second
	return cmd.Run()
}

func ref(cwd, name string, local bool) string {
	if local {
		if _, err := git(cwd, "rev-parse", "--verify", "-q", "refs/heads/"+name); err == nil {
			return name
		}
	}
	if _, err := git(cwd, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+name); err == nil {
		return "origin/" + name
	}
	return name
}

func Resolve(cwd string, pr PR) (Source, error) {
	notCheckout := fmt.Errorf("folder is not a checkout of %s", pr.Repo)
	top, err := git(cwd, "rev-parse", "--show-toplevel")
	if err != nil || top == "" {
		return Source{}, notCheckout
	}
	cwd = top
	if url, err := git(cwd, "remote", "get-url", "origin"); err == nil && !sameRepo(url, pr.Repo) {
		return Source{}, notCheckout
	}
	head := ref(cwd, pr.Head, true)
	base := ref(cwd, pr.Base, false)
	mb, err := git(cwd, "merge-base", base, head)
	if err != nil {
		return Source{}, fmt.Errorf("no common commit for %s and %s", base, head)
	}
	branch, _ := git(cwd, "branch", "--show-current")
	return Source{Cwd: cwd, MergeBase: mb, Head: head, CheckedOut: branch == pr.Head}, nil
}

func sameRepo(url, repo string) bool {
	url = strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(url), "/"), ".git")
	repo = strings.ToLower(repo)
	return strings.HasSuffix(url, "/"+repo) || strings.HasSuffix(url, ":"+repo)
}

func (s Source) diffArgs(args ...string) []string {
	args = append(args, s.MergeBase)
	if !s.CheckedOut {
		args = append(args, s.Head)
	}
	return args
}

func Changed(src Source) ([]File, error) {
	out, err := exec.Command("git", append([]string{"-C", src.Cwd}, src.diffArgs("diff", "--numstat", "-z", "-M")...)...).Output()
	if err != nil {
		return nil, err
	}
	var files []File
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		f := File{Path: parts[2]}
		if parts[0] == "-" {
			f.Binary = true
		}
		f.Added, _ = strconv.Atoi(parts[0])
		f.Removed, _ = strconv.Atoi(parts[1])
		if f.Path == "" && i+2 < len(fields) {
			f.OldPath, f.Path = fields[i+1], fields[i+2]
			i += 2
		}
		files = append(files, f)
	}
	if src.CheckedOut {
		untracked, _ := git(src.Cwd, "ls-files", "--others", "--exclude-standard", "-z")
		for _, p := range strings.Split(untracked, "\x00") {
			if p == "" {
				continue
			}
			data, _ := os.ReadFile(filepath.Join(src.Cwd, p))
			files = append(files, File{Path: p, Added: bytes.Count(data, []byte("\n")), Untracked: true, Binary: bytes.IndexByte(data, 0) >= 0})
		}
	}
	generated := generatedAttrs(src.Cwd, files)
	for i := range files {
		if g, ok := Fixed(files[i].Path, head(src, files[i].Path), generated[files[i].Path]); ok {
			files[i].Group = g
		}
	}
	return files, nil
}

func generatedAttrs(cwd string, files []File) map[string]bool {
	if len(files) == 0 {
		return nil
	}
	args := []string{"-C", cwd, "check-attr", "-z", "linguist-generated", "--"}
	for _, f := range files {
		args = append(args, f.Path)
	}
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+2 < len(fields); i += 3 {
		if v := fields[i+2]; v == "true" || v == "set" {
			set[fields[i]] = true
		}
	}
	return set
}

func head(src Source, p string) []byte {
	var data []byte
	if src.CheckedOut {
		data, _ = os.ReadFile(filepath.Join(src.Cwd, p))
	} else {
		data, _ = exec.Command("git", "-C", src.Cwd, "show", src.Head+":"+p).Output()
	}
	return data[:min(len(data), 512)]
}

func FileDiff(src Source, f File) ([]Line, error) {
	if f.Binary {
		return []Line{{Kind: Same, Text: "binary file"}}, nil
	}
	if f.Untracked {
		data, err := os.ReadFile(filepath.Join(src.Cwd, f.Path))
		if err != nil {
			return nil, err
		}
		var lines []Line
		for i, t := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			lines = append(lines, Line{Kind: Add, New: i + 1, Text: t})
		}
		return lines, nil
	}
	args := append([]string{"-C", src.Cwd}, src.diffArgs("diff", "--no-color", "-U1000000", "-M")...)
	args = append(args, "--")
	if f.OldPath != "" {
		args = append(args, f.OldPath)
	}
	args = append(args, f.Path)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, err
	}
	if lines := ParseFull(string(out)); len(lines) > 0 {
		return lines, nil
	}
	return []Line{{Kind: Same, Text: "no content changes"}}, nil
}

func ParseFull(unified string) []Line {
	var lines []Line
	old, nw, inHunk := 0, 0, false
	for _, l := range strings.Split(strings.TrimSuffix(unified, "\n"), "\n") {
		if strings.HasPrefix(l, "diff --git ") {
			inHunk = false
			continue
		}
		if strings.HasPrefix(l, "@@") {
			var a, c int
			fmt.Sscanf(strings.SplitN(l, " ", 3)[1], "-%d", &a)
			fmt.Sscanf(strings.SplitN(l, " ", 4)[2], "+%d", &c)
			old, nw, inHunk = a, c, true
			continue
		}
		if !inHunk || l == "" {
			continue
		}
		switch l[0] {
		case ' ':
			lines = append(lines, Line{Kind: Same, New: nw, Old: old, Text: l[1:]})
			old++
			nw++
		case '-':
			lines = append(lines, Line{Kind: Del, Old: old, Text: l[1:]})
			old++
		case '+':
			lines = append(lines, Line{Kind: Add, New: nw, Text: l[1:]})
			nw++
		}
	}
	return lines
}
