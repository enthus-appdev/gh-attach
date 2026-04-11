package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// listDeps builds a runDeps where resolveRepo returns a canned repo
// and newGitClient returns the passed fakeGitClient. Most list tests
// only need these two fields.
func listDeps(gc *fakeGitClient) runDeps {
	return runDeps{
		resolveRepo: func(override string) (*gh.Repo, error) {
			return &gh.Repo{Owner: "owner", Name: "repo"}, nil
		},
		newGitClient: func() (gitDataClient, error) { return gc, nil },
	}
}

func TestRunList_text_both_namespaces(t *testing.T) {
	gc := &fakeGitClient{
		listRefs: []gh.RefEntry{
			{Ref: "refs/uploads/issues/42", SHA: "abc1234def5678", Namespace: "issue", Target: "#42", Number: 42},
			{Ref: "refs/uploads/misc/design-v2", SHA: "9876abcdef1234", Namespace: "misc", Target: "misc/design-v2", Key: "design-v2"},
		},
	}
	var stdout, stderr bytes.Buffer
	code := runList(nil, &stdout, &stderr, listDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if gc.gotListPrefix != "" {
		t.Errorf("subPrefix = %q, want empty for no-filter", gc.gotListPrefix)
	}
	// Table header + both rows on stdout
	out := stdout.String()
	if !strings.Contains(out, "TARGET") || !strings.Contains(out, "SHA") || !strings.Contains(out, "NAMESPACE") {
		t.Errorf("stdout missing table header:\n%s", out)
	}
	if !strings.Contains(out, "#42") {
		t.Errorf("stdout missing issue row:\n%s", out)
	}
	if !strings.Contains(out, "misc/design-v2") {
		t.Errorf("stdout missing misc row:\n%s", out)
	}
	if !strings.Contains(out, "abc1234") {
		t.Errorf("stdout missing short SHA:\n%s", out)
	}
	if strings.Contains(out, "abc1234def5678") {
		t.Errorf("stdout should have short SHA, not full:\n%s", out)
	}
	// Summary line on stderr
	if !strings.Contains(stderr.String(), "2 upload ref(s) in owner/repo") {
		t.Errorf("stderr missing summary: %s", stderr.String())
	}
}

func TestRunList_issues_filter(t *testing.T) {
	gc := &fakeGitClient{
		listRefs: []gh.RefEntry{
			{Ref: "refs/uploads/issues/42", SHA: "sha", Namespace: "issue", Target: "#42", Number: 42},
		},
	}
	var stdout, stderr bytes.Buffer
	code := runList([]string{"--issues"}, &stdout, &stderr, listDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if gc.gotListPrefix != "issues" {
		t.Errorf("subPrefix = %q, want issues", gc.gotListPrefix)
	}
}

func TestRunList_misc_filter(t *testing.T) {
	gc := &fakeGitClient{}
	var stdout, stderr bytes.Buffer
	code := runList([]string{"--misc"}, &stdout, &stderr, listDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if gc.gotListPrefix != "misc" {
		t.Errorf("subPrefix = %q, want misc", gc.gotListPrefix)
	}
}

func TestRunList_mutually_exclusive_filters(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runList([]string{"--issues", "--misc"}, &stdout, &stderr, listDeps(&fakeGitClient{}))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "mutually exclusive") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

func TestRunList_empty(t *testing.T) {
	gc := &fakeGitClient{listRefs: nil}
	var stdout, stderr bytes.Buffer
	code := runList(nil, &stdout, &stderr, listDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty in text mode with no refs, got:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "No upload refs in owner/repo") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

func TestRunList_json(t *testing.T) {
	gc := &fakeGitClient{
		listRefs: []gh.RefEntry{
			{Ref: "refs/uploads/issues/42", SHA: "abc1234", Namespace: "issue", Target: "#42", Number: 42},
		},
	}
	var stdout, stderr bytes.Buffer
	code := runList([]string{"--json"}, &stdout, &stderr, listDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var parsed []gh.RefEntry
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if len(parsed) != 1 || parsed[0].Number != 42 {
		t.Errorf("parsed = %+v", parsed)
	}
}

func TestRunList_json_empty(t *testing.T) {
	gc := &fakeGitClient{listRefs: nil}
	var stdout, stderr bytes.Buffer
	code := runList([]string{"--json"}, &stdout, &stderr, listDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var parsed []gh.RefEntry
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if len(parsed) != 0 {
		t.Errorf("want empty array, got %+v", parsed)
	}
}

func TestRunList_repo_override_passed_through(t *testing.T) {
	var gotOverride string
	deps := runDeps{
		resolveRepo: func(override string) (*gh.Repo, error) {
			gotOverride = override
			return &gh.Repo{Owner: "o", Name: "r"}, nil
		},
		newGitClient: func() (gitDataClient, error) { return &fakeGitClient{}, nil },
	}
	var stdout, stderr bytes.Buffer
	runList([]string{"--repo", "other/repo"}, &stdout, &stderr, deps)
	if gotOverride != "other/repo" {
		t.Errorf("override passed = %q, want other/repo", gotOverride)
	}
}

func TestRunList_errors(t *testing.T) {
	t.Run("resolveRepo error", func(t *testing.T) {
		deps := runDeps{
			resolveRepo: func(string) (*gh.Repo, error) { return nil, errors.New("boom") },
		}
		var stdout, stderr bytes.Buffer
		code := runList(nil, &stdout, &stderr, deps)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "resolve repo: boom") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("newGitClient error", func(t *testing.T) {
		deps := runDeps{
			resolveRepo:  func(string) (*gh.Repo, error) { return &gh.Repo{}, nil },
			newGitClient: func() (gitDataClient, error) { return nil, errors.New("no auth") },
		}
		var stdout, stderr bytes.Buffer
		code := runList(nil, &stdout, &stderr, deps)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "create git client: no auth") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("ListRefs error", func(t *testing.T) {
		gc := &fakeGitClient{listErr: errors.New("api 500")}
		var stdout, stderr bytes.Buffer
		code := runList(nil, &stdout, &stderr, listDeps(gc))
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "list refs: api 500") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runList([]string{"--nope"}, &stdout, &stderr, listDeps(&fakeGitClient{}))
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})
}
