package typing

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/benteven/deckhand/internal/live"
	"github.com/benteven/deckhand/internal/paths"
	"github.com/benteven/deckhand/internal/tmux"
)

var (
	ErrMenu   = errors.New("the session shows a menu")
	ErrPasted = errors.New("a menu opened before Enter")

	seq      atomic.Int64
	menuLine = regexp.MustCompile(`(?m)^\s*(❯\s*)?1\.\s+Yes\b`)
)

func Asking(t tmux.Client, id, pane string) bool {
	sessions, _ := live.Running(paths.ClaudeHome())
	for _, s := range sessions {
		if s.SessionID == id && s.Status == "waiting" {
			return true
		}
	}
	screen, err := t.Run("capture-pane", "-p", "-t", pane, "-S", "-20")
	return err == nil && menuLine.MatchString(screen)
}

func Type(t tmux.Client, id, pane, text string) error {
	if Asking(t, id, pane) {
		return ErrMenu
	}
	buffer := fmt.Sprintf("deck-type-%d-%d", os.Getpid(), seq.Add(1))
	if err := t.RunInput(text, "load-buffer", "-b", buffer, "-"); err != nil {
		return err
	}
	if _, err := t.Run("paste-buffer", "-p", "-d", "-b", buffer, "-t", pane); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond)
	if Asking(t, id, pane) {
		return ErrPasted
	}
	_, err := t.Run("send-keys", "-t", pane, "Enter")
	return err
}
