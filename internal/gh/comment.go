package gh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

// commentMarker identifies the gh-attach upsert comment on a PR/issue.
const commentMarker = "<!-- gh-attach -->"

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

// formatComment builds the full markdown body for an attachment comment.
// It is the marker + heading prefix followed by a single section, and is
// only used when no existing comment is being upserted.
func formatComment(repo *Repo, paths []AttachmentPath, commitSHA, title string) string {
	return commentMarker + "\n### Attachments\n" + FormatSection(repo, paths, commitSHA, title)
}

// imageExtensions is the set of file extensions FormatSection renders
// as inline image embeds (`![alt](url)`). Any other extension is
// rendered as a plain link (`[name](url)`), which is the correct
// shape for PDFs, logs, text files, archives, etc. — trying to use
// an image embed for those produces a broken-image icon in the
// GitHub markdown renderer, Slack, and email clients.
//
// Extension-based detection (rather than MIME sniffing of the file
// bytes) keeps the decision predictable and local to the rendering
// layer: FormatSection only has the basename to work with, not the
// file itself, and the caller's intent matches the extension they
// chose.
var imageExtensions = map[string]struct{}{
	".png":  {},
	".jpg":  {},
	".jpeg": {},
	".gif":  {},
	".webp": {},
	".svg":  {},
	".bmp":  {},
	".ico":  {},
	".apng": {},
	".avif": {},
	".heic": {},
	".heif": {},
}

// isImage reports whether `name` has an extension FormatSection
// should render as an inline image embed. Match is case-insensitive
// so `IMG_1234.JPG` and `foo.Png` both count as images.
func isImage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := imageExtensions[ext]
	return ok
}

// IsImage reports whether name carries an extension that FormatSection
// renders as an inline image embed rather than a click-through link.
// Callers outside this package use it to keep an upload inline — e.g. a
// GIF whose name lacks such an extension would render only as a link.
func IsImage(name string) bool { return isImage(name) }

// FormatSection builds the markdown section for an attachment
// upload, without the marker or heading. Used for both default
// stdout output from the CLI and the appended section in an
// upserted PR/issue comment.
//
// Shape depends on the mix of file types:
//   - Pure image upload: the existing 1- or 2-column markdown table
//     with `![name](url)` cells. Two columns for multiple images so
//     users get side-by-side previews (before/after screenshots).
//   - Pure non-image upload: a single-column table with `[name](url)`
//     cells, so PDFs, logs, archives, etc. produce clickable links
//     instead of broken image embeds.
//   - Mixed upload: images come first in a 1- or 2-column image
//     table, followed by non-images in a single-column link table.
//     Keeps the side-by-side preview for images without wedging
//     plain links into the same row.
func FormatSection(repo *Repo, paths []AttachmentPath, commitSHA, title string) string {
	var b strings.Builder

	if title != "" {
		fmt.Fprintf(&b, "\n**%s**\n\n", title)
	} else {
		b.WriteString("\n")
	}

	type entry struct {
		name string
		url  string
	}
	var images, files []entry
	for _, p := range paths {
		// URL-encoding of special characters in the filename (spaces,
		// `#`, `?`, non-ASCII) is handled inside EmbedURL via
		// url.PathEscape, so the href is always browser-safe while
		// the display name (alt text) stays raw.
		e := entry{name: p.Path, url: EmbedURL(repo, commitSHA, p.Path)}
		if isImage(p.Path) {
			images = append(images, e)
		} else {
			files = append(files, e)
		}
	}

	// Render images as the existing 1- or 2-column image table.
	if len(images) > 0 {
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
	}

	// Render non-images as a single-column table of plain links. A
	// table (rather than a bullet list) keeps the visual weight
	// consistent with the image table above it, and preserves the
	// "file name header + content cell" structure so the two
	// sections read as a unit when they appear together.
	//
	// fmt.Fprintf on a *strings.Builder is the idiomatic form for
	// infallible writes — strings.Builder.Write is documented to
	// never fail, so there's nothing useful to do with the error.
	if len(files) > 0 {
		for _, f := range files {
			fmt.Fprintf(&b, "| %s |\n", f.name)
			b.WriteString("|---|\n")
			fmt.Fprintf(&b, "| [%s](%s) |\n\n", f.name, f.url)
		}
	}

	return b.String()
}

// UpsertComment creates or updates the attachment comment on a PR.
func (c *CommentClient) UpsertComment(repo *Repo, prNumber int, paths []AttachmentPath, commitSHA, title string) (string, error) {
	prefix := fmt.Sprintf("repos/%s/%s", repo.Owner, repo.Name)

	existingID, existingBody, existingURL, err := c.findMarkerComment(prefix, prNumber)
	if err != nil {
		return "", err
	}

	if existingID != 0 {
		updatedBody := existingBody + "\n---\n" + FormatSection(repo, paths, commitSHA, title)
		url, err := c.updateComment(prefix, existingID, updatedBody)
		if err != nil {
			return "", err
		}
		if url == "" {
			url = existingURL
		}
		return url, nil
	}

	fullBody := formatComment(repo, paths, commitSHA, title)
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

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

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

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create comment: %d — %s", resp.StatusCode, respBody)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
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

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("update comment: %d — %s", resp.StatusCode, respBody)
	}

	var result struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.HTMLURL, nil
}
