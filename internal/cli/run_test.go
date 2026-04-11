package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// ---------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------

// fakeGitClient is a test double for gh.GitDataClient. It captures the
// args it was called with and returns canned results or a canned error.
// It implements the full gitDataClient interface (PushAttachments +
// ListRefs + DeleteRef); individual tests only populate the fields
// relevant to the method they exercise.
type fakeGitClient struct {
	// canned response for PushAttachments
	paths  []gh.AttachmentPath
	sha    string
	err    error
	called bool
	// captured args (PushAttachments)
	gotRepo          *gh.Repo
	gotRefPath       string
	gotCommitMessage string
	gotFiles         []string

	// If true, PushAttachments snapshots the bytes of each file at
	// call time into gotPushContent (keyed by basename). Needed by
	// stdin tests because runUpload defers os.RemoveAll on the temp
	// dir, so the temp file is gone by the time the test body runs
	// its assertions.
	savePushContent bool
	gotPushContent  map[string][]byte

	// canned response for ListRefs
	listRefs      []gh.RefEntry
	listErr       error
	listCalled    bool
	gotListPrefix string

	// canned response for DeleteRef
	deleteErr     error
	deleteCalled  bool
	gotDeletePath string
}

func (f *fakeGitClient) PushAttachments(repo *gh.Repo, refPath, commitMessage string, files []string) ([]gh.AttachmentPath, string, error) {
	f.called = true
	f.gotRepo = repo
	f.gotRefPath = refPath
	f.gotCommitMessage = commitMessage
	f.gotFiles = files
	if f.savePushContent {
		f.gotPushContent = make(map[string][]byte, len(files))
		for _, p := range files {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, "", err
			}
			f.gotPushContent[filepath.Base(p)] = data
		}
	}
	if f.err != nil {
		return nil, "", f.err
	}
	return f.paths, f.sha, nil
}

func (f *fakeGitClient) ListRefs(repo *gh.Repo, subPrefix string) ([]gh.RefEntry, error) {
	f.listCalled = true
	f.gotRepo = repo
	f.gotListPrefix = subPrefix
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listRefs, nil
}

func (f *fakeGitClient) DeleteRef(repo *gh.Repo, refPath string) error {
	f.deleteCalled = true
	f.gotRepo = repo
	f.gotDeletePath = refPath
	return f.deleteErr
}

// fakeCmtClient is a test double for gh.CommentClient. Same shape as
// fakeGitClient.
type fakeCmtClient struct {
	url    string
	err    error
	called bool
	// captured args
	gotRepo      *gh.Repo
	gotNumber    int
	gotPaths     []gh.AttachmentPath
	gotCommitSHA string
	gotTitle     string
}

