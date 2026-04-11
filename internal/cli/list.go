package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// runList implements the `gh attach list` subcommand. It queries the
// GitHub Git Data matching-refs endpoint for refs under refs/uploads/
// and renders either an aligned text table (default) or a JSON array
// (`--json`). See README for the output contract.
func runList(args []string, stdout, stderr io.Writer, deps runDeps) int {
	fs := flag.NewFlagSet("gh-attach list", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoOverride := fs.String("repo", "", "Target repo as OWNER/NAME or a GitHub URL (default: origin of the current clone)")
	issuesOnly := fs.Bool("issues", false, "Show only issue-scoped refs (refs/uploads/issues/*)")
	miscOnly := fs.Bool("misc", false, "Show only ad-hoc refs (refs/uploads/misc/*)")
	asJSON := fs.Bool("json", false, "Emit JSON instead of a text table")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "List upload refs (refs/uploads/*) in a repo.\n\n")
		_, _ = fmt.Fprintf(stderr, "Usage:\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach list [flags]\n\n")
		_, _ = fmt.Fprintf(stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 1
	}

	if *issuesOnly && *miscOnly {
		_, _ = fmt.Fprintln(stderr, "error: --issues and --misc are mutually exclusive")
		return 1
	}

	repo, err := deps.resolveRepo(*repoOverride)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: resolve repo: %v\n", err)
		return 1
	}

	client, err := deps.newGitClient()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: create git client: %v\n", err)
		return 1
	}

	subPrefix := ""
	switch {
	case *issuesOnly:
		subPrefix = "issues"
	case *miscOnly:
		subPrefix = "misc"
	}

	entries, err := client.ListRefs(repo, subPrefix)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: list refs: %v\n", err)
		return 1
	}

	if *asJSON {
		// Always emit a valid JSON array, even when empty.
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if entries == nil {
			entries = []gh.RefEntry{}
		}
		if err := enc.Encode(entries); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 1
		}
		return 0
	}

	if len(entries) == 0 {
		_, _ = fmt.Fprintf(stderr, "No upload refs in %s\n", repo)
		return 0
	}

	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TARGET\tSHA\tNAMESPACE")
	for _, e := range entries {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", e.Target, shortSHA(e.SHA), e.Namespace)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintf(stderr, "\n%d upload ref(s) in %s\n", len(entries), repo)
	return 0
}

// shortSHA returns the first 7 characters of a git SHA, matching the
// abbreviated form gh/git clients use elsewhere. If the input is
// already shorter than 7 chars (e.g. a test fixture), it is returned
// unchanged.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
