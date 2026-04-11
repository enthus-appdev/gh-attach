// Package cli wires together the gh-attach command-line front end: flag
// parsing, argument validation, file expansion, and orchestrating the
// upload + comment flow via the internal/gh package.
package cli

import "strconv"

// parseArgs separates an optional leading PR or issue number from the
// file list. A leading numeric argument with value > 0 is treated as
// the target number; everything else is consumed as files.
func parseArgs(args []string) (int, []string) {
	if len(args) == 0 {
		return 0, nil
	}
	if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
		return n, args[1:]
	}
	return 0, args
}
