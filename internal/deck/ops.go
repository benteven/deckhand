package deck

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/benteven/deckhand/internal/accounts"
	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/paths"
	"github.com/benteven/deckhand/internal/review"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/tmux"
	"github.com/benteven/deckhand/internal/transcript"
)

type Ops struct {
	T         tmux.Client
	Cfg       store.Config
	HooksPath string
	Home      string

	Exe                string
	PluginDir          string
	AssistantPluginDir string
	AssistantDir       string
	AssistantID        string
	Rules              string
}

func (o Ops) Start(cwd, sessionID, name string, resume bool, account, prompt string) (string, error) {
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return "", fmt.Errorf("folder not found: %s", cwd)
	}
	args := []string{paths.ClaudeCommand()}
	envArgs := []string{"env", ctl.SessionEnv + "=" + sessionID}
	if account != "" {
		if !accounts.LoggedIn(o.Home, account) {
			return "", fmt.Errorf("account %s is not logged in, press A to log in", account)
		}
		dir, err := accounts.Prepare(o.Home, paths.ClaudeHome(), account)
		if err != nil {
			return "", err
		}
		envArgs = append(envArgs, "CLAUDE_CONFIG_DIR="+dir)
	}
	args = append(envArgs, args...)
	if resume {
		args = append(args, "--resume", sessionID)
	} else {
		args = append(args, "--session-id", sessionID)
	}
	if name != "" {
		args = append(args, "--name", name)
	}
	args = append(args, "--settings", o.HooksPath)
	args = append(args, o.Cfg.ClaudeArgs...)
	if _, ok, _ := review.Load(o.Home, sessionID); ok {
		args = append(args, "--add-dir="+review.Dir(o.Home, sessionID))
	}
	if o.AssistantID != "" && sessionID == o.AssistantID {
		args = append(args,
			"--plugin-dir="+o.AssistantPluginDir,
			"--append-system-prompt-file="+filepath.Join(o.AssistantDir, "instructions.md"),
			"--allowedTools=Bash("+o.Exe+" session *),Bash("+o.Exe+" sub *)",
		)
	} else {
		args = append(args, "--plugin-dir="+o.PluginDir)
		if o.Rules != "" {
			args = append(args, "--append-system-prompt="+o.Rules)
		}
	}
	if prompt != "" {
		args = append(args, prompt)
	}
	for i, a := range args {
		args[i] = tmux.ShellQuote(a)
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	command := tmux.ShellQuote(shell) + " -lc " + tmux.ShellQuote("exec "+strings.Join(args, " "))

	newWindow := []string{"new-window", "-d", "-P", "-F", "#{pane_id}", "-t", tmux.BgSession + ":"}
	newWindow = append(newWindow, "-c", strings.ReplaceAll(cwd, "#", "##"), command)
	paneID, err := o.T.Run(newWindow...)
	if err != nil {
		return "", err
	}
	paneID = strings.TrimSpace(paneID)
	if _, err := o.T.Run("set-option", "-p", "-t", paneID, "@deck-session", sessionID, ";", "set-option", "-p", "-t", paneID, "@deck-account", account); err != nil {
		return paneID, err
	}
	return paneID, nil
}