func (f *fakeCmtClient) UpsertComment(repo *gh.Repo, number int, paths []gh.AttachmentPath, commitSHA, title string) (string, error) {
	f.called = true
	f.gotRepo = repo
	f.gotNumber = number
	f.gotPaths = paths
	f.gotCommitSHA = commitSHA
	f.gotTitle = title
	if f.err != nil {
		return "", f.err
	}
	return f.url, nil
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// happyDeps builds a runDeps where every dependency succeeds with
// sensible fake values. Individual tests can override fields as needed.
func happyDeps(gitClient *fakeGitClient, cmtClient *fakeCmtClient) runDeps {
	return runDeps{
		resolveRepo: func(override string) (*gh.Repo, error) {
			if override != "" {
				return &gh.Repo{Owner: "overridden", Name: "repo"}, nil
			}
			return &gh.Repo{Owner: "auto", Name: "repo"}, nil
		},
		resolvePR: func(repo *gh.Repo) (int, error) {
			return 99, nil
		},
		newGitClient: func() (gitDataClient, error) { return gitClient, nil },
		newCmtClient: func() (commentClient, error) { return cmtClient, nil },
		expandFiles: func(patterns []string) ([]string, error) {
			return patterns, nil
		},
		stdin: strings.NewReader(""),
	}
}

// ---------------------------------------------------------------------
// runUpload tests (direct — skip flag parsing, exercise core logic)
// ---------------------------------------------------------------------

func TestRunUploadIssueMode(t *testing.T) {
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "banner.png"}},
		sha:   "abc1234",
	}
	cmt := &fakeCmtClient{}
	deps := happyDeps(git, cmt)

	var stdout, stderr bytes.Buffer
	err := runUpload(42, []string{"banner.png"}, "", false, "", "", "", false, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !git.called {
		t.Error("gitClient.PushAttachments was not called")
	}
	if cmt.called {
		t.Error("cmtClient should NOT be called without --comment")
	}
	if git.gotRefPath != "uploads/issues/42" {
		t.Errorf("refPath = %q, want %q", git.gotRefPath, "uploads/issues/42")
	}
	if git.gotCommitMessage != "upload for #42" {
		t.Errorf("commit message = %q, want %q", git.gotCommitMessage, "upload for #42")
	}
	if !strings.Contains(stdout.String(), "![banner.png]") {
		t.Errorf("stdout missing markdown image: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "abc1234") {
		t.Errorf("stdout missing commit SHA: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Uploading 1 file(s) to #42 in auto/repo...") {
		t.Errorf("stderr missing progress line: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Uploaded:") {
		t.Errorf("stderr missing 'Uploaded:' header: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "blob/abc1234/banner.png?raw=true") {
		t.Errorf("stderr missing URL line: %s", stderr.String())
	}
}

func TestRunUploadKeyMode(t *testing.T) {
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "mockup.png"}},
		sha:   "def5678",
	}
	cmt := &fakeCmtClient{}
	deps := happyDeps(git, cmt)

	var stdout, stderr bytes.Buffer
	err := runUpload(0, []string{"mockup.png"}, "Design v2", false, "", "design-v2", "", false, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if git.gotRefPath != "uploads/misc/design-v2" {
		t.Errorf("refPath = %q, want %q", git.gotRefPath, "uploads/misc/design-v2")
	}
	if git.gotCommitMessage != "upload for misc/design-v2" {
		t.Errorf("commit message = %q, want %q", git.gotCommitMessage, "upload for misc/design-v2")
	}
	if cmt.called {
		t.Error("cmtClient should NOT be called in key mode")
	}
	if !strings.Contains(stderr.String(), "misc/design-v2") {
		t.Errorf("stderr missing key target: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "**Design v2**") {
		t.Errorf("stdout missing title: %s", stdout.String())
	}
}

func TestRunUploadAutoDetectPR(t *testing.T) {
	git := &fakeGitClient{sha: "sha", paths: []gh.AttachmentPath{{Path: "f.png"}}}
	deps := happyDeps(git, &fakeCmtClient{})

	var stdout, stderr bytes.Buffer
	err := runUpload(0, []string{"f.png"}, "", false, "", "", "", false, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// happyDeps.resolvePR returns 99.
	if git.gotRefPath != "uploads/issues/99" {
		t.Errorf("refPath = %q, want uploads/issues/99", git.gotRefPath)
	}
}

func TestRunUploadWithComment(t *testing.T) {
	git := &fakeGitClient{sha: "sha", paths: []gh.AttachmentPath{{Path: "f.png"}}}
	cmt := &fakeCmtClient{url: "https://example.com/pull/7#issuecomment-1"}
	deps := happyDeps(git, cmt)

	var stdout, stderr bytes.Buffer
	err := runUpload(7, []string{"f.png"}, "", true, "", "", "", false, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cmt.called {
		t.Error("cmtClient.UpsertComment should have been called")
	}
	if cmt.gotNumber != 7 {
		t.Errorf("comment number = %d, want 7", cmt.gotNumber)
	}
	if !strings.Contains(stderr.String(), "Commented: https://example.com/pull/7#issuecomment-1") {
		t.Errorf("stderr missing Commented line: %s", stderr.String())
	}
}

// ---------------------------------------------------------------------
// runUpload --json tests
// ---------------------------------------------------------------------

func TestRunUpload_json_issue_mode(t *testing.T) {
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "banner.png"}},
		sha:   "abc1234def5678",
	}
	deps := happyDeps(git, &fakeCmtClient{})

	var stdout, stderr bytes.Buffer
	err := runUpload(42, []string{"banner.png"}, "", false, "", "", "", true, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stderr should be completely silent in JSON mode — no progress
	// line, no Uploaded: URL list.
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty in JSON mode, got:\n%s", stderr.String())
	}

	// Parse the stdout JSON and assert the expected fields.
	var parsed uploadResult
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}

	if parsed.Repo != "auto/repo" {
		t.Errorf("repo = %q, want auto/repo", parsed.Repo)
	}
	if parsed.Target != "#42" {
		t.Errorf("target = %q, want #42", parsed.Target)
	}
	if parsed.Namespace != "issue" {
		t.Errorf("namespace = %q, want issue", parsed.Namespace)
	}
	if parsed.Number != 42 {
		t.Errorf("number = %d, want 42", parsed.Number)
	}
	if parsed.Key != "" {
		t.Errorf("key should be empty in issue mode, got %q", parsed.Key)
	}
	if parsed.Ref != "refs/uploads/issues/42" {
		t.Errorf("ref = %q, want refs/uploads/issues/42", parsed.Ref)
	}
	if parsed.CommitSHA != "abc1234def5678" {
		t.Errorf("sha = %q, want abc1234def5678", parsed.CommitSHA)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Name != "banner.png" {
		t.Errorf("files = %+v, want [{Name:banner.png ...}]", parsed.Files)
	}
	if !strings.Contains(parsed.Files[0].URL, "blob/abc1234def5678/banner.png?raw=true") {
		t.Errorf("files[0].url = %q", parsed.Files[0].URL)
	}
	if !strings.Contains(parsed.Markdown, "![banner.png]") {
		t.Errorf("markdown field missing expected content: %q", parsed.Markdown)
	}
	if parsed.CommentURL != "" {
		t.Errorf("comment_url should be empty without --comment, got %q", parsed.CommentURL)
	}
}

func TestRunUpload_json_key_mode(t *testing.T) {
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "mockup.png"}},
		sha:   "feedc0de",
	}
	deps := happyDeps(git, &fakeCmtClient{})

	var stdout, stderr bytes.Buffer
	err := runUpload(0, []string{"mockup.png"}, "", false, "", "design-v2", "", true, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty, got:\n%s", stderr.String())
	}

	var parsed uploadResult
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if parsed.Namespace != "misc" {
		t.Errorf("namespace = %q, want misc", parsed.Namespace)
	}
	if parsed.Key != "design-v2" {
		t.Errorf("key = %q, want design-v2", parsed.Key)
	}
	if parsed.Number != 0 {
		t.Errorf("number should be zero in misc mode, got %d", parsed.Number)
	}
	if parsed.Target != "misc/design-v2" {
		t.Errorf("target = %q, want misc/design-v2", parsed.Target)
	}
	if parsed.Ref != "refs/uploads/misc/design-v2" {
		t.Errorf("ref = %q", parsed.Ref)
	}
}

