package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// runDelete implements the `gh attach delete` subcommand. It prompts
// for interactive confirmation by default (suppressed with --yes) and
// then calls gh.DeleteRef for either an issue-scoped ref (from a
// positional NUMBER) or an ad-hoc ref (from --key KEY).
func runDelete(args []string, stdin io.Reader, stdout, stderr io.Writer, deps runDeps) int {
	fs := flag.NewFlagSet("gh-attach delete", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoOverride := fs.String("repo", "", "Target repo as OWNER/NAME or a GitHub URL (default: origin of the current clone)")
	key := fs.String("key", "", "Ad-hoc target (refs/uploads/misc/KEY). Mutually exclusive with NUMBER.")
	yes := fs.Bool("yes", false, "Skip the interactive confirmation prompt")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Delete an upload ref (refs/uploads/*) from a repo.\n\n")
		_, _ = fmt.Fprintf(stderr, "Usage:\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach delete [flags] NUMBER\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach delete [flags] --key KEY\n\n")
		_, _ = fmt.Fprintf(stderr, "Pass a PR/issue NUMBER to delete refs/uploads/issues/NUMBER, or\n")
		_, _ = fmt.Fprintf(stderr, "--key KEY to delete refs/uploads/misc/KEY. Exactly one is required.\n\n")
		_, _ = fmt.Fprintf(stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 1
	}

	// Extract the positional NUMBER (if any). Unlike the upload flow,
	// delete takes at most one positional — reject any extras so the
	// user sees a clear error instead of silently dropping their input.
	positional := fs.Args()
	number, remaining := parseArgs(positional)
	if len(remaining) > 0 {
		_, _ = fmt.Fprintf(stderr, "error: unexpected extra argument(s): %s\n", strings.Join(remaining, " "))
		return 1
	}

	if number != 0 && *key != "" {
		_, _ = fmt.Fprintln(stderr, "error: cannot combine NUMBER with --key — they target different ref namespaces")
		return 1
	}
	if number == 0 && *key == "" {
		_, _ = fmt.Fprintln(stderr, "error: specify a PR/issue NUMBER or --key KEY")
		return 1
	}
	if *key != "" {
		if err := gh.ValidateKey(*key); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}

	repo, err := deps.resolveRepo(*repoOverride)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: resolve repo: %v\n", err)
		return 1
	}

	// Build the ref path and a user-facing target descriptor.
	var refPath, target string
	if *key != "" {
		refPath = "uploads/misc/" + *key
		target = "misc/" + *key
	} else {
		refPath = fmt.Sprintf("uploads/issues/%d", number)
		target = fmt.Sprintf("#%d", number)
	}

	// Confirmation prompt unless --yes.
	if !*yes {
		_, _ = fmt.Fprintf(stderr, "About to delete refs/%s in %s/%s.\n", refPath, repo.Owner, repo.Name)
		_, _ = fmt.Fprintf(stderr, "Proceed? [y/N]: ")
		answer, err := readConfirmation(stdin)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: could not read confirmation (stdin closed or non-interactive) — pass --yes to skip the prompt\n")
			return 1
		}
		if !isYes(answer) {
			_, _ = fmt.Fprintln(stderr, "Aborted")
			return 0
		}
	}

	client, err := deps.newGitClient()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: create git client: %v\n", err)
		return 1
	}

	if err := client.DeleteRef(repo, refPath); err != nil {
		if errors.Is(err, gh.ErrNotFound) {
			_, _ = fmt.Fprintf(stderr, "error: refs/%s: not found in %s/%s (%s)\n", refPath, repo.Owner, repo.Name, target)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "error: delete ref: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stderr, "Deleted refs/%s in %s/%s\n", refPath, repo.Owner, repo.Name)
	return 0
}

// readConfirmation reads a single line from stdin and returns it
// trimmed of leading/trailing whitespace. An EOF before any input is
// an error so callers can print a helpful "use --yes" message; an
// empty line (just `\n`) is treated as an empty answer, which isYes
// will correctly reject.
func readConfirmation(stdin io.Reader) (string, error) {
	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return strings.TrimSpace(scanner.Text()), nil
}

// isYes returns true only for explicit yes answers (y/Y/yes/YES).
// Everything else — including an empty line — is treated as a no.
func isYes(answer string) bool {
	a := strings.ToLower(strings.TrimSpace(answer))
	return a == "y" || a == "yes"
}
