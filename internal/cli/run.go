package cli

import (
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// gitDataClient is the subset of gh.GitDataClient that the cli layer
// needs. Defined as an interface here so tests can swap in a fake
// client that returns canned results without making HTTP calls. The
// real *gh.GitDataClient satisfies all three methods; individual test
// fakes typically only populate the one(s) the test under scrutiny
// actually exercises.
type gitDataClient interface {
	PushAttachments(repo *gh.Repo, refPath, commitMessage string, files []string) ([]gh.AttachmentPath, string, error)
	ListRefs(repo *gh.Repo, subPrefix string) ([]gh.RefEntry, error)
	DeleteRef(repo *gh.Repo, refPath string) error
}

// commentClient is the subset of gh.CommentClient that runUpload needs.
type commentClient interface {
	UpsertComment(repo *gh.Repo, prNumber int, paths []gh.AttachmentPath, commitSHA, title string) (string, error)
}

// runDeps bundles every external dependency that runUpload calls into
// so tests can replace them with in-process fakes. Production code uses
// defaultDeps(); tests build their own runDeps with stubs.
type runDeps struct {
	resolveRepo  func(override string) (*gh.Repo, error)
	resolvePR    func(repo *gh.Repo) (int, error)
	newGitClient func() (gitDataClient, error)
	newCmtClient func() (commentClient, error)
	expandFiles  func(patterns []string) ([]string, error)
}

// defaultDeps returns the real production dependencies — the thin
// wrappers around internal/gh and expandFiles. Tests never call this;
// they construct runDeps with fakes directly.
func defaultDeps() runDeps {
	return runDeps{
		resolveRepo: gh.ResolveRepo,
		resolvePR:   gh.ResolvePR,
		newGitClient: func() (gitDataClient, error) {
			return gh.NewGitDataClient()
		},
		newCmtClient: func() (commentClient, error) {
			return gh.NewCommentClient()
		},
		expandFiles: expandFiles,
	}
}

// Run parses args, dispatches to the right subcommand (or the default
// upload flow), and returns the process exit code. stdin is used by
// subcommands that need interactive confirmation (delete). stdout
// receives primary data output (rendered markdown, list output);
// stderr receives progress, prompts, URL lists, and errors. This is
// the function the command entry point calls; production code always
// goes through defaultDeps.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithDeps(args, stdin, stdout, stderr, defaultDeps())
}

// runWithDeps is the testable core of Run: it accepts the dependency
// struct so unit tests can inject fakes for the repo/PR resolvers, the
// git data client, the comment client, and the file expander. Run()
// calls it with defaultDeps() for real production runs.
func runWithDeps(args []string, stdin io.Reader, stdout, stderr io.Writer, deps runDeps) int {
	// Subcommand routing — if the first arg is exactly a known
	// subcommand name and not a flag, dispatch to it. Otherwise fall
	// through to the default upload flow. Users who want to upload a
	// file literally named "list" or "delete" can pass `./list` /
	// `./delete` to disambiguate.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "list":
			return runList(args[1:], stdout, stderr, deps)
		case "delete":
			return runDelete(args[1:], stdin, stdout, stderr, deps)
		}
	}

	fs := flag.NewFlagSet("gh-attach", flag.ContinueOnError)
	fs.SetOutput(stderr)

	title := fs.String("title", "", "Label for the upload group")
	postComment := fs.Bool("comment", false, "Also post (or upsert) the markdown as a PR/issue comment")
	repoOverride := fs.String("repo", "", "Target repo as OWNER/NAME or a GitHub URL (default: origin of the current clone)")
	key := fs.String("key", "", "Upload to an ad-hoc key under refs/uploads/misc/KEY instead of a PR/issue (mutually exclusive with NUMBER and --comment)")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Upload images to a GitHub PR or issue and print embeddable markdown to stdout.\n\n")
		_, _ = fmt.Fprintf(stderr, "Usage:\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach [flags] [NUMBER] FILE...\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach [flags] --key KEY FILE...\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach list   [flags]\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach delete [flags] NUMBER\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach delete [flags] --key KEY\n\n")
		_, _ = fmt.Fprintf(stderr, "If NUMBER is omitted on upload, it is auto-detected as a PR from the current branch.\n")
		_, _ = fmt.Fprintf(stderr, "NUMBER or --key must be passed explicitly whenever --repo is used.\n\n")
		_, _ = fmt.Fprintf(stderr, "Use --key to upload without a PR/issue — e.g. for a README image or a\n")
		_, _ = fmt.Fprintf(stderr, "not-yet-created issue. Ad-hoc uploads are stored under refs/uploads/misc/KEY\n")
		_, _ = fmt.Fprintf(stderr, "and are NOT auto-cleaned by the cleanup workflow. Use `gh attach list` to\n")
		_, _ = fmt.Fprintf(stderr, "inspect and `gh attach delete` to remove them.\n\n")
		_, _ = fmt.Fprintf(stderr, "By default, the rendered markdown is written to stdout and no comment is\n")
		_, _ = fmt.Fprintf(stderr, "posted. Pass --comment to also upsert the markdown as a PR/issue comment.\n\n")
		_, _ = fmt.Fprintf(stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		// flag.ContinueOnError already wrote the error + usage to stderr.
		return 1
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		fs.Usage()
		return 1
	}

	number, filePaths := parseArgs(remaining)
	if len(filePaths) == 0 {
		_, _ = fmt.Fprintln(stderr,"error: no image files specified")
		return 1
	}

	if err := runUpload(number, filePaths, *title, *postComment, *repoOverride, *key, stdout, stderr, deps); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// runUpload holds the post-flag-parse orchestration. It's split out