func TestRunUpload_json_with_comment(t *testing.T) {
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "f.png"}},
		sha:   "sha",
	}
	cmt := &fakeCmtClient{url: "https://example.com/pull/7#issuecomment-42"}
	deps := happyDeps(git, cmt)

	var stdout, stderr bytes.Buffer
	err := runUpload(7, []string{"f.png"}, "", true, "", "", "", true, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cmt.called {
		t.Error("cmtClient.UpsertComment should have been called with --comment")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty, got:\n%s", stderr.String())
	}

	var parsed uploadResult
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if parsed.CommentURL != "https://example.com/pull/7#issuecomment-42" {
		t.Errorf("comment_url = %q, want https://example.com/pull/7#issuecomment-42", parsed.CommentURL)
	}
}

func TestRunUpload_json_url_encoding(t *testing.T) {
	// Filenames with special characters must be URL-encoded in the
	// files[].url field. The Name field stays raw so consumers can
	// display it as-is.
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "Screen Shot 2026.png"}},
		sha:   "sha",
	}
	deps := happyDeps(git, &fakeCmtClient{})

	var stdout, stderr bytes.Buffer
	err := runUpload(1, []string{"Screen Shot 2026.png"}, "", false, "", "", "", true, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed uploadResult
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if parsed.Files[0].Name != "Screen Shot 2026.png" {
		t.Errorf("files[0].name = %q, want raw filename", parsed.Files[0].Name)
	}
	if !strings.Contains(parsed.Files[0].URL, "Screen%20Shot%202026.png") {
		t.Errorf("files[0].url = %q, want URL-encoded", parsed.Files[0].URL)
	}
}

