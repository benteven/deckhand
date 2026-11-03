package review

import (
	"bytes"
	"path"
	"slices"
	"sort"
	"strings"
)

type Group int

const (
	Changes Group = iota
	Refactor
	Imports
	Tests
	Generated
)

func (g Group) Label() string {
	return [...]string{"Changes", "Refactor only", "Imports only", "Tests", "Generated"}[g]
}

type File struct {
	Path, OldPath  string
	Added, Removed int
	Binary         bool
	Group          Group
	Note           string
}

var (
	lockFiles    = []string{"go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb", "Cargo.lock", "poetry.lock"}
	genSuffixes  = []string{".pb.go", "_gen.go", ".gen.go", ".gen.ts", ".snap"}
	testSegments = []string{"test", "tests", "__tests__", "testdata"}
)

func Fixed(p string, head []byte, generatedAttr bool) (Group, bool) {
	base := path.Base(p)
	if strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return Tests, true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if slices.Contains(testSegments, seg) {
			return Tests, true
		}
	}
	if generatedAttr || slices.Contains(lockFiles, base) {
		return Generated, true
	}
	for _, s := range genSuffixes {
		if strings.HasSuffix(base, s) {
			return Generated, true
		}
	}
	for i, line := range bytes.SplitN(head, []byte("\n"), 6) {
		if i < 5 && bytes.Contains(line, []byte("Code generated")) && bytes.Contains(line, []byte("DO NOT EDIT")) {
			return Generated, true
		}
	}
	return Changes, false
}

var noteGroups = map[string]Group{"changes": Changes, "refactor": Refactor, "imports": Imports}

func Arrange(files []File, notes []Note) []File {
	rank := map[string]int{}
	byPath := map[string]Note{}
	for i, n := range notes {
		if _, ok := byPath[n.Path]; !ok {
			byPath[n.Path], rank[n.Path] = n, i
		}
	}
	out := slices.Clone(files)
	for i := range out {
		f := &out[i]
		if f.Group == Tests || f.Group == Generated {
			continue
		}
		f.Group = Changes
		n, ok := byPath[f.Path]
		if !ok {
			continue
		}
		f.Note = n.Note
		if g, ok := noteGroups[n.Group]; ok {
			f.Group = g
		}
	}
	order := func(f File) int {
		if r, ok := rank[f.Path]; ok && f.Group < Tests {
			return r
		}
		return len(notes)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		if oa, ob := order(a), order(b); oa != ob {
			return oa < ob
		}
		return a.Path < b.Path
	})
	return out
}
