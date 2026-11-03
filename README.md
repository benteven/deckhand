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
| `ctrl-j` / `ctrl-k` | Next or previous session |
| `ctrl-b 1` ... `ctrl-b 9` | Go to session 1 to 9 |
| `ctrl-b <` / `ctrl-b >` | Narrower or wider list |
| `ctrl-b ?` | Show all keys |
| `ctrl-q` or `ctrl-b ctrl-b` | Like Cmd-Tab: go to the next session in the list, press again to go further; when you release ctrl, the pick moves to the top |
| `ctrl-shift-q` | The same, from the bottom of the list up |
| `ctrl-b ctrl-j`, `ctrl-b ctrl-k`, `ctrl-b ctrl-q` | Send that key to Claude Code |
| `ctrl-x ctrl-b` | Claude Code's own "run in background" (its `ctrl-b`) |

On macOS with the Homebrew build, deckhand sees when you release ctrl. Other builds pick the session after a 1.5 second pause.

In the list:

| Key | Does |
| --- | --- |
| arrows, `j` / `k` | Move; the right side follows |
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

## How it works

deckhand runs its own tmux server (socket `deckhand`), so your own tmux setup is not touched. The list is a Bubble Tea program in the left pane. Each Claude Code session is a real process in its own tmux pane, swapped into the right side when you select it.

Session data comes from Claude Code's own files in `~/.claude`, read only. The one exception is `d`, which moves a transcript to the Trash. Sessions started by the app get hooks through `claude --settings`, which mark when a session waits for you; your global Claude Code settings are not changed.

App state lives in `~/.config/deckhand`. Set `"prefix"` in `config.json` to use another key than `ctrl-b`, `"claude_args"` to pass extra flags to every session, and `"list_width"` (24 to 100, default 36) for the list width. Drag the border or press `ctrl-b <` and `ctrl-b >` to change it and keep the new width.

## License

MIT
