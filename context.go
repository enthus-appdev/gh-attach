package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
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

// resolveRepo returns the target GitHub repo. If override is non-empty, it
// is parsed as either "owner/name" or a full SSH/HTTPS GitHub URL and used
// directly. Otherwise, the repo is detected from the current git clone's
// origin remote.
func resolveRepo(override string) (*Repo, error) {
	if override != "" {
		// Accept full SSH/HTTPS URLs as a convenience — users often have a
		// URL from a browser address bar or `git clone` command handy. Use
		// prefix checks (not `Contains(..., "github.com")`) so valid plain
		// slugs like `foo/github.com-bar` aren't mis-routed as URLs.
		if strings.HasPrefix(override, "git@") ||
			strings.HasPrefix(override, "http://") ||
			strings.HasPrefix(override, "https://") ||
			strings.HasPrefix(override, "github.com/") {
			return parseRepoFromRemote(override)
		}
		return parseOwnerRepo(override)
	}
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return nil, fmt.Errorf("git remote get-url origin: %w", err)
	}
	return parseRepoFromRemote(strings.TrimSpace(string(out)))
}

// keyCharset is the set of characters allowed in an ad-hoc --key value.
// It is a safe subset of git's ref-name rules: alphanumerics, underscore,
// dash, dot, and forward slash for hierarchical keys like "docs/arch".
var keyCharset = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._/-]*$`)

// keyPureNumeric matches keys that are only digits. These are rejected
// because they would be visually confusable with PR/issue numbers stored
// under refs/uploads/issues/<N>.
var keyPureNumeric = regexp.MustCompile(`^[0-9]+$`)

// validateKey returns an error if key is not a legal --key value. It
// enforces a strict safe subset of git ref name rules so that every
// accepted key can be used as-is in refs/uploads/misc/<key>.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("--key cannot be empty")
	}
	if len(key) > 100 {
		return fmt.Errorf("--key must be 100 characters or fewer (got %d)", len(key))
	}
	if keyPureNumeric.MatchString(key) {
		return fmt.Errorf("--key cannot be purely numeric (confusable with a PR/issue number — try a descriptive name like %q)", "k"+key)
	}
	if !keyCharset.MatchString(key) {
		return fmt.Errorf("--key %q contains invalid characters (allowed: letters, digits, '.', '_', '-', '/'; must start with a letter, digit, or underscore)", key)
	}
	if strings.Contains(key, "..") {
		return fmt.Errorf("--key cannot contain '..' (git ref name rule)")
	}
	if strings.Contains(key, "//") {
		return fmt.Errorf("--key cannot contain '//'")
	}
	if strings.HasSuffix(key, "/") {
		return fmt.Errorf("--key cannot end with '/'")
	}
	if strings.HasSuffix(key, ".lock") {
		return fmt.Errorf("--key cannot end with '.lock' (git ref name rule)")
	}
	return nil
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
