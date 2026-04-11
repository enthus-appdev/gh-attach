package gh

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// httpClient is the shared HTTP client used by every GitDataClient and
// CommentClient request. A timeout is mandatory for a CLI tool — without
// it a stalled upload or an unresponsive GitHub edge could cause the
// process to block indefinitely (particularly painful in CI / scripts).
//
// 60s is the longest any normal gh-attach operation should take: the
// biggest blob upload is bounded by GitHub's per-blob size limit, and
// every other call (list refs, delete ref, upsert comment) is a small
// JSON request/response. Increase only if a legitimate operation starts
// getting killed.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// AttachmentPath holds the tree-relative path of an uploaded attachment.
// Stored under refs/uploads/<refPath>, the tree contains files at top level
// keyed by basename, so Path doubles as both the tree path and the display name.
type AttachmentPath struct {
	Path string // e.g. "screenshot.png"
}

// Attachment is a single file resolved via GetAttachments. It bundles
// the tree-relative path (always a basename in current uploads), the
// blob SHA for traceability, the declared size, and the raw decoded
// bytes. Content is loaded eagerly — suitable for screenshots and
// diagrams but not for multi-GB payloads. Add a streaming variant
// if that ever matters.
type Attachment struct {
	Path    string // basename under the tree root, e.g. "screenshot.png"
	SHA     string // blob SHA — stable handle for the content across pulls
	Size    int64  // declared size in bytes (matches len(Content) on success)
	Content []byte // raw decoded bytes
}

// EmbedURL returns the raw-blob GitHub URL that renders as an inline
// image when used in markdown, Slack, Discord, or pasted into a
// browser. The `path` component is URL-encoded via url.PathEscape so
// filenames containing spaces, `#`, `?`, or non-ASCII characters
// produce valid, clickable URLs. Callers pass the raw basename as
// `path`; nothing else in the URL is encoded.
//
// Centralizing this here means the CLI stderr URL list, the CLI JSON
// output, and FormatSection's markdown table all construct identical
// URLs. Drifting from each other has historically been a source of
// bugs (see the URL-encoding fix in #15's review cycle).
func EmbedURL(repo *Repo, commitSHA, path string) string {
	return fmt.Sprintf("https://github.com/%s/blob/%s/%s?raw=true",
		repo, commitSHA, url.PathEscape(path))
}

// RefEntry is a parsed view of a single upload ref returned by ListRefs.
// It carries the full git ref path plus the namespace-aware breakdown
// that the CLI layer uses to render either text columns or JSON.
//
// For refs/uploads/issues/<N>: Namespace="issue", Number=<N>, Target="#<N>"
// For refs/uploads/misc/<key>: Namespace="misc",  Key=<key>,  Target="misc/<key>"
// Anything else (future namespaces) reports Namespace="other" and Target
// is the part after refs/uploads/.
type RefEntry struct {
	Ref       string `json:"ref"`                // "refs/uploads/issues/42"
	SHA       string `json:"sha"`                // tip commit SHA
	Namespace string `json:"namespace"`          // "issue" | "misc" | "other"
	Target    string `json:"target"`             // "#42" | "misc/design-v2"
	Number    int    `json:"number,omitempty"`   // populated for namespace=="issue"
	Key       string `json:"key,omitempty"`      // populated for namespace=="misc"
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

// PushAttachments uploads files via the Git Data API to the ref at
// refs/<refPath>, a custom-namespace ref that bypasses branch protection /
// rulesets and is invisible in the Branches UI. Typical refPath values are
// "uploads/issues/<N>" for PR/issue uploads or "uploads/misc/<key>" for
// ad-hoc uploads. Returns the per-upload basenames and the commit SHA they're
// reachable from. Embed URLs reference the commit SHA directly so they remain
// valid across subsequent uploads as long as the ref is not deleted.
func (c *GitDataClient) PushAttachments(repo *Repo, refPath, commitMessage string, files []string) ([]AttachmentPath, string, error) {
	prefix := fmt.Sprintf("repos/%s/%s", repo.Owner, repo.Name)
	refSuffix := refPath // path under refs/

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

	// 1. Check if our upload ref already exists for this target
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
	var paths []AttachmentPath

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
		paths = append(paths, AttachmentPath{Path: fileName})
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
		"message": commitMessage,
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

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		// Return the sentinel (not a fresh fmt.Errorf) so callers can
		// use errors.Is(err, ErrNotFound) to distinguish 404 from
		// other failures. The original fresh-error version silently
		// broke that check for every caller.
		return errNotFound
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

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

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

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

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

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PATCH %s: %d — %s", path, resp.StatusCode, respBody)
	}
	return nil
}

