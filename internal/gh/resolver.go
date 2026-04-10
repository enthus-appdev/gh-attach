package gh

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// execCommand is a package-level indirection over exec.Command so tests
// can replace it with a stub that returns canned git / gh CLI output
// without actually shelling out. Production callers see the real
// exec.Command behaviour unchanged.
var execCommand = exec.Command

// ResolveRepo returns the target GitHub repo. If override is non-empty,
// it is parsed as either "owner/name" or a full SSH/HTTPS GitHub URL and
// used directly. Otherwise, the repo is detected from the current git
// clone's origin remote.
func ResolveRepo(override string) (*Repo, error) {
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
	out, err := execCommand("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return nil, fmt.Errorf("git remote get-url origin: %w", err)
	}
	return parseRepoFromRemote(strings.TrimSpace(string(out)))
}

// ResolvePR auto-detects the PR number for the current branch using gh.
// It only makes sense inside a clone of the target repo; the CLI layer
// guards against using it together with --repo or --key.
func ResolvePR(repo *Repo) (int, error) {
	out, err := execCommand("gh", "pr", "view", "--json", "number", "--repo", repo.Owner+"/"+repo.Name).Output()
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

// ghAuthToken retrieves the GitHub auth token from the gh CLI. Used by
// both NewGitDataClient and NewCommentClient to avoid duplicating the
// exec call.
func ghAuthToken() (string, error) {
	out, err := execCommand("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w (is gh authenticated?)", err)
	}
	return strings.TrimSpace(string(out)), nil
}
