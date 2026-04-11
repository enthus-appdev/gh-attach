package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// getDeps builds a runDeps where resolveRepo returns a canned repo,
// resolvePR returns 99 (so auto-detect tests land on a known number),
// and newGitClient returns the passed fakeGitClient.
func getDeps(gc *fakeGitClient) runDeps {
	return runDeps{
		resolveRepo: func(override string) (*gh.Repo, error) {
			return &gh.Repo{Owner: "owner", Name: "repo"}, nil
		},
		resolvePR: func(repo *gh.Repo) (int, error) {
			return 99, nil
		},
		newGitClient: func() (gitDataClient, error) { return gc, nil },
		stdin:        strings.NewReader(""),
	}
}

// sampleAttachments returns a deterministic pair of attachments used
// across multiple tests. Using the same fixture keeps assertions
// comparing against a single source of truth.
func sampleAttachments() []gh.Attachment {
	return []gh.Attachment{
		{Path: "shot.png", SHA: "blob-shot", Size: 18, Content: []byte("PNG-BYTES-FOR-SHOT")},
		{Path: "note.md", SHA: "blob-note", Size: 11, Content: []byte("hello world")},
	}
}

func TestRunGet_text_issue_mode(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: sampleAttachments(),
		getSHA:         "tip-commit-sha",
	}
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "42"}, &stdout, &stderr, getDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if gc.gotGetPath != "uploads/issues/42" {
		t.Errorf("refPath = %q, want uploads/issues/42", gc.gotGetPath)
	}

	// Both files should land in outDir with exact byte content.
	for _, att := range sampleAttachments() {
		dst := filepath.Join(outDir, att.Path)
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read %s: %v", dst, err)
		}
		if !bytes.Equal(got, att.Content) {
			t.Errorf("%s content = %q, want %q", att.Path, got, att.Content)
		}
	}

	// stdout should list the written paths, one per line.
	stdoutLines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(stdoutLines) != 2 {
		t.Errorf("stdout lines = %d, want 2:\n%s", len(stdoutLines), stdout.String())
	}
	for _, line := range stdoutLines {
		if !strings.HasPrefix(line, outDir) {
			t.Errorf("stdout line %q should start with outDir %q", line, outDir)
		}
	}

	// stderr should carry the progress + summary lines.
	if !strings.Contains(stderr.String(), "Downloading from #42 in owner/repo") {
		t.Errorf("stderr missing progress line: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Downloaded 2 file(s) to") {
		t.Errorf("stderr missing summary line: %s", stderr.String())
	}
}

func TestRunGet_text_key_mode(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: []gh.Attachment{
			{Path: "diagram.png", SHA: "b", Size: 3, Content: []byte("abc")},
		},
		getSHA: "sha",
	}
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "--key", "design-v2"}, &stdout, &stderr, getDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if gc.gotGetPath != "uploads/misc/design-v2" {
		t.Errorf("refPath = %q, want uploads/misc/design-v2", gc.gotGetPath)
	}
	if !strings.Contains(stderr.String(), "Downloading from misc/design-v2") {
		t.Errorf("stderr missing key target: %s", stderr.String())
	}
}

func TestRunGet_json(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: sampleAttachments(),
		getSHA:         "abc1234def5678",
	}
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "--json", "42"}, &stdout, &stderr, getDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	// JSON mode: stderr silent.
	if stderr.Len() != 0 {
		t.Errorf("stderr should be empty in JSON mode, got:\n%s", stderr.String())
	}

	var parsed downloadResult
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if parsed.Repo != "owner/repo" {
		t.Errorf("repo = %q", parsed.Repo)
	}
	if parsed.Target != "#42" {
		t.Errorf("target = %q", parsed.Target)
	}
	if parsed.Namespace != "issue" {
		t.Errorf("namespace = %q", parsed.Namespace)
	}
	if parsed.Number != 42 {
		t.Errorf("number = %d", parsed.Number)
	}
	if parsed.Key != "" {
		t.Errorf("key should be empty in issue mode, got %q", parsed.Key)
	}
	if parsed.Ref != "refs/uploads/issues/42" {
		t.Errorf("ref = %q", parsed.Ref)
	}
	if parsed.CommitSHA != "abc1234def5678" {
		t.Errorf("sha = %q", parsed.CommitSHA)
	}
	if parsed.OutputDir != outDir {
		t.Errorf("output_dir = %q, want %q", parsed.OutputDir, outDir)
	}
	if len(parsed.Files) != 2 {
		t.Fatalf("files = %v, want 2 entries", parsed.Files)
	}
	for _, f := range parsed.Files {
		if f.Path != filepath.Join(outDir, f.Name) {
			t.Errorf("file %q path = %q, want %q", f.Name, f.Path, filepath.Join(outDir, f.Name))
		}
	}
}