func (o Ops) StopJob(job, account string) error {
	cmd := exec.Command(paths.ClaudeCommand(), "stop", job)
	cmd.Env = os.Environ()
	if account != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+accounts.Dir(o.Home, account))
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("claude stop %s: %v: %s", job, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (o Ops) Login(exe, account string) (string, error) {
	command := tmux.ShellQuote(exe) + " login --wait " + tmux.ShellQuote(account)
	paneID, err := o.T.Run("new-window", "-d", "-P", "-F", "#{pane_id}", "-t", tmux.BgSession+":", command)
	if err != nil {
		return "", err
	}
	paneID = strings.TrimSpace(paneID)
	_, err = o.T.Run("set-option", "-p", "-t", paneID, "@deck-role", "login")
	return paneID, err
}

func RemoveAccount(deckHome, name string) error {
	dir := accounts.Dir(deckHome, name)
	if !accounts.ValidName(name) || !accounts.Exists(deckHome, name) {
		return fmt.Errorf("no account %q", name)
	}
	logout := exec.Command(paths.ClaudeCommand(), "auth", "logout")
	logout.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	if out, err := logout.CombinedOutput(); err != nil && accounts.LoggedIn(deckHome, name) {
		return fmt.Errorf("log out of %s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return Trash(dir)
}

const reviewSize = "66%"

func (o Ops) ShowReview(exe, id, claudePane string) error {
	panes, err := o.T.Panes()
	if err != nil {
		return err
	}
	reviewPane := ""
	for _, p := range panes {
		if p.Role != tmux.ReviewRole || p.Review != id {
			continue
		}
		if p.Dead {
			o.T.Run("kill-pane", "-t", p.ID)
			continue
		}
		reviewPane = p.ID
	}
	if reviewPane == "" {
		return o.startReview(exe, id, claudePane)
	}
	current, err := o.T.Run("display-message", "-p", "-t", tmux.Slot, "#{pane_id}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(current) == reviewPane {
		return nil
	}
	if err := o.Show(claudePane); err != nil {
		return err
	}
	_, err = o.T.Run("join-pane", "-d", "-v", "-b", "-l", reviewSize, "-s", reviewPane, "-t", tmux.Slot)
	return err
}

func (o Ops) startReview(exe, id, claudePane string) error {
	if err := o.Show(claudePane); err != nil {
		return err
	}
	paneID, err := o.T.Run("split-window", "-d", "-v", "-b", "-l", reviewSize, "-P", "-F", "#{pane_id}", "-t", tmux.Slot, tmux.ReviewCommand(exe, id))
	if err != nil {
		return err
	}
	paneID = strings.TrimSpace(paneID)
	if _, err := o.T.Run("set-option", "-p", "-t", paneID, "@deck-role", tmux.ReviewRole, ";", "set-option", "-p", "-t", paneID, tmux.ReviewOption, id); err != nil {
		o.T.Run("kill-pane", "-t", paneID)
		return err
	}
	return nil
}

func (o Ops) mainPanes() ([]string, error) {
	out, err := o.T.Run("list-panes", "-t", tmux.DeckSession+":main", "-F", "#{pane_id}\t#{@deck-role}")
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

func (o Ops) parkReviews() error {
	lines, err := o.mainPanes()
	if err != nil {
		return err
	}
	for _, line := range lines {
		id, role, _ := strings.Cut(line, "\t")
		if role != tmux.ReviewRole {
			continue
		}
		if _, err := o.T.Run("break-pane", "-d", "-s", id, "-t", tmux.BgSession+":"); err != nil {
			return err
		}
	}
	return nil
}

func (o Ops) Show(paneID string) error {
	if err := o.parkReviews(); err != nil {
		return err
	}
	current, err := o.T.Run("display-message", "-p", "-t", tmux.Slot, "#{pane_id}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(current) == paneID {
		return nil
	}
	_, err = o.T.Run("swap-pane", "-d", "-s", paneID, "-t", tmux.Slot)
	return err
}

func (o Ops) Focus(target string) error {
	_, err := o.T.Run("select-pane", "-t", target)
	return err
}

func (o Ops) Stop(paneID string, busy bool) error {
	if busy {
		if _, err := o.T.Run("send-keys", "-t", paneID, "C-c"); err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
	lines, err := o.mainPanes()
	if err != nil {
		return err
	}
	for _, line := range lines {
		if id, _, _ := strings.Cut(line, "\t"); id == paneID {
			return fmt.Errorf("pane %s is still in the slot", paneID)
		}
	}
	out, err := o.T.Run("display-message", "-p", "-t", paneID, "#{pane_dead} #{pane_pid}")
	if err != nil {
		return ignoreGone(err)
	}
	if dead, pid, _ := strings.Cut(strings.TrimSpace(out), " "); dead != "1" {
		if n, err := strconv.Atoi(pid); err == nil && n > 0 {
			syscall.Kill(n, syscall.SIGTERM)
		}
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		out, err := o.T.Run("display-message", "-p", "-t", paneID, "#{pane_dead}")
		if err != nil {
			return ignoreGone(err)
		}
		if strings.TrimSpace(out) == "1" {
			break
		}
	}
	_, err = o.T.Run("kill-pane", "-t", paneID)
	return ignoreGone(err)
}

func ignoreGone(err error) error {
	if err != nil && strings.Contains(err.Error(), "can't find pane") {
		return nil
	}
	return err
}

func (o Ops) StopOutside(pid int, startedAt time.Time, timeout time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("no process for this session")
	}
	if syscall.Kill(pid, 0) == syscall.ESRCH {
		return nil
	}
	if err := confirmClaude(pid, startedAt); err != nil {
		return err
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return nil
		}
	}
	return fmt.Errorf("session still running after %s, not taken over", timeout)
}

func ProcessStart(pid int) (start time.Time, comm string, err error) {
	cmd := exec.Command("ps", "-o", "lstart=,comm=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, "", fmt.Errorf("process %d not found", pid)
	}
	f := strings.Fields(string(out))
	if len(f) < 6 {
		return time.Time{}, "", fmt.Errorf("process %d: unexpected ps output %q", pid, out)
	}
	start, err = time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(f[:5], " "), time.Local)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("process %d: %w", pid, err)
	}
	return start, strings.Join(f[5:], " "), nil
}

func confirmClaude(pid int, startedAt time.Time) error {
	if startedAt.IsZero() {
		return fmt.Errorf("process %d has no start time, not taken over", pid)
	}
	start, comm, err := ProcessStart(pid)
	if err != nil {
		return fmt.Errorf("%w, not taken over", err)
	}
	if !strings.Contains(filepath.Base(comm), "claude") {
		return fmt.Errorf("process %d is not claude, not taken over", pid)
	}
	if start.After(startedAt) {
		return fmt.Errorf("process %d started after the session, not taken over", pid)
	}
	return nil
}

func TrashSession(id, transcriptPath string) error {
	if !transcript.ValidID(id) || filepath.Base(transcriptPath) != id+".jsonl" {
		return fmt.Errorf("not a session transcript: %s", transcriptPath)
	}
	paths := []string{transcriptPath}
	dir := filepath.Join(filepath.Dir(transcriptPath), id)
	if info, err := os.Lstat(dir); err == nil && info.IsDir() {
		paths = append(paths, dir)
	}
	return Trash(paths...)
}

func Trash(paths ...string) error {
	args := make([]string, 0, len(paths))
	for _, p := range paths {
		if base := filepath.Base(p); filepath.Clean(p) != p || strings.HasPrefix(base, ".") || base == string(filepath.Separator) {
			return fmt.Errorf("trash: refused %s", p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		args = append(args, abs)
	}
	cmd, err := trashCommand(args)
	if err != nil {
		return err
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	text, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if _, after, ok := strings.Cut(text, "# "); ok {
		text = after
	}
	if text == "" {
		text = err.Error()
	}
	return fmt.Errorf("trash: %s", text)
}