// from Run so tests can exercise it with already-parsed flag values
// and injected fake dependencies.
func runUpload(number int, filePaths []string, title string, postComment bool, repoOverride, key string, stdout, stderr io.Writer, deps runDeps) error {
	// Argument conflicts — fail fast before any network work.
	if number != 0 && key != "" {
		return fmt.Errorf("cannot combine NUMBER with --key — they target different ref namespaces")
	}
	if key != "" && postComment {
		return fmt.Errorf("--comment requires a PR/issue number and is incompatible with --key")
	}
	if key != "" {
		if err := gh.ValidateKey(key); err != nil {
			return err
		}
	}

	// Resolve repo context
	repo, err := deps.resolveRepo(repoOverride)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	// PR auto-detection uses `gh pr view` on the current branch, which
	// only makes sense inside a clone of the target repo. Any use of
	// --repo means the caller is being explicit about the target, so
	// require NUMBER (or --key) explicitly too.
	if number == 0 && key == "" && repoOverride != "" {
		return fmt.Errorf("--repo requires an explicit NUMBER or --key (PR auto-detection only works inside a clone of the target repo)")
	}

	// Resolve number from the current branch's PR if not provided.
	// Auto-detection only covers PRs (via `gh pr view`); to target an
	// issue, pass its number explicitly. Skipped entirely in key mode.
	if number == 0 && key == "" {
		number, err = deps.resolvePR(repo)
		if err != nil {
			return fmt.Errorf("resolve PR: %w", err)
		}
	}

	// Expand globs and validate files exist
	files, err := deps.expandFiles(filePaths)
	if err != nil {
		return err
	}

	// Build the ref path, commit message, and user-facing target
	// descriptor from the mode. gh.PushAttachments is namespace-agnostic
	// — it takes whatever refPath it's given.
	var refPath, commitMessage, target string
	if key != "" {
		refPath = "uploads/misc/" + key
		commitMessage = "upload for misc/" + key
		target = "misc/" + key
	} else {
		refPath = fmt.Sprintf("uploads/issues/%d", number)
		commitMessage = fmt.Sprintf("upload for #%d", number)
		target = fmt.Sprintf("#%d", number)
	}

	_, _ = fmt.Fprintf(stderr, "Uploading %d file(s) to %s in %s/%s...\n", len(files), target, repo.Owner, repo.Name)

	// Push images to refs/<refPath> via Git Data API
	client, err := deps.newGitClient()
	if err != nil {
		return fmt.Errorf("create git client: %w", err)
	}
	paths, commitSHA, err := client.PushAttachments(repo, refPath, commitMessage, files)
	if err != nil {
		return fmt.Errorf("push attachments: %w", err)
	}

	// Always emit the rendered markdown to stdout so the caller can embed
	// it anywhere — PR body, Slack, issue template, pbcopy, etc.
	markdown := strings.TrimSpace(gh.FormatSection(repo, paths, commitSHA, title))
	_, _ = fmt.Fprintln(stdout, markdown)

	// Always emit the raw, directly-embeddable URLs to stderr so the user
	// sees actionable references in their terminal even when stdout is piped.
	// Filename is URL-encoded so files containing spaces, `#`, `?`, or
	// non-ASCII characters produce valid, clickable URLs.
	_, _ = fmt.Fprintln(stderr, "Uploaded:")
	for _, p := range paths {
		_, _ = fmt.Fprintf(stderr, "  https://github.com/%s/%s/blob/%s/%s?raw=true\n", repo.Owner, repo.Name, commitSHA, url.PathEscape(p.Path))
	}

	// Opt-in side-effect: also post/upsert the markdown as a PR/issue comment.
	if postComment {
		cc, err := deps.newCmtClient()
		if err != nil {
			return fmt.Errorf("create comment client: %w", err)
		}
		commentURL, err := cc.UpsertComment(repo, number, paths, commitSHA, title)
		if err != nil {
			return fmt.Errorf("upsert comment: %w", err)
		}
		_, _ = fmt.Fprintf(stderr, "Commented: %s\n", commentURL)
	}

	return nil
}
