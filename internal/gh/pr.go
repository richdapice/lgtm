package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// RepoNWO is "owner/name" for the repo in cwd, as gh resolves it from the
// remote. Everything in prism's API is keyed by owner+repo, so this is the
// bridge from "I'm in a checkout" to those calls.
func (c Client) RepoNWO(ctx context.Context) (owner, name string, err error) {
	out, err := c.run(ctx, nil, "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	if err != nil {
		return "", "", err
	}
	nwo := strings.TrimSpace(string(out))
	i := strings.IndexByte(nwo, '/')
	if i <= 0 {
		return "", "", fmt.Errorf("gh: unexpected nameWithOwner %q", nwo)
	}
	return nwo[:i], nwo[i+1:], nil
}

func (c Client) DefaultBranch(ctx context.Context) (string, error) {
	out, err := c.run(ctx, nil, "repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// FindPR returns the open PR for a head branch, if one exists. ok=false with a
// nil error means "none yet".
func (c Client) FindPR(ctx context.Context, head string) (number int, url string, ok bool, err error) {
	out, err := c.run(ctx, nil, "pr", "list", "--head", head, "--state", "open", "--json", "number,url", "--limit", "1")
	if err != nil {
		return 0, "", false, err
	}
	var prs []struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return 0, "", false, fmt.Errorf("gh pr list: %w", err)
	}
	if len(prs) == 0 {
		return 0, "", false, nil
	}
	return prs[0].Number, prs[0].URL, true, nil
}

// CreatePR opens the PR. The body goes on stdin via --body-file - so a long
// body with backticks and quotes never touches a shell or ARG_MAX. gh prints
// the new PR's URL; the number is the last path segment.
func (c Client) CreatePR(ctx context.Context, base, head, title, body string, draft bool) (number int, url string, err error) {
	args := []string{"pr", "create", "--base", base, "--head", head, "--title", title, "--body-file", "-"}
	if draft {
		args = append(args, "--draft")
	}
	out, err := c.run(ctx, []byte(body), args...)
	if err != nil {
		return 0, "", err
	}
	url = lastNonEmptyLine(string(out))
	seg := url[strings.LastIndexByte(url, '/')+1:]
	number, err = strconv.Atoi(seg)
	if err != nil {
		return 0, url, fmt.Errorf("gh pr create: could not read PR number from %q", url)
	}
	return number, url, nil
}

// Checks summarizes CI for a PR using the statusCheckRollup prism already
// models, so there is one parser for check state, not two.
func (c Client) Checks(ctx context.Context, owner, repo string, number int) (pass, fail, pending int, err error) {
	d, err := c.GetPR(ctx, owner, repo, number)
	if err != nil {
		return 0, 0, 0, err
	}
	pass, fail, pending = d.ChecksSummary()
	return pass, fail, pending, nil
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