func TestRunGet_json_key_mode(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: []gh.Attachment{{Path: "f.png", SHA: "b", Size: 1, Content: []byte("x")}},
		getSHA:         "sha",
	}
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "--json", "--key", "design-v2"}, &stdout, &stderr, getDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}

	var parsed downloadResult
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, stdout.String())
	}
	if parsed.Namespace != "misc" {
		t.Errorf("namespace = %q, want misc", parsed.Namespace)
	}
	if parsed.Key != "design-v2" {
		t.Errorf("key = %q", parsed.Key)
	}
	if parsed.Number != 0 {
		t.Errorf("number should be zero in key mode, got %d", parsed.Number)
	}
	if parsed.Ref != "refs/uploads/misc/design-v2" {
		t.Errorf("ref = %q", parsed.Ref)
	}
}

func TestRunGet_auto_detect_PR(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: []gh.Attachment{{Path: "f.png", SHA: "b", Size: 1, Content: []byte("x")}},
		getSHA:         "sha",
	}
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir}, &stdout, &stderr, getDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	// getDeps.resolvePR returns 99.
	if gc.gotGetPath != "uploads/issues/99" {
		t.Errorf("refPath = %q, want uploads/issues/99", gc.gotGetPath)
	}
}

func TestRunGet_creates_missing_output_dir(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: []gh.Attachment{{Path: "f.png", SHA: "b", Size: 1, Content: []byte("x")}},
		getSHA:         "sha",
	}
	parent := t.TempDir()
	// Two levels deep, neither exists yet — MkdirAll should create both.
	outDir := filepath.Join(parent, "restored", "2026-04-11")

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "42"}, &stdout, &stderr, getDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outDir, "f.png")); err != nil {
		t.Errorf("expected file to be created under new dir: %v", err)
	}
}

func TestRunGet_existing_file_without_force(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: sampleAttachments(),
		getSHA:         "sha",
	}
	outDir := t.TempDir()
	// Pre-create one of the two files that sampleAttachments will try
	// to write. Without --force, runGet should fail without touching
	// either file (pre-flight atomicity).
	conflictPath := filepath.Join(outDir, "shot.png")
	if err := os.WriteFile(conflictPath, []byte("DO-NOT-TOUCH"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "42"}, &stdout, &stderr, getDeps(gc))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "refusing to overwrite") {
		t.Errorf("stderr: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Errorf("stderr should suggest --force: %s", stderr.String())
	}

	// Pre-flight means existing file is untouched AND the other file
	// was never written.
	got, _ := os.ReadFile(conflictPath)
	if string(got) != "DO-NOT-TOUCH" {
		t.Errorf("pre-existing file content = %q, want DO-NOT-TOUCH (atomicity)", got)
	}
	if _, err := os.Stat(filepath.Join(outDir, "note.md")); !os.IsNotExist(err) {
		t.Errorf("note.md should NOT exist (pre-flight failed before writing), stat err = %v", err)
	}
}

func TestRunGet_force_overwrites(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: sampleAttachments(),
		getSHA:         "sha",
	}
	outDir := t.TempDir()
	// Pre-create with old content. --force should replace with fresh
	// bytes from the fake client.
	oldContent := []byte("OLD-CONTENT")
	conflictPath := filepath.Join(outDir, "shot.png")
	if err := os.WriteFile(conflictPath, oldContent, 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "--force", "42"}, &stdout, &stderr, getDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	got, _ := os.ReadFile(conflictPath)
	if bytes.Equal(got, oldContent) {
		t.Errorf("file was not overwritten, still has old content")
	}
	if !bytes.Equal(got, []byte("PNG-BYTES-FOR-SHOT")) {
		t.Errorf("content = %q, want fresh bytes", got)
	}
}

