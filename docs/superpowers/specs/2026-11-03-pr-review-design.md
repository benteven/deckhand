# PR review in deckhand

## Why

Bent reviews PRs that Claude wrote in his deckhand sessions. GitHub is slow for this for two reasons:

1. It shows too much. Tests, generated files, lock files and pure refactors sit in the same list as the change that matters.
2. Comments for Claude go on GitHub, which reads like talking to himself, and Claude has to fetch them.

The review moves into deckhand. A review Claude explains each file in one or two lines, sorts the files by importance, and takes comments directly with the file and line.

## Goals

- Start a review from a session: select the session, press `v`, select one of its PRs.
- Show the full new file with the changes highlighted, not only hunks.
- Group the files: Changes, Refactor only, Imports only, Tests, Generated. Tests and Generated are folded until opened.
- Sort Changes by importance, most important first.
- Give each file a short note: what the change does and where it fits. One or two lines.
- Comment on a line and send it straight to the review Claude, with file and line.
- Move between the PRs of the session without leaving the review.

## Non-goals

- No GitHub review comments, no approvals, no posting of any kind. Nothing leaves the machine.
- No separate worktree. The review works in the folder of the session that made the PR.
- No storage of notes in the repo. Notes live in deckhand's own state.
- No review of PRs that no deckhand session made.

## Anti-goals

- Long notes. If a note needs a paragraph, it has failed. Bent reads volume as padding.
- Methodology prose in the UI: no "Claude analysed N files using…" lines.
- A review screen so slow to start that GitHub is faster again. The diff must show before Claude's notes are ready.

## How it works (decided)

### Start

1. In the list, select a session and press `v`.
2. If the session has one PR, the review starts on it. If it has more, a picker lists them (number and title).
3. Deckhand starts a review Claude in the session's folder, on the session's account, and adds a row "review #N" to the Active list. The row behaves like other session rows: enter opens it, `s` suspends it, `q`/`Q` work as usual.
4. One review covers all the PRs of the session: one row and one review Claude. `J`/`K` change the PR the diff shows, and the row label follows (`review #N`). Notes are kept per PR.

### Calls made by Claude, not by Bent

- `v` works on Live and Stale rows in Active that have PRs. The picker and the `J`/`K` cycle list open and draft PRs only, the same set `visiblePRs` shows.
- The review folder is the session's current folder. If that folder is not a git checkout of the PR's repo, `v` shows an error in the hint line and starts nothing.
- A second `v` on a session that already has a review opens that review on the selected PR.
- A review row takes a 1 to 9 number like any session. State stores the review marker (session it came from, folder, PR list, current PR), so suspend, resume and a `ui` restart bring the row back as a review with its diff pane, not as a plain session.
- A comment is sent at once when the review Claude is idle or busy, since Claude Code queues typed input. When the review Claude is asking (permission prompt or question), the comment waits and is sent when it stops asking. The comment box says it is waiting.

### Screen

Layout A from the mockup, with a file column:

```
│ list      │ file column        │ diff of the selected file          │
│           │ #318  J/K PR       │ note (purple, 1 to 2 lines)        │
│ review #N │ Changes            │ full new file, changes highlighted │
│           │   risk/score.go    │                                    │
│           │ Refactor only      ├────────────────────────────────────┤
│           │ Imports only       │ review Claude                      │
│           │ ▸ Tests (4)        │                                    │
│           │ ▸ Generated (2)    │                                    │
```

- The session list stays on the left as it is today.
- The file column shows the PR number and the groups. Each file shows `+added -removed`.
- The diff pane shows the full new file. Added lines are green, deleted lines show in red at the place they were removed.
- The review Claude sits under the diff, about one third of the height.

### Keys in the diff pane

| Key | Action |
|-----|--------|
| `j` `k` `↑` `↓` | Move the cursor one line |
| `n` / `N` | Next / previous change |
| `]` / `[` | Next / previous file in the file column |
| `c` | Open a comment box on the cursor line |
| `enter` (in the box) | Send the comment |
| `esc` (in the box) | Close the box, send nothing |
| `J` / `K` | Next / previous PR of the same session |

### Comments

A comment goes to the review Claude as a normal prompt, typed into its pane. It carries the file path, the line number and the text of that line, then the comment. The review Claude acts like any Claude session: it changes code when told to and discusses when asked.

### After a change

When files in the folder change, the diff loads again and the cursor stays on the same line where possible. The diff compares the folder (including uncommitted changes) with the merge base of the PR's base branch, so the review Claude's edits show at once.

### Stacked PRs

Only one branch of a stack is checked out at a time. `J`/`K` never check out a branch. A PR whose branch is not checked out is read from its head ref with `git show`, and the file column header shows "not checked out". The comment prompt names the PR's branch, and the review Claude checks it out itself before it changes code. That moves the original session's folder to that branch too, which Bent accepts.

## Constraints

- The right side today is one tmux pane, the slot (`=deck:main.1`), and `Ops.Show` swaps a session pane into it. A review needs two panes there (diff and Claude). Showing a review must put both in the slot area, and switching away must give the slot back to a single pane.
- `ctrl-j` / `ctrl-k` keep their meaning, next / previous session, everywhere (`internal/tmux/tmux.go` `switchKey`). The review does not rebind them.
- `ctrl-q` is the Cmd-Tab style switch and stays as it is.
- All tests run isolated: `env -u TMUX -u CLAUDE_CONFIG_DIR` with temp `CLAUDE_HOME`, `DECKHAND_HOME`, `DECKHAND_SOCKET`, `TMUX_TMPDIR`. Nothing may write under `~/.claude`.
- Repo rules: no code comments, no em dashes, few tests (one per rule).

## Left to the builder

- How the diff pane is built: likely a `deckhand review` subcommand running its own Bubble Tea program, like `deckhand preview`.
- How the review Claude hands its grouping, order and notes to deckhand. Suggested: its first prompt asks it to read the PR and write a JSON file under `DECKHAND_HOME`, which the diff pane watches. This also gives the review Claude the context of the whole PR for the discussion.
- Which files count as Tests and Generated. Suggested: deckhand decides these by rules (test file patterns, `linguist-generated` in `.gitattributes`, "Code generated … DO NOT EDIT" headers, lock files) so they fold at once. Claude decides Changes, Refactor only, Imports only and the order.
- What the file column shows before the notes are ready: suggested, all non-test, non-generated files under Changes in path order, with no notes.
- Where the PR's base and head come from: suggested `gh pr view N --json baseRefName,headRefName,title`.
- The exact text of the prompt that carries a comment.