func TestRunUpload_json_via_runWithDeps(t *testing.T) {
	// Exercise the full flag-parse path through runWithDeps so the
	// --json flag registration is covered end-to-end.
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "f.png"}},
		sha:   "sha",
	}
	deps := happyDeps(git, &fakeCmtClient{})

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--json", "42", "f.png"}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty in JSON mode, got:\n%s", stderr.String())
	}
	var parsed uploadResult
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if parsed.Number != 42 {
		t.Errorf("number = %d, want 42", parsed.Number)
	}
}

func TestRunUploadConflictsAndValidation(t *testing.T) {
	tests := []struct {
		name         string
		number       int
		files        []string
		postComment  bool
		repoOverride string
		key          string
		errSubstr    string
	}{
		{
			name:      "NUMBER + --key",
			number:    42,
			files:     []string{"f.png"},
			key:       "design",
			errSubstr: "cannot combine NUMBER with --key",
		},
		{
			name:        "--key + --comment",
			files:       []string{"f.png"},
			postComment: true,
			key:         "design",
			errSubstr:   "--comment requires a PR/issue number",
		},
		{
			name:      "--key invalid (pure numeric)",
			files:     []string{"f.png"},
			key:       "123",
			errSubstr: "purely numeric",
		},
		{
			name:         "--repo without NUMBER or --key",
			files:        []string{"f.png"},
			repoOverride: "owner/repo",
			errSubstr:    "--repo requires an explicit NUMBER or --key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := happyDeps(&fakeGitClient{}, &fakeCmtClient{})
			err := runUpload(tt.number, tt.files, "", tt.postComment, tt.repoOverride, tt.key, "", false, io.Discard, io.Discard, deps)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.errSubstr)
			}
		})
	}
}

func TestRunUploadDependencyErrors(t *testing.T) {
	baseDeps := func() runDeps {
		return happyDeps(
			&fakeGitClient{sha: "sha", paths: []gh.AttachmentPath{{Path: "f.png"}}},
			&fakeCmtClient{url: "url"},
		)
	}

	t.Run("resolveRepo error", func(t *testing.T) {
		deps := baseDeps()
		deps.resolveRepo = func(string) (*gh.Repo, error) { return nil, errors.New("boom") }
		err := runUpload(42, []string{"f.png"}, "", false, "", "", "", false, io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "resolve repo: boom") {
			t.Errorf("got %v, want 'resolve repo: boom'", err)
		}
	})

	t.Run("resolvePR error", func(t *testing.T) {
		deps := baseDeps()
		deps.resolvePR = func(*gh.Repo) (int, error) { return 0, errors.New("no PR") }
		err := runUpload(0, []string{"f.png"}, "", false, "", "", "", false, io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "resolve PR: no PR") {
			t.Errorf("got %v, want 'resolve PR: no PR'", err)
		}
	})

	t.Run("expandFiles error", func(t *testing.T) {
		deps := baseDeps()
		deps.expandFiles = func([]string) ([]string, error) { return nil, errors.New("no files") }
		err := runUpload(42, []string{"f.png"}, "", false, "", "", "", false, io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "no files") {
			t.Errorf("got %v, want 'no files'", err)
		}
	})

	t.Run("newGitClient error", func(t *testing.T) {
		deps := baseDeps()
		deps.newGitClient = func() (gitDataClient, error) { return nil, errors.New("no auth") }
		err := runUpload(42, []string{"f.png"}, "", false, "", "", "", false, io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "create git client: no auth") {
			t.Errorf("got %v, want 'create git client: no auth'", err)
		}
	})

	t.Run("PushAttachments error", func(t *testing.T) {
		deps := baseDeps()
		gc := &fakeGitClient{err: errors.New("api 500")}
		deps.newGitClient = func() (gitDataClient, error) { return gc, nil }
		err := runUpload(42, []string{"f.png"}, "", false, "", "", "", false, io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "push attachments: api 500") {
			t.Errorf("got %v, want 'push attachments: api 500'", err)
		}
	})

	t.Run("newCmtClient error under --comment", func(t *testing.T) {
		deps := baseDeps()
		deps.newCmtClient = func() (commentClient, error) { return nil, errors.New("no auth") }
		err := runUpload(42, []string{"f.png"}, "", true, "", "", "", false, io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "create comment client: no auth") {
			t.Errorf("got %v, want 'create comment client: no auth'", err)
		}
	})

	t.Run("UpsertComment error under --comment", func(t *testing.T) {
		deps := baseDeps()
		cc := &fakeCmtClient{err: errors.New("forbidden")}
		deps.newCmtClient = func() (commentClient, error) { return cc, nil }
		err := runUpload(42, []string{"f.png"}, "", true, "", "", "", false, io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "upsert comment: forbidden") {
			t.Errorf("got %v, want 'upsert comment: forbidden'", err)
		}
	})
}