func TestRunGet_not_found(t *testing.T) {
	gc := &fakeGitClient{getErr: gh.ErrNotFound}
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "--key", "nope"}, &stdout, &stderr, getDeps(gc))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "not found in owner/repo") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

func TestRunGet_empty_tree(t *testing.T) {
	// Ref exists but has zero files. Shouldn't error — just print a
	// note and exit 0. Also must not create anything in outDir.
	gc := &fakeGitClient{
		getAttachments: []gh.Attachment{},
		getSHA:         "sha",
	}
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "42"}, &stdout, &stderr, getDeps(gc))
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "No files in refs/uploads/issues/42") {
		t.Errorf("stderr: %s", stderr.String())
	}
	entries, _ := os.ReadDir(outDir)
	if len(entries) != 0 {
		t.Errorf("outDir should be empty, got %d entries", len(entries))
	}
}

func TestRunGet_repo_override_passed_through(t *testing.T) {
	var gotOverride string
	deps := runDeps{
		resolveRepo: func(override string) (*gh.Repo, error) {
			gotOverride = override
			return &gh.Repo{Owner: "o", Name: "r"}, nil
		},
		newGitClient: func() (gitDataClient, error) {
			return &fakeGitClient{
				getAttachments: []gh.Attachment{{Path: "f.png", Content: []byte("x")}},
				getSHA:         "sha",
			}, nil
		},
	}
	outDir := t.TempDir()

	var stdout, stderr bytes.Buffer
	runGet([]string{"--output", outDir, "--repo", "other/repo", "42"}, &stdout, &stderr, deps)
	if gotOverride != "other/repo" {
		t.Errorf("override = %q, want other/repo", gotOverride)
	}
}

func TestRunGet_arg_conflicts(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		errSubstr string
	}{
		{
			name:      "NUMBER + --key",
			args:      []string{"--key", "foo", "42"},
			errSubstr: "cannot combine NUMBER with --key",
		},
		{
			name:      "invalid --key (pure numeric)",
			args:      []string{"--key", "123"},
			errSubstr: "purely numeric",
		},
		{
			name:      "extra positional after NUMBER",
			args:      []string{"42", "extra"},
			errSubstr: "unexpected extra argument(s): extra",
		},
		{
			name:      "extra positional after --key",
			args:      []string{"--key", "foo", "garbage"},
			errSubstr: "unexpected extra argument(s): garbage",
		},
		{
			name:      "--repo without NUMBER or --key",
			args:      []string{"--repo", "owner/repo"},
			errSubstr: "--repo requires an explicit NUMBER or --key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runGet(tt.args, &stdout, &stderr, getDeps(&fakeGitClient{}))
			if code != 1 {
				t.Errorf("exit = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tt.errSubstr) {
				t.Errorf("stderr = %q, want substring %q", stderr.String(), tt.errSubstr)
			}
		})
	}
}

