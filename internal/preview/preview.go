package preview

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"github.com/benteven/deckhand/internal/textclean"
	"github.com/benteven/deckhand/internal/transcript"
)

const (
	pollInterval = 200 * time.Millisecond
	tailEntries  = 40
	footer       = "(last known state, read only)"
)

var (
	gray   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	normal = lipgloss.NewStyle()
)

func Render(entries []transcript.Entry, title, note string, width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}
	if title == "" && len(entries) == 0 {
		return centered("No session", width, height)
	}
	var body []string
	for _, e := range entries {
		var text string
		e.Text = textclean.Clean(e.Text)
		switch e.Kind {
		case transcript.Prompt:
			text = "> " + e.Text
		case transcript.Tool:
			text = "● " + e.Text
		default:
			text = e.Text
		}
		body = append(body, strings.Split(ansi.Wrap(text, width, ""), "\n")...)
	}
	body = append(body, note)
	room := height - 1
	if len(body) > room {
		body = body[len(body)-room:]
	}
	lines := make([]string, 0, height)
	lines = append(lines, normal.Render(ansi.Truncate(title, width, "")))
	for _, l := range body {
		lines = append(lines, gray.Render(l))
	}
	return strings.Join(lines, "\n")
}

func centered(text string, width, height int) string {
	pad := max((width-len(text))/2, 0)
	lines := make([]string, height)
	lines[(height-1)/2] = strings.Repeat(" ", pad) + gray.Render(text)
	return strings.Join(lines, "\n")
}

func Print(path string) error {
	entries, err := transcript.Tail(path, tailEntries)
	if err != nil {
		return err
	}
	fmt.Println(Render(entries, path, footer, 100, 40))
	return nil
}

func Run(targetFile string) error {
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	fmt.Print("\x1b[?25l")
	defer fmt.Print("\x1b[?25h")

	var last string
	var lastSize int64 = -1
	var lastW, lastH int
	var lastTarget string
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		w, h, err := term.GetSize(os.Stdout.Fd())
		if err != nil || w < 1 || h < 1 {
			w, h = 80, 24
		}
		target := readTarget(targetFile)
		var size int64 = -1
		if path, _, _ := strings.Cut(target, "\t"); path != "" {
			if info, err := os.Stat(path); err == nil {
				size = info.Size()
			}
		}
		if target != lastTarget || size != lastSize || w != lastW || h != lastH {
			lastTarget, lastSize, lastW, lastH = target, size, w, h
			if out := draw(target, w, h); out != last {
				last = out
				fmt.Print("\x1b[H\x1b[2J" + out)
			}
		}
		select {
		case <-tick.C:
		case <-resize:
		}
	}
}

func readTarget(file string) string {
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\r\n")
}

func draw(target string, w, h int) string {
	if target == "" {
		return Render(nil, "", "", w, h)
	}
	path, rest, _ := strings.Cut(target, "\t")
	title, note, _ := strings.Cut(rest, "\t")
	if title == "" {
		title = path
	}
	if note == "" {
		note = footer
	}
	entries, err := transcript.Tail(path, tailEntries)
	if err != nil {
		return Render(nil, "", "", w, h)
	}
	return Render(entries, title, note, w, h)
}