// ---------------------------------------------------------------------
// runUpload stdin tests — exercise the `-` filename + --name flag
// that materialize deps.stdin into a temp file under the user-chosen
// basename before upload.
// ---------------------------------------------------------------------

func TestRunUpload_stdin_issue_mode(t *testing.T) {
	git := &fakeGitClient{
		paths:           []gh.AttachmentPath{{Path: "shot.png"}},
		sha:             "abc1234",
		savePushContent: true,
	}
	deps := happyDeps(git, &fakeCmtClient{})
	deps.stdin = strings.NewReader("PNG-BYTES")

	var stdout, stderr bytes.Buffer
	err := runUpload(42, []string{"-"}, "", false, "", "", "shot.png", false, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !git.called {
		t.Fatal("PushAttachments was not called")
	}
	if len(git.gotFiles) != 1 {
		t.Fatalf("gotFiles = %v, want exactly one file", git.gotFiles)
	}
	// The temp path's basename must equal --name so filepath.Base
	// inside PushAttachments yields the user-chosen tree-entry name.
	if got := filepath.Base(git.gotFiles[0]); got != "shot.png" {
		t.Errorf("temp basename = %q, want shot.png", got)
	}
	if got := string(git.gotPushContent["shot.png"]); got != "PNG-BYTES" {
		t.Errorf("temp content = %q, want PNG-BYTES", got)
	}
	if git.gotRefPath != "uploads/issues/42" {
		t.Errorf("refPath = %q, want uploads/issues/42", git.gotRefPath)
	}
	if !strings.Contains(stderr.String(), "Uploading 1 file(s) to #42 in auto/repo...") {
		t.Errorf("stderr missing progress line: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "![shot.png]") {
		t.Errorf("stdout missing markdown image: %s", stdout.String())
	}
}

func TestRunUpload_stdin_key_mode(t *testing.T) {
	git := &fakeGitClient{
		paths:           []gh.AttachmentPath{{Path: "diagram.png"}},
		sha:             "def5678",
		savePushContent: true,
	}
	deps := happyDeps(git, &fakeCmtClient{})
	deps.stdin = strings.NewReader("DIAGRAM-BYTES")

	var stdout, stderr bytes.Buffer
	err := runUpload(0, []string{"-"}, "", false, "", "docs/v2", "diagram.png", false, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if git.gotRefPath != "uploads/misc/docs/v2" {
		t.Errorf("refPath = %q, want uploads/misc/docs/v2", git.gotRefPath)
	}
	if got := filepath.Base(git.gotFiles[0]); got != "diagram.png" {
		t.Errorf("temp basename = %q, want diagram.png", got)
	}
	if got := string(git.gotPushContent["diagram.png"]); got != "DIAGRAM-BYTES" {
		t.Errorf("temp content = %q, want DIAGRAM-BYTES", got)
	}
}

func TestRunUpload_stdin_with_comment(t *testing.T) {
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "clip.png"}},
		sha:   "sha",
	}
	cmt := &fakeCmtClient{url: "https://example.com/pull/7#issuecomment-1"}
	deps := happyDeps(git, cmt)
	deps.stdin = strings.NewReader("CLIP")

	var stdout, stderr bytes.Buffer
	err := runUpload(7, []string{"-"}, "", true, "", "", "clip.png", false, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cmt.called {
		t.Error("UpsertComment should have been called with --comment")
	}
	if cmt.gotNumber != 7 {
		t.Errorf("comment number = %d, want 7", cmt.gotNumber)
	}
	if !strings.Contains(stderr.String(), "Commented: https://example.com/pull/7#issuecomment-1") {
		t.Errorf("stderr missing Commented line: %s", stderr.String())
	}
}

func TestRunUpload_stdin_empty_allowed(t *testing.T) {
	// An empty stdin stream is a legitimate case (e.g. an empty
	// capture) — it must produce a 0-byte file, not an error, and
	// PushAttachments should be called normally.
	git := &fakeGitClient{
		paths:           []gh.AttachmentPath{{Path: "empty.png"}},
		sha:             "sha",
		savePushContent: true,
	}
	deps := happyDeps(git, &fakeCmtClient{})
	deps.stdin = strings.NewReader("")

	var stdout, stderr bytes.Buffer
	err := runUpload(1, []string{"-"}, "", false, "", "", "empty.png", false, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !git.called {
		t.Error("PushAttachments should be called even for an empty stdin")
	}
	if got := git.gotPushContent["empty.png"]; len(got) != 0 {
		t.Errorf("temp content = %q, want empty", got)
	}
}

func TestRunUpload_stdin_json(t *testing.T) {
	git := &fakeGitClient{
		paths: []gh.AttachmentPath{{Path: "shot.png"}},
		sha:   "abc1234",
	}
	deps := happyDeps(git, &fakeCmtClient{})
	deps.stdin = strings.NewReader("PNG")

	var stdout, stderr bytes.Buffer
	err := runUpload(42, []string{"-"}, "", false, "", "", "shot.png", true, &stdout, &stderr, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty in JSON mode, got:\n%s", stderr.String())
	}
	var parsed uploadResult
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if parsed.Number != 42 {
		t.Errorf("number = %d, want 42", parsed.Number)
	}
	if len(parsed.Files) != 1 || parsed.Files[0].Name != "shot.png" {
		t.Errorf("files = %+v, want [{Name:shot.png ...}]", parsed.Files)
	}
}

func TestRunUpload_stdin_via_runWithDeps(t *testing.T) {
	// Exercise the full flag-parse path so --name + `-` at the shell
	// layer work end-to-end (flag registration, positional parsing,
	// stdin wiring, temp materialization, upload).
	git := &fakeGitClient{
		paths:           []gh.AttachmentPath{{Path: "piped.png"}},
		sha:             "sha",
		savePushContent: true,
	}
	deps := happyDeps(git, &fakeCmtClient{})
	deps.stdin = strings.NewReader("BYTES")

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--name", "piped.png", "42", "-"}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if !git.called {
		t.Error("PushAttachments was not called via runWithDeps")
	}
	if got := filepath.Base(git.gotFiles[0]); got != "piped.png" {
		t.Errorf("basename = %q, want piped.png", got)
	}
	if got := string(git.gotPushContent["piped.png"]); got != "BYTES" {
		t.Errorf("content = %q, want BYTES", got)
	}
}

func TestRunUpload_stdin_read_error(t *testing.T) {
	// A stdin reader that errors must bubble the materializeStdin
	// failure out of runUpload without ever calling PushAttachments.
	// errReader is defined in files_test.go (same package).
	git := &fakeGitClient{}
	deps := happyDeps(git, &fakeCmtClient{})
	deps.stdin = errReader{}

	var stdout, stderr bytes.Buffer
	err := runUpload(42, []string{"-"}, "", false, "", "", "shot.png", false, &stdout, &stderr, deps)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "read stdin") {
		t.Errorf("error = %q, want substring 'read stdin'", err.Error())
	}
	if git.called {
		t.Error("PushAttachments should not have been called after a stdin read error")
	}
}

