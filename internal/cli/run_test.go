package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// ---------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------

// fakeGitClient is a test double for gh.GitDataClient. It captures the
// args it was called with and returns canned results or a canned error.
type fakeGitClient struct {
	// canned response
	paths  []gh.AttachmentPath
	sha    string
	err    error
	called bool
	// captured args
	gotRepo          *gh.Repo
	gotRefPath       string
	gotCommitMessage string
	gotFiles         []string
}

func (f *fakeGitClient) PushAttachments(repo *gh.Repo, refPath, commitMessage string, files []string) ([]gh.AttachmentPath, string, error) {
	f.called = true
	f.gotRepo = repo
	f.gotRefPath = refPath
	f.gotCommitMessage = commitMessage
	f.gotFiles = files
	if f.err != nil {
		return nil, "", f.err
	}
	return f.paths, f.sha, nil
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
	err := runUpload(42, []string{"banner.png"}, "", false, "", "", &stdout, &stderr, deps)
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
	err := runUpload(0, []string{"mockup.png"}, "Design v2", false, "", "design-v2", &stdout, &stderr, deps)
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
	err := runUpload(0, []string{"f.png"}, "", false, "", "", &stdout, &stderr, deps)
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
	err := runUpload(7, []string{"f.png"}, "", true, "", "", &stdout, &stderr, deps)
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
			err := runUpload(tt.number, tt.files, "", tt.postComment, tt.repoOverride, tt.key, io.Discard, io.Discard, deps)
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
		err := runUpload(42, []string{"f.png"}, "", false, "", "", io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "resolve repo: boom") {
			t.Errorf("got %v, want 'resolve repo: boom'", err)
		}
	})

	t.Run("resolvePR error", func(t *testing.T) {
		deps := baseDeps()
		deps.resolvePR = func(*gh.Repo) (int, error) { return 0, errors.New("no PR") }
		err := runUpload(0, []string{"f.png"}, "", false, "", "", io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "resolve PR: no PR") {
			t.Errorf("got %v, want 'resolve PR: no PR'", err)
		}
	})

	t.Run("expandFiles error", func(t *testing.T) {
		deps := baseDeps()
		deps.expandFiles = func([]string) ([]string, error) { return nil, errors.New("no files") }
		err := runUpload(42, []string{"f.png"}, "", false, "", "", io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "no files") {
			t.Errorf("got %v, want 'no files'", err)
		}
	})

	t.Run("newGitClient error", func(t *testing.T) {
		deps := baseDeps()
		deps.newGitClient = func() (gitDataClient, error) { return nil, errors.New("no auth") }
		err := runUpload(42, []string{"f.png"}, "", false, "", "", io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "create git client: no auth") {
			t.Errorf("got %v, want 'create git client: no auth'", err)
		}
	})

	t.Run("PushAttachments error", func(t *testing.T) {
		deps := baseDeps()
		gc := &fakeGitClient{err: errors.New("api 500")}
		deps.newGitClient = func() (gitDataClient, error) { return gc, nil }
		err := runUpload(42, []string{"f.png"}, "", false, "", "", io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "push attachments: api 500") {
			t.Errorf("got %v, want 'push attachments: api 500'", err)
		}
	})

	t.Run("newCmtClient error under --comment", func(t *testing.T) {
		deps := baseDeps()
		deps.newCmtClient = func() (commentClient, error) { return nil, errors.New("no auth") }
		err := runUpload(42, []string{"f.png"}, "", true, "", "", io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "create comment client: no auth") {
			t.Errorf("got %v, want 'create comment client: no auth'", err)
		}
	})

	t.Run("UpsertComment error under --comment", func(t *testing.T) {
		deps := baseDeps()
		cc := &fakeCmtClient{err: errors.New("forbidden")}
		deps.newCmtClient = func() (commentClient, error) { return cc, nil }
		err := runUpload(42, []string{"f.png"}, "", true, "", "", io.Discard, io.Discard, deps)
		if err == nil || !strings.Contains(err.Error(), "upsert comment: forbidden") {
			t.Errorf("got %v, want 'upsert comment: forbidden'", err)
		}
	})
}

// ---------------------------------------------------------------------
// Run() tests — exercise the flag parsing + top-level paths that go
// through the real default dependencies. All of these should fail
// before any network call, so they don't need fakes.
// ---------------------------------------------------------------------

func TestRunParseErrors(t *testing.T) {
	t.Run("no args shows usage and returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(nil, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Errorf("stderr missing usage: %s", stderr.String())
		}
	})

	t.Run("unknown flag returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--nope", "file.png"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "flag provided but not defined") {
			t.Errorf("stderr missing flag error: %s", stderr.String())
		}
	})

	t.Run("-h shows usage and returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"-h"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "Upload images") {
			t.Errorf("stderr missing usage content: %s", stderr.String())
		}
	})

	t.Run("number only (no files) returns 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"42"}, &stdout, &stderr)
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
		code := Run([]string{"--key", "design", "42", "file.png"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "cannot combine NUMBER with --key") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("--key + --comment", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--key", "design", "--comment", "file.png"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "--comment requires a PR/issue number") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("--key pure numeric rejected", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--key", "123", "file.png"}, &stdout, &stderr)
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
	code := Run([]string{"--definitely-not-a-flag"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

