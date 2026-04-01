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
	"time"
)

// ScreenshotPath holds the branch-relative path and display name for an uploaded screenshot.
type ScreenshotPath struct {
	BranchPath string // e.g. "pr-123/20260401-120000-screenshot.png"
	FileName   string // e.g. "screenshot.png"
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

// PushScreenshots uploads files to the _screenshots branch via the Git Data API.
func (c *GitDataClient) PushScreenshots(repo *Repo, prNumber int, files []string) ([]ScreenshotPath, error) {
	prefix := fmt.Sprintf("repos/%s/%s", repo.Owner, repo.Name)
	timestamp := time.Now().UTC().Format("20060102-150405")

	// 1. Check if _screenshots branch exists
	parentCommitSHA := ""
	baseTreeSHA := ""

	var refResp struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := c.get(fmt.Sprintf("%s/git/ref/heads/_screenshots", prefix), &refResp)
	if err == nil {
		parentCommitSHA = refResp.Object.SHA

		var commitResp struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		}
		if err := c.get(fmt.Sprintf("%s/git/commits/%s", prefix, parentCommitSHA), &commitResp); err != nil {
			return nil, fmt.Errorf("get commit: %w", err)
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
			return nil, fmt.Errorf("read %s: %w", f, err)
		}

		blobReq := map[string]string{
			"content":  base64.StdEncoding.EncodeToString(data),
			"encoding": "base64",
		}
		var blobResp struct {
			SHA string `json:"sha"`
		}
		if err := c.post(fmt.Sprintf("%s/git/blobs", prefix), blobReq, &blobResp); err != nil {
			return nil, fmt.Errorf("create blob for %s: %w", f, err)
		}

		fileName := filepath.Base(f)
		branchPath := fmt.Sprintf("pr-%d/%s-%s", prNumber, timestamp, fileName)
		entries = append(entries, treeEntry{
			Path: branchPath,
			Mode: "100644",
			Type: "blob",
			SHA:  blobResp.SHA,
		})
		paths = append(paths, ScreenshotPath{
			BranchPath: branchPath,
			FileName:   fileName,
		})
	}

	// 3. Create tree
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
		return nil, fmt.Errorf("create tree: %w", err)
	}

	// 4. Create commit
	commitReq := map[string]interface{}{
		"message": fmt.Sprintf("screenshots for PR #%d", prNumber),
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
		return nil, fmt.Errorf("create commit: %w", err)
	}

	// 5. Create or update ref
	if parentCommitSHA != "" {
		refReq := map[string]interface{}{
			"sha":   commitResp.SHA,
			"force": false,
		}
		if err := c.patch(fmt.Sprintf("%s/git/refs/heads/_screenshots", prefix), refReq); err != nil {
			return nil, fmt.Errorf("update ref: %w", err)
		}
	} else {
		refReq := map[string]string{
			"ref": "refs/heads/_screenshots",
			"sha": commitResp.SHA,
		}
		if err := c.postNoResponse(fmt.Sprintf("%s/git/refs", prefix), refReq); err != nil {
			return nil, fmt.Errorf("create ref: %w", err)
		}
	}

	return paths, nil
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