func TestRunUpload_stdin_arg_conflicts(t *testing.T) {
	tests := []struct {
		name      string
		number    int
		files     []string
		nameFlag  string
		errSubstr string
	}{
		{
			name:      "--name without dash",
			number:    42,
			files:     []string{"file.png"},
			nameFlag:  "custom.png",
			errSubstr: "--name is only valid when reading from stdin",
		},
		{
			name:      "dash without --name",
			number:    42,
			files:     []string{"-"},
			nameFlag:  "",
			errSubstr: "--name is required when reading from stdin",
		},
		{
			name:      "dash mixed with files",
			number:    42,
			files:     []string{"-", "extra.png"},
			nameFlag:  "",
			errSubstr: "`-` must be the only file argument",
		},
		{
			name:      "dash mixed with files, dash second",
			number:    42,
			files:     []string{"a.png", "-"},
			nameFlag:  "",
			errSubstr: "`-` must be the only file argument",
		},
		{
			name:      "invalid name (path)",
			number:    42,
			files:     []string{"-"},
			nameFlag:  "../escape.png",
			errSubstr: "must be a basename",
		},
		{
			name:      "invalid name (dot)",
			number:    42,
			files:     []string{"-"},
			nameFlag:  ".",
			errSubstr: `cannot be "."`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := happyDeps(&fakeGitClient{}, &fakeCmtClient{})
			deps.stdin = strings.NewReader("")
			err := runUpload(tt.number, tt.files, "", false, "", "", tt.nameFlag, false, io.Discard, io.Discard, deps)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.errSubstr)
			}
		})
	}
}

