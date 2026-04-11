package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

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