func TestRunGet_errors(t *testing.T) {
	t.Run("resolveRepo error", func(t *testing.T) {
		deps := runDeps{
			resolveRepo: func(string) (*gh.Repo, error) { return nil, errors.New("boom") },
		}
		var stdout, stderr bytes.Buffer
		code := runGet([]string{"42"}, &stdout, &stderr, deps)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "resolve repo: boom") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("resolvePR error", func(t *testing.T) {
		deps := runDeps{
			resolveRepo: func(string) (*gh.Repo, error) { return &gh.Repo{Owner: "o", Name: "r"}, nil },
			resolvePR:   func(*gh.Repo) (int, error) { return 0, errors.New("no PR") },
		}
		var stdout, stderr bytes.Buffer
		code := runGet(nil, &stdout, &stderr, deps)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "resolve PR: no PR") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("newGitClient error", func(t *testing.T) {
		deps := runDeps{
			resolveRepo:  func(string) (*gh.Repo, error) { return &gh.Repo{}, nil },
			newGitClient: func() (gitDataClient, error) { return nil, errors.New("no auth") },
		}
		var stdout, stderr bytes.Buffer
		code := runGet([]string{"42"}, &stdout, &stderr, deps)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "create git client: no auth") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("GetAttachments non-404 error", func(t *testing.T) {
		gc := &fakeGitClient{getErr: errors.New("api 500")}
		var stdout, stderr bytes.Buffer
		code := runGet([]string{"--output", t.TempDir(), "42"}, &stdout, &stderr, getDeps(gc))
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "get attachments: api 500") {
			t.Errorf("stderr: %s", stderr.String())
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runGet([]string{"--nope"}, &stdout, &stderr, getDeps(&fakeGitClient{}))
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
	})
}

func TestRunGet_mkdir_error(t *testing.T) {
	// --output points at a path that already exists as a regular file.
	// os.MkdirAll returns ENOTDIR in that case, which exercises the
	// "create output dir" error branch. Using an existing file (not a
	// permission trick) keeps the test portable across users and CI.
	gc := &fakeGitClient{
		getAttachments: []gh.Attachment{{Path: "f.png", Content: []byte("x")}},
		getSHA:         "sha",
	}
	tmp := t.TempDir()
	blockingFile := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(blockingFile, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", blockingFile, "42"}, &stdout, &stderr, getDeps(gc))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "create output dir") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

func TestRunGet_write_error(t *testing.T) {
	// Make the output directory read-only so the per-file WriteFile
	// fails after the pre-flight check passes. Covers the "error:
	// write ..." branch. chmod is restored in a cleanup so t.TempDir
	// can remove the directory after the test.
	gc := &fakeGitClient{
		getAttachments: []gh.Attachment{{Path: "f.png", Content: []byte("x")}},
		getSHA:         "sha",
	}
	outDir := t.TempDir()
	if err := os.Chmod(outDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outDir, 0755) })

	var stdout, stderr bytes.Buffer
	code := runGet([]string{"--output", outDir, "42"}, &stdout, &stderr, getDeps(gc))
	if code != 1 {
		t.Fatalf("exit = %d, want 1. stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "write ") {
		t.Errorf("stderr: %s", stderr.String())
	}
}

// TestRunGet_subcommand_routing confirms that Run() → runWithDeps
// routes `get` to runGet. Exercises the subcommand switch in run.go
// without going through the upload flow.
func TestRunGet_subcommand_routing(t *testing.T) {
	gc := &fakeGitClient{
		getAttachments: []gh.Attachment{{Path: "f.png", SHA: "b", Size: 1, Content: []byte("x")}},
		getSHA:         "sha",
	}
	outDir := t.TempDir()
	deps := runDeps{
		resolveRepo: func(string) (*gh.Repo, error) {
			return &gh.Repo{Owner: "o", Name: "r"}, nil
		},
		newGitClient: func() (gitDataClient, error) { return gc, nil },
	}
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"get", "--output", outDir, "42"}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit = %d, want 0. stderr=%s", code, stderr.String())
	}
	if !gc.getCalled {
		t.Error("GetAttachments not called via get subcommand")
	}
}

// TestHumanizeBytes exercises the byte-formatting helper directly so
// its boundary cases are pinned and extra-large values don't fall
// off the end of the units slice (int64 goes up to ~8 EiB, and
// humanizeBytes must not panic anywhere in that range).
func TestHumanizeBytes(t *testing.T) {
	const (
		kib = int64(1024)
		mib = kib * 1024
		gib = mib * 1024
		tib = gib * 1024
		pib = tib * 1024
		eib = pib * 1024
	)
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{kib, "1.0 KiB"},
		{kib + kib/2, "1.5 KiB"},
		{mib, "1.0 MiB"},
		{gib, "1.0 GiB"},
		{tib, "1.0 TiB"},
		{pib, "1.0 PiB"},
		{eib, "1.0 EiB"},
		// int64 max (~8 EiB) must render without panicking.
		{1<<63 - 1, "8.0 EiB"},
	}
	for _, tt := range tests {
		if got := humanizeBytes(tt.in); got != tt.want {
			t.Errorf("humanizeBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