// ---------------------------------------------------------------------
// Run() tests — exercise the flag parsing + top-level paths that go
// through the real default dependencies. All of these should fail
// before any network call, so they don't need fakes.
// ---------------------------------------------------------------------

func TestRunParseErrors(t *testing.T) {
	t.Run("no args shows usage and returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(nil, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Errorf("stderr missing usage: %s", stderr.String())
		}
	})

	t.Run("unknown flag returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--nope", "file.png"}, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "flag provided but not defined") {
			t.Errorf("stderr missing flag error: %s", stderr.String())
		}
	})

	t.Run("-h shows usage and returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"-h"}, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "Upload images") {
			t.Errorf("stderr missing usage content: %s", stderr.String())
		}
	})

	t.Run("number only (no files) returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"42"}, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "no image files specified") {
			t.Errorf("stderr missing 'no image files' error: %s", stderr.String())
		}
	})
}

func TestRunArgConflictsViaFlags(t *testing.T) {
	// These conflicts trip the validation in runUpload before any
	// dependency call, so Run() with real defaultDeps() still errors
	// cleanly without touching the network.
	t.Run("NUMBER + --key", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--key", "design", "42", "file.png"}, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "cannot combine NUMBER with --key") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("--key + --comment", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--key", "design", "--comment", "file.png"}, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "--comment requires a PR/issue number") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("--key pure numeric rejected", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--key", "123", "file.png"}, strings.NewReader(""), &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "purely numeric") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})
}

