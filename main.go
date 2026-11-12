package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/benteven/deckhand/internal/accounts"
	"github.com/benteven/deckhand/internal/ctl"
	"github.com/benteven/deckhand/internal/events"
	"github.com/benteven/deckhand/internal/paths"
	"github.com/benteven/deckhand/internal/preview"
	"github.com/benteven/deckhand/internal/reviewui"
	"github.com/benteven/deckhand/internal/store"
	"github.com/benteven/deckhand/internal/tmux"
	"github.com/benteven/deckhand/internal/ui"
)

var version = "dev"

func main() {
	if moved, err := paths.MigrateHome(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: move config to", paths.DeckHome()+":", err)
	} else if moved {
		accounts.ForgetLogins(paths.DeckHome())
	}
	if len(os.Args) < 2 {
		runLaunch()
		return
	}
	switch os.Args[1] {
	case "preview":
		if len(os.Args) == 2 {
			markBuild()
		}
		runPreview(os.Args[2:])
	case "review":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: deckhand review <session id>")
			os.Exit(2)
		}
		markBuild()
		if err := reviewui.Run(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "hook":
		runHook()
	case "reload":
		if err := reload(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "login":
		if err := login(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "session":
		runCtl("session", os.Args[2:])
	case "sub":
		runCtl("sub", os.Args[2:])
	case "version":
		fmt.Println(version)
	case "ui":
		markBuild()
		if err := ui.Run(version); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
}

func buildID() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

func markBuild() {
	pane, build := os.Getenv("TMUX_PANE"), buildID()
	if pane == "" || build == "" {
		return
	}
	client := tmux.Client{Socket: paths.Socket(), Conf: filepath.Join(paths.DeckHome(), "tmux.conf")}
	if _, err := client.Run("set-option", "-p", "-t", pane, tmux.BuildOption, build); err != nil {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
}

func runPreview(args []string) {
	var err error
	if len(args) == 2 && args[0] == "--print" {
		err = preview.Print(args[1])
	} else {
		err = preview.Run(filepath.Join(paths.DeckHome(), "preview-target"))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runHook() {
	if err := events.RunHook(filepath.Join(paths.DeckHome(), "events"), os.Stdin, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func login(args []string) error {
	wait := len(args) > 0 && args[0] == "--wait"
	if wait {
		args = args[1:]
	}
	if len(args) != 1 {
		return fmt.Errorf("usage: deckhand login <account>")
	}
	err := loginAccount(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if wait {
		fmt.Print("\nPress Enter to go back to the list.")
		fmt.Scanln()
		return nil
	}
	return err
}

func loginAccount(name string) error {
	dir, err := accounts.Prepare(paths.DeckHome(), paths.ClaudeHome(), name)
	if err != nil {
		return err
	}
	cmd := exec.Command(paths.ClaudeCommand(), "auth", "login")
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	email := accounts.Email(filepath.Join(dir, ".claude.json"))
	if email == "" {
		return fmt.Errorf("%s has no login yet", name)
	}
	fmt.Printf("\n%s is logged in as %s.\n", name, email)
	return nil
}

func runLaunch() {
	if err := launch(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func setup() (tmux.Client, store.Config, string, error) {
	home := paths.DeckHome()
	cfg, err := store.LoadConfig(home)
	if err != nil {
		return tmux.Client{}, cfg, "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return tmux.Client{}, cfg, "", err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return tmux.Client{}, cfg, "", err
	}
	conf := filepath.Join(home, "tmux.conf")
	if err := tmux.WriteConf(conf, cfg); err != nil {
		return tmux.Client{}, cfg, "", err
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), events.HookSettings(exe), 0o644); err != nil {
		return tmux.Client{}, cfg, "", err
	}
	if err := ctl.WritePlugins(home, exe); err != nil {
		return tmux.Client{}, cfg, "", err
	}
	if cfg.Assistant {
		if err := ctl.WriteInstructions(filepath.Join(home, "assistant")); err != nil {
			return tmux.Client{}, cfg, "", err
		}
	}
	client := tmux.Client{Socket: paths.Socket(), Conf: conf}
	if _, err := client.Run("list-sessions"); err == nil {
		if _, err := client.Run("source-file", conf); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
	}
	return client, cfg, exe, nil
}

func reload() error {
	client, _, exe, err := setup()
	if err != nil {
		return err
	}
	if !client.HasLayout() {
		return fmt.Errorf("deckhand is not running")
	}
	return client.Reload(exe, "")
}

func launch() error {
	if os.Getenv("TMUX") != "" {
		cfg, _ := store.LoadConfig(paths.DeckHome())
		return fmt.Errorf("deckhand uses %s as its key and cannot run inside tmux. Run it in a plain terminal, or set \"prefix\" in %s.", cfg.Prefix, filepath.Join(paths.DeckHome(), "config.json"))
	}
	client, cfg, exe, err := setup()
	if err != nil {
		return err
	}
	if !client.HasLayout() {
		if err := client.EnsureLayout(exe, cfg.ListWidth); err != nil {
			return err
		}
	} else {
		if err := client.SetListWidth(cfg.ListWidth); err != nil {
			return err
		}
		if build := buildID(); build != "" {
			if err := client.Reload(exe, build); err != nil {
				fmt.Fprintln(os.Stderr, "warning:", err)
			}
		}
	}
	bin, err := exec.LookPath("tmux")
	if err != nil {
		return err
	}
	return syscall.Exec(bin, []string{"tmux", "-L", client.Socket, "-f", client.Conf, "attach", "-t", tmux.DeckSession}, os.Environ())
}
