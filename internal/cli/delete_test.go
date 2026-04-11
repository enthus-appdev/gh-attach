package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// deleteDeps builds a runDeps where resolveRepo returns a canned repo
// and newGitClient returns the passed fakeGitClient.
func deleteDeps(gc *fakeGitClient) runDeps {
	return runDeps{
		resolveRepo: func(override string) (*gh.Repo, error) {
			return &gh.Repo{Owner: "owner", Name: "repo"}, nil
		},
		newGitClient: func() (gitDataClient, error) { return gc, nil },
	}
}

func TestRunDelete_force_issue(t *testing.T) {
	gc := &fakeGitClient{}
	var stdout, stderr bytes.Buffer
	code := runDelete([]string{"--yes", "42"}, strings.NewReader(""), &stdout, &stderr, deleteDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if !gc.deleteCalled {
		t.Error("DeleteRef not called")
	}
	if gc.gotDeletePath != "uploads/issues/42" {
		t.Errorf("refPath = %q, want uploads/issues/42", gc.gotDeletePath)
	}
	if !strings.Contains(stderr.String(), "Deleted refs/uploads/issues/42 in owner/repo") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

func TestRunDelete_force_key(t *testing.T) {
	gc := &fakeGitClient{}
	var stdout, stderr bytes.Buffer
	code := runDelete([]string{"--yes", "--key", "design-v2"}, strings.NewReader(""), &stdout, &stderr, deleteDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if gc.gotDeletePath != "uploads/misc/design-v2" {
		t.Errorf("refPath = %q, want uploads/misc/design-v2", gc.gotDeletePath)
	}
}

func TestRunDelete_confirm_yes_variants(t *testing.T) {
	// Each of these answers should count as a confirmation.
	for _, answer := range []string{"y\n", "Y\n", "yes\n", "YES\n", "Yes\n"} {
		t.Run("answer="+strings.TrimSpace(answer), func(t *testing.T) {
			gc := &fakeGitClient{}
			var stdout, stderr bytes.Buffer
			code := runDelete([]string{"42"}, strings.NewReader(answer), &stdout, &stderr, deleteDeps(gc))
			if code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if !gc.deleteCalled {
				t.Errorf("DeleteRef should have been called for answer %q", answer)
			}
		})
	}
}

func TestRunDelete_confirm_no_variants(t *testing.T) {
	// Each of these answers should abort without calling DeleteRef.
	for _, answer := range []string{"n\n", "no\n", "\n", "maybe\n", "idk\n"} {
		t.Run("answer="+strings.TrimSpace(answer), func(t *testing.T) {
			gc := &fakeGitClient{}
			var stdout, stderr bytes.Buffer
			code := runDelete([]string{"42"}, strings.NewReader(answer), &stdout, &stderr, deleteDeps(gc))
			if code != 0 {
				t.Fatalf("exit = %d, want 0 (abort is not an error)", code)
			}
			if gc.deleteCalled {
				t.Errorf("DeleteRef should NOT have been called for answer %q", answer)
			}
			if !strings.Contains(stderr.String(), "Aborted") {
				t.Errorf("stderr missing 'Aborted' for answer %q: %s", answer, stderr.String())
			}
		})
	}
}

func TestRunDelete_confirm_eof(t *testing.T) {
	// Empty stdin → EOF before any input → error suggesting --yes.
	gc := &fakeGitClient{}
	var stdout, stderr bytes.Buffer
	code := runDelete([]string{"42"}, strings.NewReader(""), &stdout, &stderr, deleteDeps(gc))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if gc.deleteCalled {
		t.Error("DeleteRef should NOT have been called on stdin EOF")
	}
	if !strings.Contains(stderr.String(), "--yes") {
		t.Errorf("stderr should suggest --yes: %s", stderr.String())
	}
}

func TestRunDelete_arg_conflicts(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		errSubstr string
	}{
		{name: "no NUMBER or --key", args: []string{"--yes"}, errSubstr: "specify a PR/issue NUMBER or --key"},
		{name: "NUMBER + --key", args: []string{"--yes", "--key", "foo", "42"}, errSubstr: "cannot combine NUMBER with --key"},
		{name: "invalid --key", args: []string{"--yes", "--key", "123"}, errSubstr: "purely numeric"},
		{name: "extra positional after NUMBER", args: []string{"--yes", "42", "extra"}, errSubstr: "unexpected extra argument(s): extra"},
		{name: "extra positional after --key", args: []string{"--yes", "--key", "foo", "garbage"}, errSubstr: "unexpected extra argument(s): garbage"},
		{name: "multiple extra positionals", args: []string{"--yes", "42", "a", "b", "c"}, errSubstr: "unexpected extra argument(s): a b c"},
		{name: "garbage with no NUMBER or --key", args: []string{"--yes", "foo"}, errSubstr: "unexpected extra argument(s): foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gc := &fakeGitClient{}
			var stdout, stderr bytes.Buffer
			code := runDelete(tt.args, strings.NewReader(""), &stdout, &stderr, deleteDeps(gc))
			if code != 1 {
				t.Errorf("exit = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tt.errSubstr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.errSubstr)
			}
			if gc.deleteCalled {
				t.Error("DeleteRef should NOT have been called on arg validation failure")
			}
		})
	}
}

func TestRunDelete_not_found(t *testing.T) {
	gc := &fakeGitClient{deleteErr: gh.ErrNotFound}
	var stdout, stderr bytes.Buffer
	code := runDelete([]string{"--yes", "--key", "nope"}, strings.NewReader(""), &stdout, &stderr, deleteDeps(gc))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "not found in owner/repo") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

func TestRunDelete_errors(t *testing.T) {
	t.Run("resolveRepo error", func(t *testing.T) {
		deps := runDeps{
			resolveRepo: func(string) (*gh.Repo, error) { return nil, errors.New("boom") },
		}
		var stdout, stderr bytes.Buffer
		code := runDelete([]string{"--yes", "42"}, strings.NewReader(""), &stdout, &stderr, deps)
		if code != 1 {
			t.Fatalf("exit = %d", code)
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
		code := runDelete([]string{"--yes", "42"}, strings.NewReader(""), &stdout, &stderr, deps)
		if code != 1 {
			t.Fatalf("exit = %d", code)
		}
		if !strings.Contains(stderr.String(), "create git client: no auth") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("DeleteRef non-404 error", func(t *testing.T) {
		gc := &fakeGitClient{deleteErr: errors.New("api 500")}
		var stdout, stderr bytes.Buffer
		code := runDelete([]string{"--yes", "42"}, strings.NewReader(""), &stdout, &stderr, deleteDeps(gc))
		if code != 1 {
			t.Fatalf("exit = %d", code)
		}
		if !strings.Contains(stderr.String(), "delete ref: api 500") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runDelete([]string{"--nope"}, strings.NewReader(""), &stdout, &stderr, deleteDeps(&fakeGitClient{}))
		if code != 1 {
			t.Fatalf("exit = %d", code)
		}
	})
}

// TestRunDelete_subcommand_routing confirms that Run() → runWithDeps
// routes `delete` to runDelete. Exercises the subcommand switch in
// run.go without going through the upload flow.
func TestRunDelete_subcommand_routing(t *testing.T) {
	gc := &fakeGitClient{}
	deps := runDeps{
		resolveRepo: func(string) (*gh.Repo, error) {
			return &gh.Repo{Owner: "o", Name: "r"}, nil
		},
		newGitClient: func() (gitDataClient, error) { return gc, nil },
	}
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"delete", "--yes", "42"}, strings.NewReader(""), &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if !gc.deleteCalled {
		t.Error("DeleteRef not called via delete subcommand")
	}
}

func TestRunList_subcommand_routing(t *testing.T) {
	gc := &fakeGitClient{listRefs: []gh.RefEntry{}}
	deps := runDeps{
		resolveRepo: func(string) (*gh.Repo, error) {
			return &gh.Repo{Owner: "o", Name: "r"}, nil
		},
		newGitClient: func() (gitDataClient, error) { return gc, nil },
	}
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"list"}, strings.NewReader(""), &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if !gc.listCalled {
		t.Error("ListRefs not called via list subcommand")
	}
}
