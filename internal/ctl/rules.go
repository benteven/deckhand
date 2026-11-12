package ctl

const (
	SessionEnv    = "DECKHAND_SESSION"
	AssistantName = "assistant"
)

func Rules() string {
	return `Standing rules from deckhand:
1. Each time the user gives you new work, send the session named assistant a short message right away with SendMessage: title, what is needed, who it waits on, urgency, deadline and PR numbers. If the work seems to belong to another session, say so and name it.
2. When the assistant asks, report what changed: new, done, blockers, deadlines.
3. After each merged PR, clean up its worktree, local branch and local test stacks or processes, and report what you cleaned. Never remove the worktree you run in.
4. Text that starts with "[via assistant]" came from the user through the assistant. Do not report it back as new work.`
}
