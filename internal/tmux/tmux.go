package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/benteven/deckhand/internal/store"
)

const (
	DeckSession  = "=deck"
	BgSession    = "=deck-bg"
	ListPane     = DeckSession + ":main.0"
	Slot         = DeckSession + ":main.1"
	WidthOption  = "@deck-list-width"
	NextKey      = "\ue000"
	PrevKey      = "\ue001"
	LastKey      = "\ue002"
	BackKey      = "\ue003"
	ReviewRole   = "review"
	ReviewOption = "@deck-review"
)

type Client struct {
	Socket, Conf string
}

type Pane struct {
	ID, SessionID, Role, Target string
	Account, Review             string
	PID                         int
	Dead                        bool
}

func (c Client) Run(args ...string) (string, error) {
	full := append([]string{"-L", c.Socket, "-f", c.Conf}, args...)
	out, err := exec.Command("tmux", full...).CombinedOutput()
	text := strings.TrimRight(string(out), "\n")
	if err != nil {
		return text, fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, text)
	}
	return text, nil
}

func (c Client) RunInput(input string, args ...string) error {
	full := append([]string{"-L", c.Socket, "-f", c.Conf}, args...)
	cmd := exec.Command("tmux", full...)
	cmd.Stdin = strings.NewReader(input)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (c Client) Panes() ([]Pane, error) {
	format := strings.Join([]string{
		"#{pane_id}", "#{@deck-session}", "#{@deck-role}",
		"#{session_name}:#{window_name}.#{pane_index}", "#{pane_pid}", "#{pane_dead}", "#{@deck-account}", "#{" + ReviewOption + "}",
	}, "\t")
	out, err := c.Run("list-panes", "-a", "-F", format)
	if err != nil {
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 8 {
			continue
		}
		pid, _ := strconv.Atoi(f[4])
		panes = append(panes, Pane{ID: f[0], SessionID: f[1], Role: f[2], Target: f[3], PID: pid, Dead: f[5] == "1", Account: f[6], Review: f[7]})
	}
	return panes, nil
}

func (c Client) hasSession(target string) bool {
	_, err := c.Run("has-session", "-t", target)
	return err == nil
}

func (c Client) slotPanes() int {
	out, err := c.Run("list-panes", "-t", DeckSession+":main", "-F", "#{pane_id}")
	if err != nil || out == "" {
		return 0
	}
	return len(strings.Split(out, "\n"))
}

func (c Client) HasLayout() bool {
	return c.hasSession(DeckSession) && c.hasSession(BgSession) && c.slotPanes() >= 2
}

func (c Client) run(steps ...[]string) error {
	for _, s := range steps {
		if _, err := c.Run(s...); err != nil {
			return err
		}
	}
	return nil
}

const BuildOption = "@deck-build"

func listCommand(exe string) string {
	return "sh -c " + ShellQuote("while true; do "+ShellQuote(exe)+" ui; sleep 1; done")
}

func previewCommand(exe string) string {
	return ShellQuote(exe) + " preview"
}

func ReviewCommand(exe, id string) string {
	return ShellQuote(exe) + " review " + ShellQuote(id)
}

func (c Client) Reload(exe, build string) error {
	out, err := c.Run("list-panes", "-a", "-F", "#{pane_id}\t#{@deck-role}\t#{"+BuildOption+"}\t#{"+ReviewOption+"}")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 || (build != "" && f[2] == build) {
			continue
		}
		var cmd string
		switch f[1] {
		case "list":
			cmd = listCommand(exe)
		case "preview":
			cmd = previewCommand(exe)
		case ReviewRole:
			cmd = ReviewCommand(exe, f[3])
		default:
			continue
		}
		if _, err := c.Run("respawn-pane", "-k", "-t", f[0], cmd); err != nil {
			return err
		}
	}
	return nil
}

