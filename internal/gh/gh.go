package gh

import (
	"context"
	"encoding/json"
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", url, "--json", "state,isDraft,statusCheckRollup")
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	var v struct {
		State   string  `json:"state"`
		IsDraft bool    `json:"isDraft"`
		Checks  []check `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
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

func Info(url string) (number int, title, base, head string, err error) {
	if !strings.HasPrefix(url, "https://") {
		return 0, "", "", "", fmt.Errorf("not a PR url: %q", url)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", url, "--json", "number,title,baseRefName,headRefName")
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return 0, "", "", "", err
	}
	var v struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Base   string `json:"baseRefName"`
		Head   string `json:"headRefName"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return 0, "", "", "", err
	}
	return v.Number, v.Title, v.Base, v.Head, nil
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
