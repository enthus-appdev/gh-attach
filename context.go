package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Repo identifies a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

// parseRepoFromRemote extracts owner/name from a git remote URL.
func parseRepoFromRemote(remote string) (*Repo, error) {
	// SSH: git@github.com:owner/repo.git
	if strings.HasPrefix(remote, "git@") {
		parts := strings.SplitN(remote, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("cannot parse SSH remote: %s", remote)
		}
		return parseOwnerRepo(parts[1])
	}

	// HTTPS: https://github.com/owner/repo.git
	if strings.Contains(remote, "github.com/") {
		idx := strings.Index(remote, "github.com/")
		return parseOwnerRepo(remote[idx+len("github.com/"):])
	}

	return nil, fmt.Errorf("cannot parse remote URL: %s", remote)
}

func parseOwnerRepo(path string) (*Repo, error) {
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("cannot parse owner/repo from: %s", path)
	}
	return &Repo{Owner: parts[0], Name: parts[1]}, nil
}

// resolveRepo detects the GitHub repo from the current git remote.
func resolveRepo() (*Repo, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return nil, fmt.Errorf("git remote get-url origin: %w", err)
	}
	return parseRepoFromRemote(strings.TrimSpace(string(out)))
}

// resolvePR auto-detects the PR number for the current branch using gh.
func resolvePR(repo *Repo) (int, error) {
	out, err := exec.Command("gh", "pr", "view", "--json", "number", "--repo", repo.Owner+"/"+repo.Name).Output()
	if err != nil {
		return 0, fmt.Errorf("no PR found for current branch (pass an explicit PR or issue number): %w", err)
	}
	var result struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0, fmt.Errorf("parse PR response: %w", err)
	}
	if result.Number == 0 {
		return 0, fmt.Errorf("no PR found for current branch")
	}
	return result.Number, nil
}
