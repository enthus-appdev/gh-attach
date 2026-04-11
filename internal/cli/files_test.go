package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// errReader is an io.Reader that always fails on Read. Used to
// exercise error paths in code that copies from an io.Reader.
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, errors.New("boom") }

func TestExpandFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Seed files + a subdirectory for tests to reference.
	for _, name := range []string{"a.png", "b.png", "readme.md"} {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	// File inside subdir so "subdir/*" glob returns real files.
	if err := os.WriteFile(filepath.Join(subDir, "c.png"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("single existing file", func(t *testing.T) {
		in := []string{filepath.Join(tmpDir, "a.png")}
		got, err := expandFiles(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, in) {
			t.Errorf("got %v, want %v", got, in)
		}
	})

	t.Run("glob matching multiple files", func(t *testing.T) {
		got, err := expandFiles([]string{filepath.Join(tmpDir, "*.png")})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		sort.Strings(got)
		want := []string{
			filepath.Join(tmpDir, "a.png"),
			filepath.Join(tmpDir, "b.png"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("multiple patterns accumulate", func(t *testing.T) {
		got, err := expandFiles([]string{
			filepath.Join(tmpDir, "a.png"),
			filepath.Join(tmpDir, "b.png"),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{
			filepath.Join(tmpDir, "a.png"),
			filepath.Join(tmpDir, "b.png"),
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("glob with no matches errors", func(t *testing.T) {
		_, err := expandFiles([]string{filepath.Join(tmpDir, "*.jpg")})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no files matched") {
			t.Errorf("error = %q, want 'no files matched'", err.Error())
		}
	})

	t.Run("invalid glob syntax errors", func(t *testing.T) {
		// An unclosed bracket is a filepath.Glob syntax error.
		_, err := expandFiles([]string{"[broken"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "invalid glob") {
			t.Errorf("error = %q, want 'invalid glob'", err.Error())
		}
	})

	t.Run("directories in glob result are skipped", func(t *testing.T) {
		// tmpDir/* matches a.png, b.png, readme.md, and subdir/.
		// Directories should be silently skipped.
		got, err := expandFiles([]string{filepath.Join(tmpDir, "*")})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, p := range got {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatalf("stat %s: %v", p, err)
			}
			if info.IsDir() {
				t.Errorf("expandFiles returned a directory: %s", p)
			}
		}
		// We expect exactly 3 files (a.png, b.png, readme.md), not subdir.
		if len(got) != 3 {
			t.Errorf("got %d entries, want 3 (directories should be skipped)", len(got))
		}
	})

	t.Run("only-directory match returns no files error", func(t *testing.T) {
		// subdir doesn't match anything except itself at this level.
		_, err := expandFiles([]string{subDir})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no files found") {
			t.Errorf("error = %q, want 'no files found'", err.Error())
		}
	})

	t.Run("stat error on unreadable path", func(t *testing.T) {
		// Create a symlink to a non-existent target. filepath.Glob will
		// return the symlink path; os.Stat will fail with "no such file".
		// Covers the `info, err := os.Stat(m); if err != nil { return }` path.
		dangling := filepath.Join(tmpDir, "dangling-link")
		if err := os.Symlink(filepath.Join(tmpDir, "does-not-exist"), dangling); err != nil {
			t.Fatal(err)
		}
		_, err := expandFiles([]string{dangling})
		if err == nil {
			t.Fatal("expected error from stat on dangling symlink, got nil")
		}
	})

	t.Run("nonexistent literal path returns no match", func(t *testing.T) {
		// Glob of a literal path that doesn't exist returns zero matches
		// (no syntax error) — should produce "no files matched".
		_, err := expandFiles([]string{filepath.Join(tmpDir, "does-not-exist.png")})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no files matched") {
			t.Errorf("error = %q, want 'no files matched'", err.Error())
		}
	})
}

func TestValidateName(t *testing.T) {
	t.Run("valid basenames are accepted", func(t *testing.T) {
		good := []string{
			"shot.png",
			"image.jpg",
			"Screen Shot 2026.png",
			".hidden.png",
			"with-dashes_and.dots.1.png",
			"übernote.md", // non-ASCII is fine
			strings.Repeat("x", 255),
		}
		for _, n := range good {
			if err := validateName(n); err != nil {
				t.Errorf("validateName(%q) = %v, want nil", n, err)
			}
		}
	})
	t.Run("invalid basenames are rejected", func(t *testing.T) {
		tests := []struct {
			in        string
			errSubstr string
		}{
			{"", "cannot be empty"},
			{".", `cannot be "."`},
			{"..", `cannot be ".."`},
			// Unix path separators
			{"foo/bar.png", "must be a basename"},
			{"/abs/path.png", "must be a basename"},
			{"foo\\bar.png", "must be a basename"},
			// Windows-reserved characters — rejected so the basename is
			// legal on every common filesystem without per-platform
			// escaping when embedded in URLs or git tree entries.
			{"foo<bar.png", "legal filesystem characters"},
			{"foo>bar.png", "legal filesystem characters"},
			{"foo:bar.png", "legal filesystem characters"},
			{`foo"bar.png`, "legal filesystem characters"},
			{"foo|bar.png", "legal filesystem characters"},
			{"foo?bar.png", "legal filesystem characters"},
			{"foo*bar.png", "legal filesystem characters"},
			{"has\x00nul.png", "NUL bytes"},
			{strings.Repeat("x", 256), "255 bytes or fewer"},
		}
		for _, tt := range tests {
			err := validateName(tt.in)
			if err == nil {
				t.Errorf("validateName(%q) = nil, want error", tt.in)
				continue
			}
			if !strings.Contains(err.Error(), tt.errSubstr) {
				t.Errorf("validateName(%q) = %q, want substring %q", tt.in, err.Error(), tt.errSubstr)
			}
		}
	})
}

func TestMaterializeStdin(t *testing.T) {
	t.Run("copies bytes into a file named by the basename", func(t *testing.T) {
		path, cleanup, err := materializeStdin(strings.NewReader("hello world"), "test.bin")
		if err != nil {
			t.Fatalf("materializeStdin: %v", err)
		}
		defer cleanup()
		if got := filepath.Base(path); got != "test.bin" {
			t.Errorf("basename = %q, want test.bin", got)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(data) != "hello world" {
			t.Errorf("content = %q, want %q", data, "hello world")
		}
	})
	t.Run("empty input produces an empty file", func(t *testing.T) {
		path, cleanup, err := materializeStdin(strings.NewReader(""), "empty.bin")
		if err != nil {
			t.Fatalf("materializeStdin: %v", err)
		}
		defer cleanup()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Size() != 0 {
			t.Errorf("size = %d, want 0", info.Size())
		}
	})
	t.Run("cleanup removes the temp directory", func(t *testing.T) {
		path, cleanup, err := materializeStdin(strings.NewReader("x"), "f.bin")
		if err != nil {
			t.Fatalf("materializeStdin: %v", err)
		}
		cleanup()
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("expected temp file to be removed, stat err = %v", err)
		}
		// Also check the parent directory is gone.
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Errorf("expected temp dir to be removed, stat err = %v", err)
		}
	})
	t.Run("read error is wrapped and temp dir is cleaned up", func(t *testing.T) {
		// A reader that always errors covers the io.Copy error branch
		// and confirms we wrap the underlying cause with %w.
		path, cleanup, err := materializeStdin(errReader{}, "f.bin")
		if err == nil {
			cleanup()
			t.Fatal("expected error, got nil")
		}
		if cleanup != nil {
			t.Error("expected cleanup to be nil on error (caller cannot defer it)")
		}
		if path != "" {
			t.Errorf("expected empty path on error, got %q", path)
		}
		if !strings.Contains(err.Error(), "read stdin") {
			t.Errorf("error = %q, want substring 'read stdin'", err.Error())
		}
		if unwrapped := errors.Unwrap(err); unwrapped == nil || unwrapped.Error() != "boom" {
			t.Errorf("expected wrapped cause %q, got %v", "boom", unwrapped)
		}
	})
}

// fakeWriteCloser is a test double for the io.WriteCloser returned by
// stdinFS.create. Callers pick which of Write or Close should fail by
// setting writeErr / closeErr; unset methods behave as no-ops.
type fakeWriteCloser struct {
	writeErr error
	closeErr error
	written  int
}

func (f *fakeWriteCloser) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.written += len(p)
	return len(p), nil
}
func (f *fakeWriteCloser) Close() error { return f.closeErr }

// TestMaterializeStdinWith exercises the stdinFS injection seam so the
// MkdirTemp / Create / Close error branches — which OS-level fault
// injection can't reach cleanly — get real coverage. The happy path
// and the io.Copy error branch are covered by TestMaterializeStdin
// above, which calls the real materializeStdin wrapper.
func TestMaterializeStdinWith(t *testing.T) {
	t.Run("mkdirTemp error", func(t *testing.T) {
		fs := stdinFS{
			mkdirTemp: func(_, _ string) (string, error) {
				return "", errors.New("no space")
			},
			create: func(_ string) (io.WriteCloser, error) {
				t.Fatal("create should not be called when mkdirTemp fails")
				return nil, nil
			},
		}
		path, cleanup, err := materializeStdinWith(fs, strings.NewReader("x"), "f.bin")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "create temp dir: no space") {
			t.Errorf("error = %q, want 'create temp dir: no space'", err.Error())
		}
		if path != "" {
			t.Errorf("expected empty path, got %q", path)
		}
		if cleanup != nil {
			t.Error("expected nil cleanup (caller cannot defer it)")
		}
	})

	t.Run("create error cleans up the temp dir", func(t *testing.T) {
		// Use real MkdirTemp so we can assert the created dir gets
		// removed when create fails — a leak here would drop temp
		// directories on every failed upload.
		tmpDirHolder := ""
		fs := stdinFS{
			mkdirTemp: func(dir, pattern string) (string, error) {
				d, err := os.MkdirTemp(dir, pattern)
				tmpDirHolder = d
				return d, err
			},
			create: func(_ string) (io.WriteCloser, error) {
				return nil, errors.New("permission denied")
			},
		}
		_, _, err := materializeStdinWith(fs, strings.NewReader("x"), "f.bin")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "create temp file: permission denied") {
			t.Errorf("error = %q", err.Error())
		}
		if _, statErr := os.Stat(tmpDirHolder); !os.IsNotExist(statErr) {
			t.Errorf("temp dir %q should have been cleaned up, stat err = %v", tmpDirHolder, statErr)
		}
	})

	t.Run("close error is wrapped", func(t *testing.T) {
		// A WriteCloser whose Write succeeds but Close fails drives the
		// final `if err := f.Close(); err != nil` branch.
		tmpDirHolder := ""
		fs := stdinFS{
			mkdirTemp: func(dir, pattern string) (string, error) {
				d, err := os.MkdirTemp(dir, pattern)
				tmpDirHolder = d
				return d, err
			},
			create: func(_ string) (io.WriteCloser, error) {
				return &fakeWriteCloser{closeErr: errors.New("disk full")}, nil
			},
		}
		_, _, err := materializeStdinWith(fs, strings.NewReader("payload"), "f.bin")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "close temp file: disk full") {
			t.Errorf("error = %q", err.Error())
		}
		if _, statErr := os.Stat(tmpDirHolder); !os.IsNotExist(statErr) {
			t.Errorf("temp dir %q should have been cleaned up, stat err = %v", tmpDirHolder, statErr)
		}
	})

	t.Run("write error from the fake WriteCloser also exercises io.Copy error", func(t *testing.T) {
		// Belt-and-suspenders for the io.Copy branch via a failing
		// WriteCloser instead of a failing Reader. Confirms the error
		// wrapping is symmetric regardless of which side fails.
		tmpDirHolder := ""
		fs := stdinFS{
			mkdirTemp: func(dir, pattern string) (string, error) {
				d, err := os.MkdirTemp(dir, pattern)
				tmpDirHolder = d
				return d, err
			},
			create: func(_ string) (io.WriteCloser, error) {
				return &fakeWriteCloser{writeErr: errors.New("short write")}, nil
			},
		}
		_, _, err := materializeStdinWith(fs, strings.NewReader("payload"), "f.bin")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "read stdin: short write") {
			t.Errorf("error = %q", err.Error())
		}
		if _, statErr := os.Stat(tmpDirHolder); !os.IsNotExist(statErr) {
			t.Errorf("temp dir should have been cleaned up, stat err = %v", statErr)
		}
	})
}
