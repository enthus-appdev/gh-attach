package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
