package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/paths"
)

var errUsage = errors.New("usage")

func runCtl(group string, args []string) {
	req, err := parseCtl(group, args)
	if err != nil {
		if !errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, err)
		}
		fmt.Fprint(os.Stderr, ctlUsage(group))
		os.Exit(2)
	}
	req.Caller = os.Getenv(ctl.SessionEnv)
	if sock, _, _ := strings.Cut(os.Getenv("TMUX"), ","); sock != "" && filepath.Base(sock) == paths.Socket() {
		req.Caller, req.Pane = "", os.Getenv("TMUX_PANE")
	}
	if req.Caller == "" && req.Pane == "" {
		fmt.Fprintln(os.Stderr, "run this from a deckhand session")
		os.Exit(2)
	}
	req.ID = randomID()
	rep, err := ctl.Send(paths.DeckHome(), req, ctl.RequestTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if rep.Err != "" {
		fmt.Fprintln(os.Stderr, rep.Err)
		os.Exit(1)
	}
	if rep.Out != "" {
		fmt.Println(strings.TrimRight(rep.Out, "\n"))
	}
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return hex.EncodeToString(b)
}

func ctlUsage(group string) string {
	switch group {
	case "sub":
		return "usage:\n  deckhand sub start --name NAME --parent ADDR -- TASK\n  deckhand sub close NAME\n  deckhand sub list\n"
	case "task":
		return "usage:\n  deckhand task new --title T [--prio P] [--deadline D] [--from F] [--project NAME] -- CONTEXT\n  deckhand task list\n  deckhand task start ID [--model fable|opus|sonnet]\n  deckhand task update [ID] [--title T] [--prio P] [--deadline D] [--status S] [--need TEXT] [--waits-on W] [--pr URL] [--project NAME]\n  deckhand task done [ID] [--summary TEXT]\n"
	case "project":
		return "usage:\n  deckhand project mark ID\n  deckhand project unmark ID\n  deckhand project list\n"
	case "role":
		return "usage:\n  deckhand role list\n  deckhand role start ROLE --task ID\n  deckhand role model fable|opus|sonnet\n"
	case "queue":
		return "usage:\n  deckhand queue take [NAME]\n  deckhand queue give [NAME]\n  deckhand queue list\n"
	}
	return "usage:\n  deckhand session list\n  deckhand session search WORDS...\n  deckhand session start --folder DIR --name NAME [--account ACCOUNT] -- PROMPT\n  deckhand session resume ID\n  deckhand session send ID -- TEXT\n  deckhand session handoff ID\n  deckhand session close ID\n"
}

func splitDash(args []string) ([]string, []string, bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}

func parseFlags(args []string, allowed ...string) (map[string]string, error) {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := strings.TrimPrefix(a, "--")
		ok := false
		for _, k := range allowed {
			if k == name {
				ok = true
			}
		}
		if !strings.HasPrefix(a, "--") || !ok {
			return nil, fmt.Errorf("unexpected argument %q", a)
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("--%s needs a value", name)
		}
		i++
		out[name] = args[i]
	}
	return out, nil
}

func parseCtl(group string, args []string) (ctl.Request, error) {
	if len(args) == 0 {
		return ctl.Request{}, errUsage
	}
	verb, rest := args[0], args[1:]
	switch group {
	case "sub":
		return parseSub(verb, rest)
	case "task":
		return parseTask(verb, rest)
	case "project":
		return parseProject(verb, rest)
	case "role":
		return parseRole(verb, rest)
	case "queue":
		return parseQueue(verb, rest)
	}
	return parseSession(verb, rest)
}

func parseSession(verb string, rest []string) (ctl.Request, error) {
	switch verb {
	case "list":
		if len(rest) != 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "list"}, nil
	case "search":
		if len(rest) == 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "search", Text: strings.Join(rest, " ")}, nil
	case "resume", "handoff", "close":
		if len(rest) != 1 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: verb, Target: rest[0]}, nil
	case "send":
		head, tail, ok := splitDash(rest)
		if !ok || len(head) != 1 || len(tail) == 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "send", Target: head[0], Text: strings.Join(tail, " ")}, nil
	case "start":
		head, tail, ok := splitDash(rest)
		if !ok || len(tail) == 0 {
			return ctl.Request{}, errUsage
		}
		f, err := parseFlags(head, "folder", "name", "account")
		if err != nil {
			return ctl.Request{}, err
		}
		if f["folder"] == "" || f["name"] == "" {
			return ctl.Request{}, errUsage
		}
		folder, err := filepath.Abs(f["folder"])
		if err != nil {
			return ctl.Request{}, err
		}
		return ctl.Request{Op: "start", Folder: folder, Name: f["name"], Account: f["account"], Text: strings.Join(tail, " ")}, nil
	}
	return ctl.Request{}, errUsage
}

