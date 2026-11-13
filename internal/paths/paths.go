package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

const legacyName = "claude-deck"

func DeckHome() string {
	if v := os.Getenv("DECKHAND_HOME"); v != "" {
		return v
	}
	return filepath.Join(userHome(), ".config", "deckhand")
}

func MigrateHome() (bool, error) {
	if os.Getenv("DECKHAND_HOME") != "" {
		return false, nil
	}
	old := filepath.Join(userHome(), ".config", legacyName)
	if info, err := os.Lstat(old); err != nil || !info.IsDir() {
		return false, nil
	}
	home := DeckHome()
	if _, err := os.Lstat(home); err == nil {
		return false, nil
	}
	if err := os.Rename(old, home); err != nil {
		return false, err
	}
	return true, os.Symlink(home, old)
}

func ClaudeHome() string {
	if v := os.Getenv("CLAUDE_HOME"); v != "" {
		return v
	}
	return filepath.Join(userHome(), ".claude")
}

var socket = sync.OnceValue(func() string {
	if exec.Command("tmux", "-L", legacyName, "has-session", "-t", "=deck").Run() == nil {
		return legacyName
	}
	return "deckhand"
})

func Socket() string {
	if v := os.Getenv("DECKHAND_SOCKET"); v != "" {
		return v
	}
	return socket()
}

func ClaudeCommand() string {
	if v := os.Getenv("DECKHAND_CLAUDE"); v != "" {
		return v
	}
	return "claude"
}

func Opener() string {
	if v := os.Getenv("DECKHAND_OPEN"); v != "" {
		return v
	}
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

func userHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}
