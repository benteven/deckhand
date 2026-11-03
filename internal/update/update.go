package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	repo       = "benteven/deckhand"
	tap        = "benteven/tap"
	checkEvery = 6 * time.Hour
)

var (
	latestURL  = "https://api.github.com/repos/" + repo + "/releases/latest"
	installURL = "https://raw.githubusercontent.com/" + repo + "/main/install.sh"
	client     = &http.Client{Timeout: 15 * time.Second}
)

type cache struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

func Latest(home string, now time.Time) string {
	path := filepath.Join(home, "update.json")
	var c cache
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &c)
	}
	if now.Sub(c.Checked) < checkEvery {
		return c.Latest
	}
	c.Checked = now
	if tag, err := fetchLatest(); err == nil {
		c.Latest = tag
	}
	if data, err := json.Marshal(c); err == nil {
		os.WriteFile(path, data, 0o644)
	}
	return c.Latest
}

func fetchLatest() (string, error) {
	body, err := get(latestURL)
	if err != nil {
		return "", err
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", err
	}
	return strings.TrimPrefix(rel.Tag, "v"), nil
}

func get(url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func Newer(current, latest string) bool {
	cur, ok := parse(current)
	if !ok {
		return false
	}
	next, ok := parse(latest)
	if !ok {
		return false
	}
	for i := range cur {
		if next[i] != cur[i] {
			return next[i] > cur[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func Install(exe, current string) (string, error) {
	if prefix := brewPrefix(exe); prefix != "" {
		brew := filepath.Join(prefix, "bin", "brew")
		for _, args := range [][]string{{"update", "--quiet"}, {"upgrade", "deckhand"}} {
			if out, err := exec.Command(brew, args...).CombinedOutput(); err != nil {
				if bytes.Contains(out, []byte("untrusted tap")) {
					return "", fmt.Errorf("Run brew trust %s, then U", tap)
				}
				return "", fmt.Errorf("brew %s: %v: %s", args[0], err, lastLine(out))
			}
		}
		return filepath.Join(prefix, "bin", "deckhand"), nil
	}
	if _, ok := parse(current); !ok {
		return exe, nil
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	script, err := get(installURL)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("sh")
	cmd.Stdin = bytes.NewReader(script)
	cmd.Env = append(os.Environ(), "DECKHAND_BIN_DIR="+filepath.Dir(real))
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("install: %v: %s", err, lastLine(out))
	}
	return exe, nil
}

func brewPrefix(exe string) string {
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return ""
	}
	if i := strings.Index(real, "/Cellar/"); i > 0 {
		return real[:i]
	}
	return ""
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return lines[len(lines)-1]
}
