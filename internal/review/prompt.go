package review

import "fmt"

func NotesPrompt(pr PR, src Source, notesPath string) string {
	cmd := "git diff " + src.MergeBase
	if !src.CheckedOut {
		cmd += " " + src.Head
	}
	return fmt.Sprintf(`Review PR #%d "%s" (branch %s, base %s). Read the change with: %s
Write JSON to %s shaped {"files":[{"path":"...","group":"changes|refactor|imports","note":"..."}]}.
List changes most important first. Use refactor when behaviour does not change and imports when only imports change. Skip test and generated files.
Each note says in one or two short lines what the change does and where it fits.
Then wait for my review comments. Discuss when I ask, change code when I tell you to.`, pr.Number, pr.Title, pr.Head, pr.Base, cmd, notesPath)
}

func CommentPrompt(pr PR, src Source, path string, l Line, comment string) string {
	where := fmt.Sprintf("%s:%d", path, l.New)
	if l.Kind == Del {
		where = fmt.Sprintf("%s removed line %d", path, l.Old)
	}
	branch := pr.Head
	if !src.CheckedOut {
		branch += " not checked out"
	}
	return fmt.Sprintf("Review comment on PR #%d, %s (branch %s):\n> %s\n%s", pr.Number, where, branch, l.Text, comment)
}
