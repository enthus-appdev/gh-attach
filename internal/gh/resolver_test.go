package gh

import (
	"os/exec"
	"strings"
	"testing"
)

// withStubExec swaps the package-level execCommand for the duration
// of a test and returns a cleanup function. It's a small helper so
// every resolver test doesn't have to repeat the save/restore dance.
func withStubExec(t *testing.T, stub func(name string, args ...string) *exec.Cmd) {
	t.Helper()
	old := execCommand
	execCommand = stub
	t.Cleanup(func() { execCommand = old })
}

// stubOutput returns a stub execCommand that echoes the given string
// to stdout and exits 0. The trailing newline from `echo` is fine
// because the resolver parsers run strings.TrimSpace on the output.
func stubOutput(out string) func(name string, args ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", out)
	}
}

// stubFail returns a stub execCommand that always exits non-zero
// (the POSIX `false` command). Used to exercise the "shell-out
// failed" error path without caring about stdout contents.
func stubFail() func(name string, args ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}
}

// ---------------------------------------------------------------------
// ResolveRepo
// ---------------------------------------------------------------------

func TestResolveRepoFromGitRemote(t *testing.T) {
	t.Run("SSH remote", func(t *testing.T) {
		withStubExec(t, stubOutput("git@github.com:enthus-appdev/gh-attach.git"))
		repo, err := ResolveRepo("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.Owner != "enthus-appdev" || repo.Name != "gh-attach" {
			t.Errorf("repo = %+v, want enthus-appdev/gh-attach", repo)
		}
	})

	t.Run("HTTPS remote", func(t *testing.T) {
		withStubExec(t, stubOutput("https://github.com/enthus-appdev/gh-attach.git"))
		repo, err := ResolveRepo("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if repo.Owner != "enthus-appdev" || repo.Name != "gh-attach" {
			t.Errorf("repo = %+v, want enthus-appdev/gh-attach", repo)
		}
	})

	t.Run("git command fails", func(t *testing.T) {
		withStubExec(t, stubFail())
		_, err := ResolveRepo("")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "git remote get-url origin") {
			t.Errorf("error = %q", err.Error())
		}
	})

	t.Run("git remote output is unparseable", func(t *testing.T) {
		withStubExec(t, stubOutput("not-a-url"))
		_, err := ResolveRepo("")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "cannot parse remote URL") {
			t.Errorf("error = %q", err.Error())
		}
	})
}

func TestResolveRepoFromOverride(t *testing.T) {
	// None of these touch execCommand — they go through the override
	// branch in ResolveRepo. Stubbing execCommand to fail would still
	// pass if the override branch is taken.
	withStubExec(t, stubFail())

	tests := []struct {
		name      string
		override  string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{name: "plain owner/name", override: "foo/bar", wantOwner: "foo", wantName: "bar"},
		{name: "plain slug with github.com in name", override: "foo/github.com-bar", wantOwner: "foo", wantName: "github.com-bar"},
		{name: "SSH URL", override: "git@github.com:foo/bar.git", wantOwner: "foo", wantName: "bar"},
		{name: "HTTPS URL", override: "https://github.com/foo/bar", wantOwner: "foo", wantName: "bar"},
		{name: "HTTP URL", override: "http://github.com/foo/bar.git", wantOwner: "foo", wantName: "bar"},
		{name: "bare github.com/ prefix", override: "github.com/foo/bar", wantOwner: "foo", wantName: "bar"},
		{name: "ssh:// URL", override: "ssh://git@github.com/foo/bar.git", wantOwner: "foo", wantName: "bar"},
		{name: "invalid plain slug", override: "notvalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := ResolveRepo(tt.override)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo.Owner != tt.wantOwner || repo.Name != tt.wantName {
				t.Errorf("repo = %+v, want %s/%s", repo, tt.wantOwner, tt.wantName)
			}
		})
	}
}

// ---------------------------------------------------------------------
// ResolvePR
// ---------------------------------------------------------------------

func TestResolvePR(t *testing.T) {
	repo := &Repo{Owner: "owner", Name: "repo"}

	t.Run("happy path", func(t *testing.T) {
		withStubExec(t, stubOutput(`{"number": 42}`))
		n, err := ResolvePR(repo)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 42 {
			t.Errorf("number = %d, want 42", n)
		}
	})

	t.Run("gh command fails", func(t *testing.T) {
		withStubExec(t, stubFail())
		_, err := ResolvePR(repo)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no PR found for current branch") {
			t.Errorf("error = %q", err.Error())
		}
	})

	t.Run("invalid JSON output", func(t *testing.T) {
		withStubExec(t, stubOutput("not json"))
		_, err := ResolvePR(repo)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "parse PR response") {
			t.Errorf("error = %q", err.Error())
		}
	})

	t.Run("zero number is rejected", func(t *testing.T) {
		// The JSON is valid and parses, but Number is 0. ResolvePR
		// treats that as "no PR found".
		withStubExec(t, stubOutput(`{"number": 0}`))
		_, err := ResolvePR(repo)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no PR found for current branch") {
			t.Errorf("error = %q", err.Error())
		}
	})
}

// ---------------------------------------------------------------------
// ghAuthToken
// ---------------------------------------------------------------------

func TestGhAuthToken(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		withStubExec(t, stubOutput("gho_faketoken"))
		tok, err := ghAuthToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tok != "gho_faketoken" {
			t.Errorf("token = %q, want gho_faketoken", tok)
		}
	})

	t.Run("gh auth token fails", func(t *testing.T) {
		withStubExec(t, stubFail())
		_, err := ghAuthToken()
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "gh auth token") {
			t.Errorf("error = %q", err.Error())
		}
	})
}