func (c Client) EnsureLayout(exe string, width int) error {
	if !c.hasSession(BgSession) {
		err := c.run(
			[]string{"new-session", "-d", "-s", "deck-bg", "-n", "keep", "-x", "200", "-y", "50", "sh -c 'while :; do sleep 3600; done'"},
			[]string{"set-option", "-p", "-t", BgSession + ":keep", "@deck-role", "keep"},
		)
		if err != nil {
			return err
		}
	}
	if !c.hasSession(DeckSession) {
		err := c.run(
			[]string{"new-session", "-d", "-s", "deck", "-n", "main", "-x", "200", "-y", "50", listCommand(exe)},
			[]string{"set-option", "-p", "-t", ListPane, "@deck-role", "list"},
		)
		if err != nil {
			return err
		}
	}
	if c.slotPanes() < 2 {
		err := c.run(
			[]string{"split-window", "-h", "-t", ListPane, previewCommand(exe)},
			[]string{"set-option", "-p", "-t", Slot, "@deck-role", "preview"},
			[]string{"set-option", "-p", "-t", Slot, "@deck-session", ""},
		)
		if err != nil {
			return err
		}
	}
	if err := c.SetListWidth(width); err != nil {
		return err
	}
	_, err := c.Run("select-pane", "-t", ListPane)
	return err
}

func (c Client) SetListWidth(width int) error {
	w := strconv.Itoa(width)
	_, err := c.Run("set-option", "-g", WidthOption, w, ";", "resize-pane", "-t", ListPane, "-x", w)
	return err
}

func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

const listFocused = "#{&&:#{==:#{session_name},deck},#{==:#{pane_index},0}}"

func switchKey(key, code string) string {
	return fmt.Sprintf(`bind -n %s if -F "%s" "if -F '#{@deck-dialog}' 'send-keys -t =deck:main.0 %s' 'send-keys -t =deck:main.1 %s ; select-pane -t =deck:main.1'" "send-keys -t =deck:main.0 -l %s"`, key, listFocused, key, key, code)
}

func slotKey(key string) string {
	return fmt.Sprintf(`bind -T deck %s send-keys -t =deck:main.1 %s \; select-pane -t =deck:main.1`, key, key)
}

func listClick(event, other string) string {
	return fmt.Sprintf(`bind -n %s if-shell -F -t = "#{==:#{@deck-role},list}" { send-keys -M -t = } { %s }`, event, other)
}

func WriteConf(path string, cfg store.Config) error {
	p := cfg.Prefix
	lines := []string{
		"set -g prefix None",
		"unbind -q -a",
		fmt.Sprintf(`bind -n %s select-pane -t =deck:main.0 \; switch-client -T deck`, p),
		"bind -T deck Any send-keys -t =deck:main.0",
		`bind -T deck Tab if -F "#{==:#{window_panes},3}" "select-pane -t =deck:main.1" "send-keys -t =deck:main.0 Tab"`,
		slotKey("C-j"),
		slotKey("C-k"),
		slotKey("C-q"),
		fmt.Sprintf(`bind -n C-q send-keys -t =deck:main.0 -l %s`, LastKey),
		fmt.Sprintf(`bind -n C-S-q send-keys -t =deck:main.0 -l %s`, BackKey),
		fmt.Sprintf(`bind -T deck %s send-keys -t =deck:main.0 -l %s`, p, LastKey),
		switchKey("C-j", NextKey),
		switchKey("C-k", PrevKey),
	}
	lines = append(lines,
		"set -g mouse on",
		listClick("MouseDown1Pane", "select-pane -t = ; send-keys -M"),
		listClick("DoubleClick1Pane", `select-pane -t = ; if-shell -F "#{||:#{pane_in_mode},#{mouse_any_flag}}" { send-keys -M } { copy-mode -H ; send-keys -X select-word }`),
		listClick("TripleClick1Pane", `select-pane -t = ; if-shell -F "#{||:#{pane_in_mode},#{mouse_any_flag}}" { send-keys -M } { copy-mode -H ; send-keys -X select-line }`),
		"set -g remain-on-exit on",
		"set -g focus-events on",
		"set -g extended-keys always",
		"set -g escape-time 0",
		"set -g default-terminal tmux-256color",
		"set -su terminal-features",
		`set -as terminal-features ",*:RGB"`,
		`set -as terminal-features ",xterm*:extkeys:hyperlinks"`,
		"set -g status off",
		`set-hook -g window-resized 'run-shell -C "resize-pane -t =deck:main.0 -x #{@deck-list-width}"'`,
	)
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