// TestRunWithDepsFullFlow exercises runWithDeps (the testable core of
// Run()) with fake dependencies so we can cover the success path from
// flag parsing through to the final "Commented:" line without touching
// the network. Also covers the Run()-level error wrapping branch.
func TestRunWithDepsFullFlow(t *testing.T) {
	t.Run("success with explicit NUMBER", func(t *testing.T) {
		git := &fakeGitClient{
			paths: []gh.AttachmentPath{{Path: "file.png"}},
			sha:   "c0ffee",
		}
		deps := happyDeps(git, &fakeCmtClient{})

		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"42", "file.png"}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\nstderr=%s", code, stderr.String())
		}
		if !git.called {
			t.Error("gitClient.PushAttachments not called")
		}
		if !strings.Contains(stdout.String(), "![file.png]") {
			t.Errorf("stdout missing image markdown: %s", stdout.String())
		}
	})

	t.Run("success with --key", func(t *testing.T) {
		git := &fakeGitClient{
			paths: []gh.AttachmentPath{{Path: "f.png"}},
			sha:   "sha",
		}
		deps := happyDeps(git, &fakeCmtClient{})
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"--key", "my-key", "f.png"}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\nstderr=%s", code, stderr.String())
		}
		if git.gotRefPath != "uploads/misc/my-key" {
			t.Errorf("refPath = %q", git.gotRefPath)
		}
	})

	t.Run("success with --comment", func(t *testing.T) {
		git := &fakeGitClient{
			paths: []gh.AttachmentPath{{Path: "f.png"}},
			sha:   "sha",
		}
		cmt := &fakeCmtClient{url: "https://example/cmt"}
		deps := happyDeps(git, cmt)
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"--comment", "--title", "Hi", "5", "f.png"}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\nstderr=%s", code, stderr.String())
		}
		if !cmt.called {
			t.Error("cmtClient.UpsertComment not called")
		}
		if cmt.gotTitle != "Hi" {
			t.Errorf("title = %q, want Hi", cmt.gotTitle)
		}
		if !strings.Contains(stderr.String(), "Commented: https://example/cmt") {
			t.Errorf("stderr missing Commented line: %s", stderr.String())
		}
	})

	t.Run("filename with spaces is URL-encoded in stderr output", func(t *testing.T) {
		git := &fakeGitClient{
			paths: []gh.AttachmentPath{{Path: "Screen Shot 2026.png"}},
			sha:   "sha",
		}
		deps := happyDeps(git, &fakeCmtClient{})
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"42", "Screen Shot 2026.png"}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
		}
		// stderr URL should be encoded
		if !strings.Contains(stderr.String(), "Screen%20Shot%202026.png?raw=true") {
			t.Errorf("stderr missing URL-encoded filename:\n%s", stderr.String())
		}
		// stdout markdown should have URL-encoded URL but raw alt text (from gh.FormatSection)
		if !strings.Contains(stdout.String(), "Screen%20Shot%202026.png?raw=true") {
			t.Errorf("stdout missing URL-encoded filename in markdown:\n%s", stdout.String())
		}
		if !strings.Contains(stdout.String(), "![Screen Shot 2026.png]") {
			t.Errorf("stdout missing raw display name in alt:\n%s", stdout.String())
		}
	})

	t.Run("success with auto-detect PR", func(t *testing.T) {
		git := &fakeGitClient{
			paths: []gh.AttachmentPath{{Path: "f.png"}},
			sha:   "sha",
		}
		deps := happyDeps(git, &fakeCmtClient{})
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"f.png"}, &stdout, &stderr, deps)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0\nstderr=%s", code, stderr.String())
		}
		if git.gotRefPath != "uploads/issues/99" {
			t.Errorf("refPath = %q, want uploads/issues/99", git.gotRefPath)
		}
	})

	t.Run("runUpload error bubbles up as stderr + exit 1", func(t *testing.T) {
		deps := happyDeps(&fakeGitClient{err: errors.New("bang")}, &fakeCmtClient{})
		var stdout, stderr bytes.Buffer
		code := runWithDeps([]string{"42", "f.png"}, &stdout, &stderr, deps)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "error: push attachments: bang") {
			t.Errorf("stderr missing error line: %s", stderr.String())
		}
	})
}

// TestRunDelegatesToRunWithDeps is a tiny smoke test that Run() actually
// calls runWithDeps with defaultDeps — it verifies Run's one-line body
// doesn't regress.
func TestRunDelegatesToRunWithDeps(t *testing.T) {
	var stdout, stderr bytes.Buffer
	// Unknown flag → runWithDeps hits the flag.Parse error path → 1.
	// Exercises Run()'s only statement.
	code := Run([]string{"--definitely-not-a-flag"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

