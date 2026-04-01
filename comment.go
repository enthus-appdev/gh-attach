package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const commentMarker = "<!-- pr-screenshots -->"

// CommentClient interacts with the GitHub Issues API for PR comments.
type CommentClient struct {
	BaseURL string
	Token   string
}

// NewCommentClient creates a client using the gh auth token.
func NewCommentClient() (*CommentClient, error) {
	token, err := ghAuthToken()
	if err != nil {
		return nil, err
	}
	return &CommentClient{
		BaseURL: "https://api.github.com",
		Token:   token,
	}, nil
}

// formatComment builds the full markdown body for a screenshot comment (including marker and header).
func formatComment(repo *Repo, paths []ScreenshotPath, title string) string {
	var b strings.Builder
	b.WriteString(commentMarker + "\n### Screenshots\n")

	if title != "" {
		b.WriteString(fmt.Sprintf("\n**%s**\n\n", title))
	} else {
		b.WriteString("\n")
	}

	type imageEntry struct {
		name string
		url  string
	}
	var images []imageEntry
	for _, p := range paths {
		url := fmt.Sprintf("https://github.com/%s/%s/blob/_screenshots/%s?raw=true", repo.Owner, repo.Name, p.BranchPath)
		images = append(images, imageEntry{name: p.FileName, url: url})
	}

	cols := 2
	if len(images) == 1 {
		cols = 1
	}

	for i := 0; i < len(images); i += cols {
		end := i + cols
		if end > len(images) {
			end = len(images)
		}
		row := images[i:end]

		headers := make([]string, len(row))
		for j, img := range row {
			headers[j] = img.name
		}
		b.WriteString("| " + strings.Join(headers, " | ") + " |\n")

		seps := make([]string, len(row))
		for j := range row {
			seps[j] = "---"
		}
		b.WriteString("|" + strings.Join(seps, "|") + "|\n")

		cells := make([]string, len(row))
		for j, img := range row {
			cells[j] = fmt.Sprintf("![%s](%s)", img.name, img.url)
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n\n")
	}

	return b.String()
}

// formatSection builds just the new section (without marker/header) for appending.
func formatSection(repo *Repo, paths []ScreenshotPath, title string) string {
	var b strings.Builder

	if title != "" {
		b.WriteString(fmt.Sprintf("\n**%s**\n\n", title))
	} else {
		b.WriteString("\n")
	}

	type imageEntry struct {
		name string
		url  string
	}
	var images []imageEntry
	for _, p := range paths {
		url := fmt.Sprintf("https://github.com/%s/%s/blob/_screenshots/%s?raw=true", repo.Owner, repo.Name, p.BranchPath)
		images = append(images, imageEntry{name: p.FileName, url: url})
	}

	cols := 2
	if len(images) == 1 {
		cols = 1
	}

	for i := 0; i < len(images); i += cols {
		end := i + cols
		if end > len(images) {
			end = len(images)
		}
		row := images[i:end]

		headers := make([]string, len(row))
		for j, img := range row {
			headers[j] = img.name
		}
		b.WriteString("| " + strings.Join(headers, " | ") + " |\n")

		seps := make([]string, len(row))
		for j := range row {
			seps[j] = "---"
		}
		b.WriteString("|" + strings.Join(seps, "|") + "|\n")

		cells := make([]string, len(row))
		for j, img := range row {
			cells[j] = fmt.Sprintf("![%s](%s)", img.name, img.url)
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n\n")
	}

	return b.String()
}

// UpsertComment creates or updates the screenshot comment on a PR.
func (c *CommentClient) UpsertComment(repo *Repo, prNumber int, paths []ScreenshotPath, title string) (string, error) {
	prefix := fmt.Sprintf("repos/%s/%s", repo.Owner, repo.Name)

	existingID, existingBody, existingURL, err := c.findMarkerComment(prefix, prNumber)
	if err != nil {
		return "", err
	}

	if existingID != 0 {
		updatedBody := existingBody + "\n---\n" + formatSection(repo, paths, title)
		url, err := c.updateComment(prefix, existingID, updatedBody)
		if err != nil {
			return "", err
		}
		if url == "" {
			url = existingURL
		}
		return url, nil
	}

	fullBody := formatComment(repo, paths, title)
	return c.createComment(prefix, prNumber, fullBody)
}

func (c *CommentClient) findMarkerComment(prefix string, prNumber int) (int, string, string, error) {
	url := fmt.Sprintf("%s/%s/issues/%d/comments", c.BaseURL, prefix, prNumber)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return 0, "", "", fmt.Errorf("list comments: %d — %s", resp.StatusCode, body)
	}

	var comments []struct {
		ID      int    `json:"id"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
		return 0, "", "", err
	}

	for _, comment := range comments {
		if strings.Contains(comment.Body, commentMarker) {
			return comment.ID, comment.Body, comment.HTMLURL, nil
		}
	}
	return 0, "", "", nil
}

func (c *CommentClient) createComment(prefix string, prNumber int, body string) (string, error) {
	url := fmt.Sprintf("%s/%s/issues/%d/comments", c.BaseURL, prefix, prNumber)
	payload, _ := json.Marshal(map[string]string{"body": body})

	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create comment: %d — %s", resp.StatusCode, respBody)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.HTMLURL, nil
}

func (c *CommentClient) updateComment(prefix string, commentID int, body string) (string, error) {
	url := fmt.Sprintf("%s/%s/issues/comments/%d", c.BaseURL, prefix, commentID)
	payload, _ := json.Marshal(map[string]string{"body": body})

	req, err := http.NewRequest("PATCH", url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("update comment: %d — %s", resp.StatusCode, respBody)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.HTMLURL, nil
}