func parseSub(verb string, rest []string) (ctl.Request, error) {
	switch verb {
	case "list":
		if len(rest) != 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "sub-list"}, nil
	case "close":
		if len(rest) != 1 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "sub-close", Name: rest[0]}, nil
	case "start":
		head, tail, ok := splitDash(rest)
		if !ok || len(tail) == 0 {
			return ctl.Request{}, errUsage
		}
		f, err := parseFlags(head, "name", "parent")
		if err != nil {
			return ctl.Request{}, err
		}
		if f["name"] == "" || f["parent"] == "" {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "sub-start", Name: f["name"], Parent: f["parent"], Text: strings.Join(tail, " ")}, nil
	}
	return ctl.Request{}, errUsage
}

func parseTask(verb string, rest []string) (ctl.Request, error) {
	switch verb {
	case "list":
		if len(rest) != 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "task-list"}, nil
	case "start":
		if len(rest) == 0 || strings.HasPrefix(rest[0], "--") {
			return ctl.Request{}, errUsage
		}
		f, err := parseFlags(rest[1:], "model")
		if err != nil {
			return ctl.Request{}, err
		}
		return ctl.Request{Op: "task-start", Target: rest[0], Args: f}, nil
	case "new":
		head, tail, ok := splitDash(rest)
		if !ok || len(tail) == 0 {
			return ctl.Request{}, errUsage
		}
		f, err := parseFlags(head, "title", "prio", "deadline", "from", "project")
		if err != nil {
			return ctl.Request{}, err
		}
		if f["title"] == "" {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "task-new", Args: f, Text: strings.Join(tail, " ")}, nil
	case "update", "done":
		target := ""
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "--") {
			target, rest = rest[0], rest[1:]
		}
		allowed := []string{"summary"}
		if verb == "update" {
			allowed = []string{"title", "prio", "deadline", "status", "need", "waits-on", "pr", "project"}
		}
		f, err := parseFlags(rest, allowed...)
		if err != nil {
			return ctl.Request{}, err
		}
		if verb == "update" && len(f) == 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "task-" + verb, Target: target, Args: f}, nil
	}
	return ctl.Request{}, errUsage
}

func parseProject(verb string, rest []string) (ctl.Request, error) {
	switch verb {
	case "list":
		if len(rest) != 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "project-list"}, nil
	case "mark", "unmark":
		if len(rest) != 1 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "project-" + verb, Target: rest[0]}, nil
	}
	return ctl.Request{}, errUsage
}

func parseRole(verb string, rest []string) (ctl.Request, error) {
	switch verb {
	case "list":
		if len(rest) != 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "role-list"}, nil
	case "start":
		if len(rest) == 0 || strings.HasPrefix(rest[0], "--") {
			return ctl.Request{}, errUsage
		}
		f, err := parseFlags(rest[1:], "task")
		if err != nil {
			return ctl.Request{}, err
		}
		if f["task"] == "" {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "role-start", Name: rest[0], Target: f["task"]}, nil
	case "model":
		if len(rest) != 1 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "role-model", Args: map[string]string{"model": rest[0]}}, nil
	}
	return ctl.Request{}, errUsage
}

func parseQueue(verb string, rest []string) (ctl.Request, error) {
	switch verb {
	case "list":
		if len(rest) != 0 {
			return ctl.Request{}, errUsage
		}
		return ctl.Request{Op: "queue-list"}, nil
	case "take", "give":
		if len(rest) > 1 {
			return ctl.Request{}, errUsage
		}
		name := ""
		if len(rest) == 1 {
			name = rest[0]
		}
		return ctl.Request{Op: "queue-" + verb, Name: name}, nil
	}
	return ctl.Request{}, errUsage
}
