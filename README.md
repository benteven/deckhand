# deckhand

A terminal app for people who run many Claude Code sessions at once.

Running sessions sit in a list on the left with the live session on the right. Sessions you close stay findable: newest first, searchable, with their last known state shown in gray. Resume one with Enter.

![Active sessions with the live session on the right](https://github.com/user-attachments/assets/69656439-4a51-4d2a-939f-f109e43e0e70)

![Suspended sessions with the last known state in gray](https://github.com/user-attachments/assets/ab4f5043-9b47-468b-a322-765acfab3eb9)

The screenshots use made-up sessions.

deckhand is not made by or affiliated with Anthropic.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/benteven/deckhand/main/install.sh | sh
```

This installs a release binary to `~/.local/bin`. Set `DECKHAND_BIN_DIR` to use another folder, or `DECKHAND_VERSION` to pin a release. You need tmux.

With Homebrew:

```sh
brew install benteven/tap/deckhand
```

This also installs tmux. You need [Claude Code](https://docs.claude.com/en/docs/claude-code) on your PATH. `gh` is optional; with it, rows show the state of linked pull requests.

deckhand was called claude-deck before. Homebrew, the install script and `U` move an old install over: the `claude-deck` command stays as a link, the config moves to `~/.config/deckhand`, and a running deck keeps its sessions until you quit it with `Q`. Accounts need one new login, because Claude Code ties the login to the folder path.

From source (Go 1.27 or later):

```sh
go install github.com/benteven/deckhand@latest
```

macOS and Linux. On Linux, `d` needs `gio` or `trash-cli` to move files to the Trash.

## Use

Run `deckhand` in a plain terminal, not inside tmux.

```
Active 4  Suspended 23
  1 api refactor                 ┃ > fix the review comments
  ━━━━━━━━━━━━ 72% ctx           ┃
  #4631 draft  2 artifacts       ┃ ● Edit src/routes.ts
    ↳ Review routes (tests)      ┃ ● Bash go test ./...
  2 docs cleanup ● waiting       ┃
  ━━━━━━━━━━━━ 31% ctx           ┃ the live Claude Code session
  Elsewhere                      ┃
  billing spike                  ┃
```

Each row shows the context used, open pull requests (● open, ○ draft; green when checks pass, yellow while they run, red when one fails), the artifacts it published, running subagents, a mark when the session waits for you, and a yellow dot next to the number when remote control is on.

### Keys

From anywhere:

| Key | Does |
| --- | --- |
| `ctrl-b` | Focus the list |
| `ctrl-j` / `ctrl-k` | Next or previous session, without subsessions |
| `ctrl-b 1` ... `ctrl-b 9` | Go to session 1 to 9; 1 is always the assistant |
| `ctrl-b <` / `ctrl-b >` | Narrower or wider list |
| `ctrl-b ?` | Show all keys |
| `ctrl-q` or `ctrl-b ctrl-b` | Like Cmd-Tab: go to the next session in the list (subsessions are skipped), press again to go further; when you release ctrl, the pick moves to the top |
| `ctrl-shift-q` | The same, from the bottom of the list up |
| `ctrl-\` | Next session in the same family: the parent, then its subsessions |
| `ctrl-b ctrl-j`, `ctrl-b ctrl-k`, `ctrl-b ctrl-q`, `ctrl-b ctrl-\` | Send that key to Claude Code |
| `ctrl-x ctrl-b` | Claude Code's own "run in background" (its `ctrl-b`) |

On macOS with the Homebrew build, deckhand sees when you release ctrl. Other builds pick the session after a 1.5 second pause.

In the list:

| Key | Does |
| --- | --- |
| arrows, `j` / `k` | Move between sessions and skip subsessions; the right side follows. In a search, move through every match |
| `ctrl-↑` / `ctrl-↓`, `J` / `K` | Move one row, subsessions too |
| Enter | Open the session, or resume a suspended one |
| Esc | Back to the session you came from |
| `1` ... `9` | Go to that session |
| Tab | Switch between Active and Suspended |
| `?` | Show all keys |
| `/` | Search names, prompts, folders, branches and PR numbers; in Suspended, also the whole conversation |
| `n` | New session in a recent folder |
| `s` | Suspend: rename it, then stop it |
| `r` | Rename |
| `R` | Restart the session in place, for example after Claude Code updates. A session that Claude Code parked into a background job is stopped there and resumed in the pane |
| `A` | Switch account for this session or all sessions, add or remove accounts |
| `c` | Hand off: Claude writes its state to a note, the deck runs `/clear`, then Claude reads the note back |
| `g` | Agent mode: `j`/`k` also step through subagents and show the live subagent transcript; Enter opens the parent session |
| `G` | Turn the assistant on or off |
| `t` | Take over a session running in another terminal; on your own session, move it to the next account |
| `o` | Open the row's pull requests |
| `v` | Review the row's pull requests and the rest of their stack, in merge order. In the review: `j`/`k` move, `n`/`N` change, `]`/`[` file, `J`/`K` PR, `t` tests and generated files, `c` comment to Claude, `tab` go to Claude |
| `a` | List the row's artifacts; Enter opens one |
| `d` | Move a suspended session to the Trash |
| `q` | Close the app; sessions keep running |
| `U` | Upgrade to the latest release with Homebrew or the install script, then reload the list and preview; sessions keep running |
| `Q` | Stop all sessions and quit |

Run `deckhand` again to come back to running sessions. After an upgrade, this also reloads the list with the new version, and `deckhand reload` does the same from any shell. The list checks GitHub for a new release every 6 hours and shows a line when one is out. After a restart, sessions that were active show in gray and the app offers to resume them.

### Accounts

Use more than one Claude subscription. Press `A`, pick "+ add account", give it a name, and log in. You can also run `deckhand login <name>` in a terminal. In the list, Enter switches the session and `A` switches all sessions. `d` logs an account out and moves its folder to the Trash.

Each account is a config folder in `~/.config/deckhand/accounts/<name>`, used as `CLAUDE_CONFIG_DIR`. It links to the shared parts of `~/.claude` (transcripts, live sessions, settings, CLAUDE.md, agents, commands, skills, plugins, history), so `--resume` works on any account. The login and account caches stay apart; Claude Code gives each config folder its own Keychain entry. Trusted folders and MCP servers from `~/.claude.json` are copied to the account each time a session starts.

Each account has its own color for session names; `A` shows which color is which, and the default account stays white. `t` moves a session to the next account, and `n` starts on the account of the session under the cursor (`ctrl-a` in the folder list changes it). A switch restarts the session in place. Sessions that are busy or have an open question switch when they are idle. Sessions running elsewhere are not switched.

When a session hits a usage limit, the deck moves it to an account without a limit and sends "continue". The account for new sessions comes first, then the others in order. New sessions and resumes also skip a limited account. After the reset, moved sessions go back to their own account when they are idle and not on screen. The row shows "● limit" with the reset time while no account is free, and `A` shows which accounts are limited.

### Subsessions

A session can start helper sessions with the `subsession` skill. Each subsession is a full Claude Code session in its own git worktree, started from the parent's folder. The skill runs `deckhand sub start --name <name> --parent <address> -- "<task>"`, and `deckhand sub list` and `deckhand sub close <name>` list and close them. The parent and the subsession talk to each other with `SendMessage`.

In the list, subsessions sit under their parent, indented with `↳`. When the parent is suspended, it shows in gray at its place with "suspended", and Enter resumes it. A subsession whose parent is gone shows "sub of" and the parent's name. In Suspended, a suspended subsession sits under its suspended parent. `ctrl-\` steps through a parent and its running subsessions and wraps around; it does nothing for a session with no running family.

Suspending a parent with `s` suspends its running subsessions too, and the prompt says how many are working. They keep their worktrees and wait under the parent in Suspended. When a subsession closes while its parent runs, deckhand asks you what to do with its worktree. Only you can answer. "y" moves the worktree to the Trash and runs `git worktree prune`; the branch stays. A subsession cannot start subsessions: there is one level. Press `R` on a session that ran before the skill existed to give it the skill.

### Assistant

The assistant is a standing Claude Code session that keeps track of your other sessions. It is off by default. Press `G` in the list to turn it on or off. Deckhand asks first, then writes the folder, saves `"assistant": true` in `~/.config/deckhand/config.json` and starts the assistant. Sessions that already run get the standing rules when you restart them with `R`.

The assistant is the first row in the list, with the gray mark "assistant". It runs in `~/.config/deckhand/assistant`. On the first start deckhand writes `instructions.md` there and never overwrites it, so you can edit it. The assistant keeps your work as tasks; see [Tasks](#tasks).

The assistant has the `sessions` skill, which runs `deckhand session` commands. Only the assistant can run them:

| Command | Does |
| --- | --- |
| `session list` | JSON lines with id, name, folder, state and what the session needs |
| `session search <words>` | Suspended sessions that match, newest first |
| `session start --folder F --name N [--account A] -- PROMPT` | Start a session |
| `session resume <id>` | Resume a suspended session |
| `session send <id> -- TEXT` | Type into a session, as you |
| `session handoff <id>`, `session close <id>` | Hand off or stop a session |

`send` types only into a normal prompt. It is refused while the session shows a permission prompt or a question menu, while you are in that pane, and while the session hands off.

Menus go to you. The assistant never answers a permission prompt or a question menu in another session. When a session starts to wait for you and you are not looking at it, deckhand types a "[deckhand]" note about it into the assistant.

Keep-alive: if the assistant stops, deckhand starts it again, at most once in 60 seconds, and shows the error in the list. Each start sends it a prompt to read `instructions.md` and set up its hourly round again. At the first refresh after 04:00 each day, deckhand updates Claude Code (`brew upgrade --cask claude-code` for a Homebrew install, else `claude update`), then restarts the assistant when it is idle and you are not in its pane. Above 60% context, the restart goes through a handoff note. Press `s` on the assistant to pause it: the row stays at the top of Active with the marks "assistant" and "paused", and nothing restarts it until you resume it with Enter.

The rules: with the assistant on, every session you start in deckhand gets a short set of standing rules. Each session tells the assistant about new work, reports what changed when asked, cleans up its worktree and branch after a merged PR, marks text that begins with "[via assistant]" as coming from you, and, when it is a task, keeps its line on the task page current. Each session may also run `deckhand task`, `role` and `queue` without a prompt; the list checks who asks.

### Tasks

With the assistant on, each piece of work is a task. Run `deckhand page` to open the task page in your browser on `http://127.0.0.1:7317` (`"web_port"` in `config.json` changes the port). The page shows tasks that need you at the top, then your to-dos, the tasks at work and the tasks that wait on others. Answer a task or pick a menu option there, open its session in deckhand, or mark it done. `⌘K` opens a chat with the assistant: deckhand types your text into the assistant with the mark "[from the page]", and the assistant answers with `deckhand chat reply -- TEXT`. When no page is open and a task starts to need you, deckhand shows a system notification (`osascript` on macOS, `notify-send` on Linux).

The assistant asks you what it needs, then makes the task with `deckhand task new`. A plain to-do, such as "empty the Trash", has no project; you mark it done. Code work goes to a project: a long-lived session for one area, marked with `deckhand project mark <id>` (`project unmark` and `project list` too). The project starts the task with `deckhand task start <id>` as a fork of its own conversation, in a new worktree from the fetched default branch. The task keeps its line current with `deckhand task update`, and writes a summary with `deckhand task done` when its PR is merged. Deckhand adds the summary to the project's notes file, closes the task, removes the worktree and deletes the branch. A project with no open tasks is suspended after 10 idle minutes.

Roles help a task, for example a tester. A role is a Claude Code agent file (`<name>.md` with `name`, `description` and `model` in its frontmatter, then its instructions). Deckhand writes `project.md`, `task.md` and `tester.md` to `~/.config/deckhand/roles/` once and never overwrites them; add a file there for a new role. A repo can add a role, or replace one of the same name, in `.claude/agents/<name>.md` with `deckhand: role` in the frontmatter. The project runs `deckhand role list` and `deckhand role start <role> --task <id>`; the role runs in the task's worktree.

Models: the assistant runs on Sonnet, projects and tasks on Opus, and the tester on Sonnet; a role file's `model` sets its own. `deckhand task start <id> --model fable` picks another model for one task. A role runs `deckhand role model opus` to restart on another model in the same conversation.

The queue: roles that share ports, a local stack or a database take turns. A role runs `deckhand queue take` before it starts the running system and `deckhand queue give` when it stops it. The queue is per repo; a role file can name another one with `queue:`. `deckhand queue list` shows who has the turn and who waits, and the assistant tells you when a turn is stuck.

A shortcut for the chat on macOS: run `deckhand page` once, so the browser has the page's token. Then open Shortcuts, make a new shortcut with the action "Open URLs" and the URL `http://127.0.0.1:7317/#new`, and in its details add the keyboard shortcut `⌃⌥⌘A`.

## How it works

deckhand runs its own tmux server (socket `deckhand`), so your own tmux setup is not touched. The list is a Bubble Tea program in the left pane. Each Claude Code session is a real process in its own tmux pane, swapped into the right side when you select it.

Session data comes from Claude Code's own files in `~/.claude`, read only. There are two exceptions: `d` moves a transcript to the Trash, and a task start copies its project's transcript to the task's worktree entry in `~/.claude/projects`, so the task can fork from it. Sessions started by the app get hooks through `claude --settings`, which mark when a session waits for you; your global Claude Code settings are not changed.

App state lives in `~/.config/deckhand`. Set `"prefix"` in `config.json` to use another key than `ctrl-b`, `"claude_args"` to pass extra flags to every session, and `"list_width"` (24 to 100, default 36) for the list width. Drag the border or press `ctrl-b <` and `ctrl-b >` to change it and keep the new width.

## License

MIT
