package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ScreenshotPath holds the tree-relative path of an uploaded screenshot.
// Stored under refs/uploads/issues/<N>, the tree contains files at top level
// keyed by basename, so Path doubles as both the tree path and the display name.
type ScreenshotPath struct {
	Path string // e.g. "screenshot.png"
}

// GitDataClient interacts with the GitHub Git Data API.
type GitDataClient struct {
	BaseURL string // e.g. "https://api.github.com" or test server URL
	Token   string
}

// NewGitDataClient creates a client using the gh auth token.
func NewGitDataClient() (*GitDataClient, error) {
	token, err := ghAuthToken()
	if err != nil {
		return nil, err
	}
	return &GitDataClient{
		BaseURL: "https://api.github.com",
		Token:   token,
	}, nil
}

// PushScreenshots uploads files via the Git Data API to refs/uploads/issues/<N>,
// a custom-namespace ref that bypasses branch protection / rulesets and is invisible
// in the Branches UI. Returns the per-upload basenames and the commit SHA they're
// reachable from. Embed URLs reference the commit SHA directly so they remain valid
// across subsequent uploads as long as the ref is not deleted.
func (c *GitDataClient) PushScreenshots(repo *Repo, prNumber int, files []string) ([]ScreenshotPath, string, error) {
	prefix := fmt.Sprintf("repos/%s/%s", repo.Owner, repo.Name)
	refSuffix := fmt.Sprintf("uploads/issues/%d", prNumber) // path under refs/

	// 0. Reject basename collisions before any API calls. Tree paths are basenames,
	// so two source files with the same basename would silently overwrite each other.
	seenBasename := make(map[string]string, len(files))
	for _, f := range files {
		base := filepath.Base(f)
		if other, exists := seenBasename[base]; exists {
			return nil, "", fmt.Errorf("duplicate basename %q: %s and %s would collide in the same upload — rename one of the files", base, other, f)
		}
		seenBasename[base] = f
	}

	// 1. Check if our upload ref already exists for this PR/issue
	parentCommitSHA := ""
	baseTreeSHA := ""

	var refResp struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := c.get(fmt.Sprintf("%s/git/ref/%s", prefix, refSuffix), &refResp)
	if err == nil {
		parentCommitSHA = refResp.Object.SHA

		var commitResp struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		}
		if err := c.get(fmt.Sprintf("%s/git/commits/%s", prefix, parentCommitSHA), &commitResp); err != nil {
			return nil, "", fmt.Errorf("get commit: %w", err)
		}
		baseTreeSHA = commitResp.Tree.SHA
	}

	// 2. Create blobs for each file
	type treeEntry struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	}
	var entries []treeEntry
	var paths []ScreenshotPath

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", f, err)
		}

		blobReq := map[string]string{
			"content":  base64.StdEncoding.EncodeToString(data),
			"encoding": "base64",
		}
		var blobResp struct {
			SHA string `json:"sha"`
		}
		if err := c.post(fmt.Sprintf("%s/git/blobs", prefix), blobReq, &blobResp); err != nil {
			return nil, "", fmt.Errorf("create blob for %s: %w", f, err)
		}

		fileName := filepath.Base(f)
		entries = append(entries, treeEntry{
			Path: fileName,
			Mode: "100644",
			Type: "blob",
			SHA:  blobResp.SHA,
		})
		paths = append(paths, ScreenshotPath{Path: fileName})
	}

	// 3. Create tree (fast-forward by basing on the previous tree if it exists)
	treeReq := map[string]interface{}{
		"tree": entries,
	}
	if baseTreeSHA != "" {
		treeReq["base_tree"] = baseTreeSHA
	}
	var treeResp struct {
		SHA string `json:"sha"`
	}
	if err := c.post(fmt.Sprintf("%s/git/trees", prefix), treeReq, &treeResp); err != nil {
		return nil, "", fmt.Errorf("create tree: %w", err)
	}

	// 4. Create commit (fast-forward chain so older blobs stay reachable)
	commitReq := map[string]interface{}{
		"message": fmt.Sprintf("screenshots for #%d", prNumber),
		"tree":    treeResp.SHA,
	}
	if parentCommitSHA != "" {
		commitReq["parents"] = []string{parentCommitSHA}
	} else {
		commitReq["parents"] = []string{}
	}
	var commitResp struct {
		SHA string `json:"sha"`
	}
	if err := c.post(fmt.Sprintf("%s/git/commits", prefix), commitReq, &commitResp); err != nil {
		return nil, "", fmt.Errorf("create commit: %w", err)
	}

	// 5. Create or fast-forward the ref
	if parentCommitSHA != "" {
		refReq := map[string]interface{}{
			"sha":   commitResp.SHA,
			"force": false,
		}
		if err := c.patch(fmt.Sprintf("%s/git/refs/%s", prefix, refSuffix), refReq); err != nil {
			return nil, "", fmt.Errorf("update ref: %w", err)
		}
	} else {
		refReq := map[string]string{
			"ref": fmt.Sprintf("refs/%s", refSuffix),
			"sha": commitResp.SHA,
		}
		if err := c.postNoResponse(fmt.Sprintf("%s/git/refs", prefix), refReq); err != nil {
			return nil, "", fmt.Errorf("create ref: %w", err)
		}
	}

	return paths, commitResp.SHA, nil
}

func (c *GitDataClient) get(path string, result interface{}) error {
	req, err := http.NewRequest("GET", c.BaseURL+"/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not found")
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: %d — %s", path, resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *GitDataClient) post(path string, payload interface{}, result interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", c.BaseURL+"/"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: %d — %s", path, resp.StatusCode, respBody)
	}
	return json.NewDecoder(resp.Body).Decode(result)
}

func (c *GitDataClient) postNoResponse(path string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", c.BaseURL+"/"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: %d — %s", path, resp.StatusCode, respBody)
	}
	return nil
}

func (c *GitDataClient) patch(path string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PATCH", c.BaseURL+"/"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PATCH %s: %d — %s", path, resp.StatusCode, respBody)
	}
	return nil
}

// ghAuthToken retrieves the GitHub auth token from gh CLI.
func ghAuthToken() (string, error) {
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w (is gh authenticated?)", err)
	}
	return strings.TrimSpace(string(out)), nil
}
