package ctl

import (
	"os"
	"path/filepath"
)

const pluginJSON = `{"name":"deckhand","description":"deckhand sessions"}` + "\n"

func subsessionSkill(exe string) string {
	return `---
name: subsession
description: Start, list and close deckhand subsessions: full Claude sessions in their own git worktree that you can message and the user can open.
---

The deckhand binary is ` + exe + `.

1. Run ` + "`ListAgents`" + ` and copy your own address from "This session is X [id]".
2. Commit first if the subsession needs your uncommitted changes.
3. Start: ` + "`" + exe + ` sub start --name <short-name> --parent "<your address>" -- "<task>"` + "`" + `. It prints the subsession's name and worktree.
4. Talk to it with ` + "`SendMessage`" + ` to that name. It reports back to you the same way.
5. List: ` + "`" + exe + " sub list`" + `. Close: ` + "`" + exe + " sub close <name>`" + `. Closing asks the user about the worktree; do not remove it yourself.
6. A subsession cannot start subsessions.
`
}

func tasksSkill(exe string) string {
	return `---
name: tasks
description: List and start the tasks of a deckhand project, start roles, take a queue turn, and report the state of your own task.
---

The deckhand binary is ` + exe + `.

1. A project: ` + "`" + exe + " task list`" + ` shows your open tasks as JSON lines.
2. A project: ` + "`" + exe + " task start <id> [--model fable|opus|sonnet]`" + ` starts the task as a fork of your conversation, in its own worktree from the default branch. Opus is the default; use Fable only for a very important task.
3. A project: ` + "`" + exe + " role list`" + ` shows the roles. ` + "`" + exe + " role start <role> --task <id>`" + ` starts one in the task's worktree.
4. A task: ` + "`" + exe + " task update [--status S] [--need TEXT] [--waits-on W] [--pr URL]`" + ` changes your own task. S is todo, working, needs-you or waiting.
5. A task: ` + "`" + exe + " task done [--summary TEXT]`" + ` when the PR is merged.
6. A task that needs the user asks in its own reply and ends its turn. Deckhand sets the task to needs-you and tells the assistant.
7. A role: ` + "`" + exe + " role model fable|opus|sonnet`" + ` restarts you on that model in the same conversation.
8. A role: ` + "`" + exe + " queue take [name]`" + ` before you start the running system, and ` + "`" + exe + " queue give [name]`" + ` when you stop it. ` + "`" + exe + " queue list`" + ` shows the turns.
`
}

func assistantTasksSkill(exe string) string {
	return `---
name: tasks
description: Make, list, change and close the user's deckhand tasks, mark projects, answer the task page chat and read the queue. Only the assistant can use it.
---

The deckhand binary is ` + exe + `.

1. ` + "`" + exe + " task new --title T [--prio urgent|high|normal|low] [--deadline YYYY-MM-DD] [--from F] [--project NAME] -- CONTEXT`" + `: prints the new task ID. With no project, the task is a plain to-do.
2. ` + "`" + exe + " task list`" + `: open tasks as JSON lines.
3. ` + "`" + exe + " task update <id> [--title T] [--prio P] [--deadline D] [--status S] [--need TEXT] [--waits-on W] [--pr URL] [--project NAME]`" + `.
4. ` + "`" + exe + " task done <id> [--summary TEXT]`" + `.
5. ` + "`" + exe + " project list`" + `, ` + "`" + exe + " project mark <id>`" + ` and ` + "`" + exe + " project unmark <id>`" + `. A project holds the context of one area and starts its tasks.
6. ` + "`" + exe + " chat reply -- TEXT`" + `: answers the user in the chat on the task page.
7. ` + "`" + exe + " queue list`" + `: who has each queue turn and who waits.
`
}

func sessionsSkill(exe string) string {
	return `---
name: sessions
description: List, search, start, resume, type into, hand off and close deckhand sessions. Only the assistant can use it.
---

The deckhand binary is ` + exe + `.

1. ` + "`" + exe + " session list`" + `: JSON lines with id, name, folder, state, need, message.
2. ` + "`" + exe + " session search <words>`" + `: suspended sessions that match, newest first.
3. ` + "`" + exe + " session start --folder F --name N [--account A] -- PROMPT`" + `.
4. ` + "`" + exe + " session resume <id>`" + `.
5. ` + "`" + exe + " session send <id> -- TEXT`" + `: types as the user. Refused while the session shows a menu; bring the menu to the user instead.
6. ` + "`" + exe + " session handoff <id>`" + ` and ` + "`" + exe + " session close <id>`" + `.
`
}

func writePlugin(dir string, skills map[string]string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	meta := filepath.Join(dir, ".claude-plugin")
	if err := os.MkdirAll(meta, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(meta, "plugin.json"), []byte(pluginJSON), 0o644); err != nil {
		return err
	}
	for name, text := range skills {
		skillDir := filepath.Join(dir, "skills", name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func WritePlugins(home, exe string) error {
	if err := writePlugin(filepath.Join(home, "plugin"), map[string]string{"subsession": subsessionSkill(exe), "tasks": tasksSkill(exe)}); err != nil {
		return err
	}
	return writePlugin(filepath.Join(home, "plugin-assistant"), map[string]string{
		"subsession": subsessionSkill(exe),
		"sessions":   sessionsSkill(exe),
		"tasks":      assistantTasksSkill(exe),
	})
}
