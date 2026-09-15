// Package gh wraps the gh CLI for all GitHub data access. Using gh (rather
// than a Go client library) reuses its keychain auth, which matters on
// SSO-enforced work orgs.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type Client struct{}

// run executes gh and returns stdout, folding stderr into the error.
func (Client) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return errb.Bytes(), fmt.Errorf("gh %s: %s", args[0], msg)
	}
	return out.Bytes(), nil
}

func (c Client) CurrentUser(ctx context.Context) (string, error) {
	out, err := c.run(ctx, nil, "api", "user", "--jq", ".login")
	return strings.TrimSpace(string(out)), err
}

func (c Client) ListOrgs(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, nil, "api", "user/orgs", "--paginate", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	var orgs []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			orgs = append(orgs, l)
		}
	}
	return orgs, nil
}

// ListRepos returns the owner's most recently pushed repos with open-PR
// counts, via one GraphQL call.
func (c Client) ListRepos(ctx context.Context, owner string) ([]Repo, error) {
	query := `query($owner: String!) {
	  repositoryOwner(login: $owner) {
	    repositories(first: 100, orderBy: {field: PUSHED_AT, direction: DESC}) {
	      nodes { name pullRequests(states: OPEN) { totalCount } }
	    }
	  }
	}`
	out, err := c.run(ctx, nil, "api", "graphql", "-f", "query="+query, "-F", "owner="+owner)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			RepositoryOwner struct {
				Repositories struct {
					Nodes []struct {
						Name         string `json:"name"`
						PullRequests struct {
							TotalCount int `json:"totalCount"`
						} `json:"pullRequests"`
					} `json:"nodes"`
				} `json:"repositories"`
			} `json:"repositoryOwner"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("parsing repo list: %w", err)
	}
	var repos []Repo
	for _, n := range resp.Data.RepositoryOwner.Repositories.Nodes {
		repos = append(repos, Repo{Owner: owner, Name: n.Name, OpenPRs: n.PullRequests.TotalCount})
	}
	return repos, nil
}

func (c Client) ListPRs(ctx context.Context, owner, repo string) ([]PR, error) {
	out, err := c.run(ctx, nil, "pr", "list", "-R", owner+"/"+repo,
		"--json", "number,title,author,isDraft,createdAt,reviewDecision")
	if err != nil {
		return nil, err
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parsing PR list: %w", err)
	}
	return prs, nil
}

func (c Client) GetPR(ctx context.Context, owner, repo string, number int) (PRDetail, error) {
	out, err := c.run(ctx, nil, "pr", "view", fmt.Sprint(number), "-R", owner+"/"+repo,
		"--json", "number,title,body,author,baseRefName,headRefName,headRefOid,isDraft,additions,deletions,files,statusCheckRollup,url")
	if err != nil {
		return PRDetail{}, err
	}
	var d PRDetail
	if err := json.Unmarshal(out, &d); err != nil {
		return PRDetail{}, fmt.Errorf("parsing PR detail: %w", err)
	}
	return d, nil
}

func (c Client) GetDiff(ctx context.Context, owner, repo string, number int) (string, error) {
	out, err := c.run(ctx, nil, "pr", "diff", fmt.Sprint(number), "-R", owner+"/"+repo)
	return string(out), err
}

// GetFileAtRef fetches a file's contents at a specific commit, for AI context.
func (c Client) GetFileAtRef(ctx context.Context, owner, repo, path, ref string) (string, error) {
	out, err := c.run(ctx, nil, "api",
		fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", owner, repo, path, ref),
		"-H", "Accept: application/vnd.github.raw+json")
	return string(out), err
}

// HeadSHA re-fetches just the PR's current head commit (staleness check).
func (c Client) HeadSHA(ctx context.Context, owner, repo string, number int) (string, error) {
	out, err := c.run(ctx, nil, "pr", "view", fmt.Sprint(number), "-R", owner+"/"+repo,
		"--json", "headRefOid", "--jq", ".headRefOid")
	return strings.TrimSpace(string(out)), err
}
