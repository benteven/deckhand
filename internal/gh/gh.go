package gh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type check struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Workflow   string `json:"workflowName"`
	StartedAt  string `json:"startedAt"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

func State(url string) (state, checks string, err error) {
	if !strings.HasPrefix(url, "https://") {
		return "", "", fmt.Errorf("not a PR url: %q", url)
	}
	var v struct {
		State   string  `json:"state"`
		IsDraft bool    `json:"isDraft"`
		Checks  []check `json:"statusCheckRollup"`
	}
	if err := ghJSON(&v, "pr", "view", url, "--json", "state,isDraft,statusCheckRollup"); err != nil {
		return "", "", err
	}
	switch {
	case v.State == "OPEN" && v.IsDraft:
		return "draft", rollup(v.Checks), nil
	case v.State == "OPEN":
		return "open", rollup(v.Checks), nil
	case v.State == "MERGED":
		return "merged", "", nil
	case v.State == "CLOSED":
		return "closed", "", nil
	}
	return "", "", fmt.Errorf("unknown PR state %q", v.State)
}

type PR struct {
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Base    string `json:"baseRefName"`
	Head    string `json:"headRefName"`
	HeadOid string `json:"headRefOid"`
	Fork    bool   `json:"isCrossRepository"`
}

const prFields = "number,url,title,baseRefName,headRefName,headRefOid,isCrossRepository"

func Info(url string) (PR, error) {
	if !strings.HasPrefix(url, "https://") {
		return PR{}, fmt.Errorf("not a PR url: %q", url)
	}
	var v PR
	err := ghJSON(&v, "pr", "view", url, "--json", prFields)
	return v, err
}

func OpenPRs(repo string) ([]PR, error) {
	var v []PR
	err := ghJSON(&v, "pr", "list", "--repo", repo, "--state", "open", "--limit", "200", "--json", prFields)
	return v, err
}

func DefaultBranch(repo string) (string, error) {
	var v struct {
		Ref struct {
			Name string `json:"name"`
		} `json:"defaultBranchRef"`
	}
	err := ghJSON(&v, "repo", "view", repo, "--json", "defaultBranchRef")
	return v.Ref.Name, err
}

func ghJSON(v any, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return errors.New(strings.TrimSpace(string(exit.Stderr)))
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(out, v)
}

func rollup(checks []check) string {
	if len(checks) == 0 {
		return ""
	}
	latest := map[string]check{}
	for _, c := range checks {
		key := c.Workflow + "/" + c.Name + c.Context
		if prev, ok := latest[key]; !ok || c.StartedAt > prev.StartedAt {
			latest[key] = c
		}
	}
	result := "pass"
	for _, c := range latest {
		switch c.Conclusion {
		case "SUCCESS", "NEUTRAL", "SKIPPED", "CANCELLED":
			continue
		}
		switch {
		case c.State == "FAILURE" || c.State == "ERROR", c.Status == "COMPLETED":
			return "fail"
		case c.State == "PENDING" || c.State == "EXPECTED" || (c.State == "" && c.Status != "COMPLETED"):
			result = "pending"
		}
	}
	return result
}