func (c *GitDataClient) httpDelete(path string) error {
	req, err := http.NewRequest("DELETE", c.BaseURL+"/"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 300 {
		return nil
	}

	// Read the body once — we need it for both the not-found detection
	// and the fallback error message.
	respBody, _ := io.ReadAll(resp.Body)

	// Classic 404 — rare from the GitHub Git Data API for refs but
	// present historically and on some Enterprise instances.
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	// GitHub's Git Data API returns `422 Unprocessable Entity` with
	// {"message":"Reference does not exist"} when you DELETE a ref
	// that was never created. This is semantically "not found" but
	// with a non-404 status code. Detect the specific message so the
	// CLI layer can render a clean error instead of dumping the raw
	// 422 JSON body.
	if resp.StatusCode == http.StatusUnprocessableEntity &&
		bytes.Contains(respBody, []byte("Reference does not exist")) {
		return errNotFound
	}

	return fmt.Errorf("DELETE %s: %d — %s", path, resp.StatusCode, respBody)
}

// errNotFound is returned by httpDelete and ListRefs when the target
// ref or prefix doesn't exist. Callers can compare with errors.Is
// to render a friendly "not found" message at the CLI layer.
var errNotFound = fmt.Errorf("not found")

// ErrNotFound is the exported sentinel for callers that want to
// distinguish 404 responses from other errors.
var ErrNotFound = errNotFound

// ListRefs enumerates upload refs matching the given sub-prefix under
// refs/uploads/. Valid values for subPrefix are "" (all upload refs),
// "issues" (issue-scoped only), or "misc" (ad-hoc only). Each returned
// RefEntry has its Namespace/Target/Number/Key fields pre-populated
// so the CLI layer can render without re-parsing.
//
// Uses the GitHub matching-refs endpoint:
//
//	GET /repos/{owner}/{repo}/git/matching-refs/uploads[/{subPrefix}]
//
// An empty result is not an error — ListRefs returns nil, nil.
func (c *GitDataClient) ListRefs(repo *Repo, subPrefix string) ([]RefEntry, error) {
	apiPath := fmt.Sprintf("repos/%s/%s/git/matching-refs/uploads", repo.Owner, repo.Name)
	if subPrefix != "" {
		apiPath += "/" + subPrefix
	}

	// matching-refs returns an empty array (not 404) when nothing matches.
	var raw []struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.get(apiPath, &raw); err != nil {
		return nil, err
	}

	entries := make([]RefEntry, 0, len(raw))
	for _, r := range raw {
		entries = append(entries, parseRefEntry(r.Ref, r.Object.SHA))
	}
	return entries, nil
}

// parseRefEntry turns a raw git ref path into a namespace-tagged
// RefEntry. It is unexported so the CLI layer doesn't accidentally
// skip ListRefs and construct entries from raw strings.
func parseRefEntry(ref, sha string) RefEntry {
	// Expected shapes:
	//   refs/uploads/issues/<N>
	//   refs/uploads/misc/<key...>
	//   refs/uploads/<other>/<anything>
	entry := RefEntry{Ref: ref, SHA: sha, Namespace: "other"}

	const prefix = "refs/uploads/"
	if !strings.HasPrefix(ref, prefix) {
		entry.Target = ref
		return entry
	}
	rest := strings.TrimPrefix(ref, prefix)

	// Split off the namespace segment (first /-separated component).
	slash := strings.Index(rest, "/")
	if slash < 0 {
		// Single-segment rest, treat as "other" with that as target.
		entry.Target = rest
		return entry
	}
	namespace := rest[:slash]
	remainder := rest[slash+1:]

	switch namespace {
	case "issues":
		if n, err := strconv.Atoi(remainder); err == nil {
			entry.Namespace = "issue"
			entry.Number = n
			entry.Target = "#" + remainder
			return entry
		}
		// issues/<not-a-number> — shouldn't happen in practice but
		// don't crash; treat as "other" to surface it in listings.
		entry.Target = "issues/" + remainder
		return entry
	case "misc":
		entry.Namespace = "misc"
		entry.Key = remainder
		entry.Target = "misc/" + remainder
		return entry
	default:
		entry.Target = rest
		return entry
	}
}

// DeleteRef removes the ref at refs/<refPath>. Returns ErrNotFound if
// the ref doesn't exist so CLI callers can print a clean "not found"
// message without matching on error strings.
func (c *GitDataClient) DeleteRef(repo *Repo, refPath string) error {
	apiPath := fmt.Sprintf("repos/%s/%s/git/refs/%s", repo.Owner, repo.Name, refPath)
	return c.httpDelete(apiPath)
}

// GetAttachments walks the tree at refs/<refPath> and returns every
// blob entry with its raw decoded bytes plus the commit SHA the ref
// currently points at. The tip commit SHA is the caller's stable
// handle for building embed URLs that match what PushAttachments
// would have produced, so this is the exact inverse of the
// PushAttachments flow.
//
// The walk is:
//  1. GET git/ref/<refPath>       → commit SHA (404 → ErrNotFound)
//  2. GET git/commits/<commitSHA> → tree SHA
//  3. GET git/trees/<treeSHA>     → tree entries
//  4. For every "blob" entry:
//     GET git/blobs/<blobSHA>     → base64 content
//
// Non-blob entries (subtrees, submodules, symlinks) are silently
// skipped. Current uploads never produce those — PushAttachments
// only emits type=blob mode=100644 — but a future reader should
// not crash on a tree that happens to include one.
//
// Results are ordered by the tree entry order returned by the API,
// which is lexicographic for a single tree. Callers that need a
// specific order should sort the returned slice.
func (c *GitDataClient) GetAttachments(repo *Repo, refPath string) ([]Attachment, string, error) {
	prefix := fmt.Sprintf("repos/%s/%s", repo.Owner, repo.Name)

	// 1. Resolve the ref → commit SHA
	var refResp struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.get(fmt.Sprintf("%s/git/ref/%s", prefix, refPath), &refResp); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, "", errNotFound
		}
		return nil, "", fmt.Errorf("get ref: %w", err)
	}
	commitSHA := refResp.Object.SHA

	// 2. Resolve the commit → tree SHA
	var commitResp struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.get(fmt.Sprintf("%s/git/commits/%s", prefix, commitSHA), &commitResp); err != nil {
		return nil, "", fmt.Errorf("get commit: %w", err)
	}

	// 3. Resolve the tree → entries
	var treeResp struct {
		Tree []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
	}
	if err := c.get(fmt.Sprintf("%s/git/trees/%s", prefix, commitResp.Tree.SHA), &treeResp); err != nil {
		return nil, "", fmt.Errorf("get tree: %w", err)
	}

	// 4. Fetch each blob and decode
	attachments := make([]Attachment, 0, len(treeResp.Tree))
	for _, entry := range treeResp.Tree {
		if entry.Type != "blob" {
			continue
		}
		var blobResp struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Size     int64  `json:"size"`
			SHA      string `json:"sha"`
		}
		if err := c.get(fmt.Sprintf("%s/git/blobs/%s", prefix, entry.SHA), &blobResp); err != nil {
			return nil, "", fmt.Errorf("get blob %s (%s): %w", entry.SHA, entry.Path, err)
		}
		if blobResp.Encoding != "base64" {
			return nil, "", fmt.Errorf("blob %s (%s): unexpected encoding %q, want base64", entry.SHA, entry.Path, blobResp.Encoding)
		}
		// GitHub wraps base64 payloads at 60 columns, so strip whitespace
		// before decoding.
		content, err := base64.StdEncoding.DecodeString(stripWhitespace(blobResp.Content))
		if err != nil {
			return nil, "", fmt.Errorf("decode blob %s (%s): %w", entry.SHA, entry.Path, err)
		}
		attachments = append(attachments, Attachment{
			Path:    entry.Path,
			SHA:     entry.SHA,
			Size:    entry.Size,
			Content: content,
		})
	}

	return attachments, commitSHA, nil
}

// stripWhitespace removes ASCII whitespace from s. GitHub's blob
// endpoint returns base64 payloads with embedded newlines every 60
// chars; base64.StdEncoding.DecodeString rejects those, so we clean
// them up first. Broader than strict RFC 4648 but that's the intent —
// we want to be forgiving about what we accept from the API.
func stripWhitespace(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
			continue
		}
		b = append(b, c)
	}
	return string(b)
}
