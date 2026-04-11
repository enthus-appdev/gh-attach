package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// expandFiles resolves globs and verifies each file exists as a regular
// file. Directories matched by a glob are silently skipped (the caller
// passed a pattern, not an explicit directory path). Returns an error
// if a pattern matches nothing or if an expanded entry cannot be
// stat-ed.
func expandFiles(patterns []string) ([]string, error) {
	var files []string
	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", p, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no files matched: %s", p)
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				return nil, err
			}
			if info.IsDir() {
				continue
			}
			files = append(files, m)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no files found")
	}
	return files, nil
}

// validateName enforces that name is a safe basename to use when
// materializing a stdin upload. It rejects empty strings, path
// separators, `.` / `..`, NUL bytes, and anything longer than 255
// bytes. The goal is a value that (a) can't escape its temp dir via
// filepath.Join, (b) is legal on every common filesystem, and (c)
// survives being embedded in a git tree entry and a raw-blob URL
// without collisions or encoding weirdness.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("--name cannot be empty")
	}
	if len(name) > 255 {
		return fmt.Errorf("--name must be 255 bytes or fewer (got %d)", len(name))
	}
	if name == "." || name == ".." {
		return fmt.Errorf("--name cannot be %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("--name must be a basename, not a path (got %q)", name)
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("--name cannot contain NUL bytes")
	}
	return nil
}

// stdinFS is the filesystem subset that materializeStdinWith needs.
// Tests override these fields to exercise error branches (MkdirTemp,
// Create, and Close failures) that real OS fault injection can't
// reach cleanly. Production code always uses defaultStdinFS.
type stdinFS struct {
	mkdirTemp func(dir, pattern string) (string, error)
	create    func(name string) (io.WriteCloser, error)
}

// defaultStdinFS wires materializeStdin to the real os primitives.
// The create lambda exists because os.Create returns *os.File and we
// want the narrower io.WriteCloser so tests can supply fakes without
// implementing every *os.File method.
var defaultStdinFS = stdinFS{
	mkdirTemp: os.MkdirTemp,
	create: func(name string) (io.WriteCloser, error) {
		return os.Create(name)
	},
}

// materializeStdin drains stdin into a fresh file named `name` inside
// a new temp directory and returns the path plus a cleanup closure
// the caller should defer.
//
// This exists so the upload flow can support `gh attach --name X -`
// without refactoring gh.PushAttachments to accept an io.Reader: the
// temp path is a real file whose basename is exactly `name`, which
// is what PushAttachments feeds to filepath.Base when building the
// git tree entry.
//
// Callers must validate `name` (via validateName) before calling
// this — materializeStdin trusts it to be a safe basename.
func materializeStdin(stdin io.Reader, name string) (string, func(), error) {
	return materializeStdinWith(defaultStdinFS, stdin, name)
}

// materializeStdinWith is the testable variant that accepts an
// injectable stdinFS. Production calls go through materializeStdin,
// which passes defaultStdinFS. Tests pass a stdinFS with fakes to
// cover the MkdirTemp / Create / Close error branches.
func materializeStdinWith(fs stdinFS, stdin io.Reader, name string) (string, func(), error) {
	tmpDir, err := fs.mkdirTemp("", "gh-attach-stdin-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	tmpPath := filepath.Join(tmpDir, name)
	f, err := fs.create(tmpPath)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}
	if _, err := io.Copy(f, stdin); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, fmt.Errorf("read stdin: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close temp file: %w", err)
	}
	return tmpPath, cleanup, nil
}
