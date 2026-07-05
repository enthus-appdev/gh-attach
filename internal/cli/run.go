package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// uploadResult is the shape emitted to stdout when --json is passed.
// It bundles everything a script might want: the repo context, the
// namespace-tagged target, the fast-forward commit SHA, every
// uploaded file's basename + raw-blob URL, the rendered markdown
// (the same bare section that non-JSON mode prints), and the
// upserted comment URL when --comment was supplied and succeeded.
//
// The `omitempty` tags keep the output predictable: consumers see
// `number` for issue-mode uploads and `key` for misc-mode uploads
// but never both, and `comment_url` appears only when relevant.
type uploadResult struct {
	Repo       string       `json:"repo"`
	Target     string       `json:"target"`
	Namespace  string       `json:"namespace"`
	Number     int          `json:"number,omitempty"`
	Key        string       `json:"key,omitempty"`
	Ref        string       `json:"ref"`
	CommitSHA  string       `json:"sha"`
	Files      []uploadFile `json:"files"`
	Markdown   string       `json:"markdown"`
	CommentURL string       `json:"comment_url,omitempty"`
}

// uploadFile is one entry in uploadResult.Files. Name is the raw
// basename (for display / alt text); URL is the URL-encoded
// blob/<sha>/<file>?raw=true link that works in a browser.
type uploadFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// gitDataClient is the subset of gh.GitDataClient that the cli layer
// needs. Defined as an interface here so tests can swap in a fake
// client that returns canned results without making HTTP calls. The
// real *gh.GitDataClient satisfies all four methods; individual test
// fakes typically only populate the one(s) the test under scrutiny
// actually exercises.
type gitDataClient interface {
	PushAttachments(repo *gh.Repo, refPath, commitMessage string, files []string) ([]gh.AttachmentPath, string, error)
	ListRefs(repo *gh.Repo, subPrefix string) ([]gh.RefEntry, error)
	DeleteRef(repo *gh.Repo, refPath string) error
	GetAttachments(repo *gh.Repo, refPath string) ([]gh.Attachment, string, error)
}

// commentClient is the subset of gh.CommentClient that runUpload needs.
type commentClient interface {
	UpsertComment(repo *gh.Repo, prNumber int, paths []gh.AttachmentPath, commitSHA, title string) (string, error)
}

// runDeps bundles every external dependency that runUpload, runList,
// and runDelete call into so tests can replace them with in-process
// fakes. `stdin` is in here too — it's a source of user input that
// both the delete confirmation prompt and the upload stdin path need
// to read, and tests need to stub with strings.NewReader(...). Putting
// it on runDeps keeps the runWithDeps / runDelete / runUpload
// signatures short and consistent.
//
// Production code uses defaultDeps(); tests build their own runDeps
// with stubs.
type runDeps struct {
	resolveRepo  func(override string) (*gh.Repo, error)
	resolvePR    func(repo *gh.Repo) (int, error)
	newGitClient func() (gitDataClient, error)
	newCmtClient func() (commentClient, error)
	expandFiles  func(patterns []string) ([]string, error)
	stdin        io.Reader
}

// uploadOptions bundles the parsed-flag state that runUpload consumes.
// Keeping it as a struct (rather than a long positional signature)
// makes the single caller in runWithDeps explicit about which parsed
// value maps to which semantic field, and lets future flags be added
// without touching every call site.
//
// Field semantics:
//   - number: PR/issue number (0 when --key is used or auto-detected from branch)
//   - filePaths: positional file args, or ["-"] to read from deps.stdin
//   - title: --title label for the markdown section
//   - postComment: --comment — also upsert the markdown as a PR/issue comment
//   - repoOverride: --repo OWNER/NAME (default: origin of the current clone)
//   - key: --key KEY — ad-hoc upload under refs/uploads/misc/KEY
//   - name: --name BASENAME — basename to use when reading from stdin
//   - asJSON: --json — emit uploadResult JSON instead of markdown
type uploadOptions struct {
	number       int
	filePaths    []string
	title        string
	postComment  bool
	repoOverride string
	key          string
	name         string
	asJSON       bool
	gif          bool  // --gif: assemble filePaths into one animated GIF before upload
	delayMS      int   // --delay: per-frame delay (ms) in gif mode
	numColors    int   // --colors: per-frame palette size in gif mode
	maxFrames    int   // --max-frames: cap on frames assembled in gif mode
	sizeCeiling  int64 // --size-ceiling: byte ceiling triggering a reduced re-encode in gif mode
}

// defaultDeps returns the real production dependencies — the thin
// wrappers around internal/gh, expandFiles, and os.Stdin. Tests never
// call this; they construct runDeps with fakes directly.
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
		stdin:       os.Stdin,
	}
}

// Run parses args, dispatches to the right subcommand (or the default
// upload flow), and returns the process exit code. stdin is threaded
// through defaultDeps so subcommands that need to read user input
// (delete confirmation prompts, upload from `-`) can reach it via
// deps.stdin. stdout receives primary data output (rendered markdown,
// list output, JSON); stderr receives progress, prompts, URL lists,
// and errors.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	deps := defaultDeps()
	deps.stdin = stdin
	return runWithDeps(args, stdout, stderr, deps)
}

// runWithDeps is the testable core of Run: it accepts the dependency
// struct so unit tests can inject fakes for the repo/PR resolvers, the
// git data client, the comment client, the file expander, and stdin.
// Run() calls it with defaultDeps() for real production runs.
func runWithDeps(args []string, stdout, stderr io.Writer, deps runDeps) int {
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
			return runDelete(args[1:], stdout, stderr, deps)
		case "get":
			return runGet(args[1:], stdout, stderr, deps)
		}
	}

	fs := flag.NewFlagSet("gh-attach", flag.ContinueOnError)
	fs.SetOutput(stderr)

	title := fs.String("title", "", "Label for the upload group")
	postComment := fs.Bool("comment", false, "Also post (or upsert) the markdown as a PR/issue comment")
	repoOverride := fs.String("repo", "", "Target repo as OWNER/NAME or a GitHub URL (default: origin of the current clone)")
	key := fs.String("key", "", "Upload to an ad-hoc key under refs/uploads/misc/KEY instead of a PR/issue (mutually exclusive with NUMBER and --comment)")
	asJSON := fs.Bool("json", false, "Emit a JSON result object instead of the markdown table (suppresses stderr progress + URL list)")
	name := fs.String("name", "", "Basename to use when reading file bytes from stdin (`-`) or naming a --gif output. Required with stdin, rejected otherwise (except with --gif).")
	gifMode := fs.Bool("gif", false, "Assemble the input image frames into one animated GIF and upload that instead of the individual frames")
	delayMS := fs.Int("delay", 80, "Per-frame delay in milliseconds for --gif (GIF granularity is 10ms; range 20-655350)")
	colors := fs.Int("colors", 256, "Palette colors per frame for --gif (2–256)")
	maxFrames := fs.Int("max-frames", 300, "Cap on frames assembled by --gif; excess frames are evenly sampled out (0 = no cap)")
	sizeCeiling := fs.Int64("size-ceiling", 5*1024*1024, "Byte ceiling for the --gif output; over it, re-encode once with fewer colors/frames (0 = no ceiling)")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Upload images to a GitHub PR or issue and print embeddable markdown to stdout.\n\n")
		_, _ = fmt.Fprintf(stderr, "Usage:\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach [flags] [NUMBER] FILE...\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach [flags] --key KEY FILE...\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach [flags] --name BASENAME [NUMBER] -\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach [flags] --name BASENAME --key KEY -\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach list   [flags]\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach delete [flags] NUMBER\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach delete [flags] --key KEY\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach get    [flags] [NUMBER]\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach get    [flags] --key KEY\n\n")
		_, _ = fmt.Fprintf(stderr, "If NUMBER is omitted on upload, it is auto-detected as a PR from the current branch.\n")
		_, _ = fmt.Fprintf(stderr, "NUMBER or --key must be passed explicitly whenever --repo is used.\n\n")
		_, _ = fmt.Fprintf(stderr, "Use --key to upload without a PR/issue — e.g. for a README image or a\n")
		_, _ = fmt.Fprintf(stderr, "not-yet-created issue. Ad-hoc uploads are stored under refs/uploads/misc/KEY\n")
		_, _ = fmt.Fprintf(stderr, "and are NOT auto-cleaned by the cleanup workflow. Use `gh attach list` to\n")
		_, _ = fmt.Fprintf(stderr, "inspect and `gh attach delete` to remove them.\n\n")
		_, _ = fmt.Fprintf(stderr, "Pass `-` as the single FILE argument with --name BASENAME to read file\n")
		_, _ = fmt.Fprintf(stderr, "bytes from stdin (e.g. `screencapture -t png - | gh attach --name shot.png 42 -`).\n\n")
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
		_, _ = fmt.Fprintln(stderr, "error: no image files specified")
		return 1
	}

	opts := uploadOptions{
		number:       number,
		filePaths:    filePaths,
		title:        *title,
		postComment:  *postComment,
		repoOverride: *repoOverride,
		key:          *key,
		name:         *name,
		asJSON:       *asJSON,
		gif:          *gifMode,
		delayMS:      *delayMS,
		numColors:    *colors,
		maxFrames:    *maxFrames,
		sizeCeiling:  *sizeCeiling,
	}
	if err := runUpload(opts, stdout, stderr, deps); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// runUpload holds the post-flag-parse orchestration. It's split out
// from Run so tests can exercise it with already-parsed flag values
// and injected fake dependencies.
//
// In default (non-JSON) mode, stdout receives the rendered markdown
// and stderr receives progress + the directly-embeddable URL list
// (and, with --comment, a final `Commented:` line).
//
// In --json mode, stdout receives a single JSON uploadResult object
// and stderr is silent unless something errors out. Consumers that
// want partial-success handling (upload succeeded, --comment failed)
// should break the operation into two steps: `gh attach ...` then
// `gh pr comment` — a --comment failure here still exits 1 with no
// JSON on stdout, by design.
func runUpload(opts uploadOptions, stdout, stderr io.Writer, deps runDeps) error {
	// number is the only field we mutate locally (auto-detected from
	// the current branch when the caller didn't pass one). Everything
	// else is read-only and accessed via opts.X.
	number := opts.number

	// Argument conflicts — fail fast before any network work.
	if number != 0 && opts.key != "" {
		return fmt.Errorf("cannot combine NUMBER with --key — they target different ref namespaces")
	}
	if opts.key != "" && opts.postComment {
		return fmt.Errorf("--comment requires a PR/issue number and is incompatible with --key")
	}
	if opts.key != "" {
		if err := gh.ValidateKey(opts.key); err != nil {
			return err
		}
	}

	// Stdin mode detection + validation. Exactly one file arg of "-"
	// means "read file bytes from deps.stdin"; anything else is
	// either a normal upload or a user error we want to flag
	// explicitly (rather than letting expandFiles produce an opaque
	// "no files matched" result).
	useStdin := len(opts.filePaths) == 1 && opts.filePaths[0] == "-"
	if opts.gif && useStdin {
		return fmt.Errorf("--gif reads frame files from disk and cannot read from stdin (`-`)")
	}
	// Reject out-of-range gif tunables at the boundary rather than
	// silently coercing them (the flag help promises these ranges).
	if opts.gif {
		if opts.numColors < 2 || opts.numColors > 256 {
			return fmt.Errorf("--colors must be between 2 and 256 (got %d)", opts.numColors)
		}
		if opts.delayMS < 20 {
			return fmt.Errorf("--delay must be at least 20 ms (got %d)", opts.delayMS)
		}
		// GIF stores each frame's delay as a 16-bit count of
		// centiseconds; gifenc rounds delayMS/10 into that field, so
		// anything past 655350ms would wrap instead of playing slower.
		if opts.delayMS > 655350 {
			return fmt.Errorf("--delay must be at most 655350 ms (got %d)", opts.delayMS)
		}
	}
	if !useStdin {
		for _, p := range opts.filePaths {
			if p == "-" {
				return fmt.Errorf("`-` must be the only file argument when reading from stdin")
			}
		}
		// --name labels the stdin upload's basename or the --gif output
		// name; it has no meaning for a normal multi-file disk upload.
		if opts.name != "" && !opts.gif {
			return fmt.Errorf("--name is only valid when reading from stdin (`-`) or with --gif")
		}
		if opts.gif && opts.name != "" {
			if err := validateName(opts.name); err != nil {
				return err
			}
		}
	} else {
		if opts.name == "" {
			return fmt.Errorf("--name is required when reading from stdin (`-`)")
		}
		if err := validateName(opts.name); err != nil {
			return err
		}
	}

	// Resolve repo context
	repo, err := deps.resolveRepo(opts.repoOverride)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	// PR auto-detection uses `gh pr view` on the current branch, which
	// only makes sense inside a clone of the target repo. Any use of
	// --repo means the caller is being explicit about the target, so
	// require NUMBER (or --key) explicitly too.
	if number == 0 && opts.key == "" && opts.repoOverride != "" {
		return fmt.Errorf("--repo requires an explicit NUMBER or --key (PR auto-detection only works inside a clone of the target repo)")
	}

	// Resolve number from the current branch's PR if not provided.
	// Auto-detection only covers PRs (via `gh pr view`); to target an
	// issue, pass its number explicitly. Skipped entirely in key mode.
	if number == 0 && opts.key == "" {
		number, err = deps.resolvePR(repo)
		if err != nil {
			return fmt.Errorf("resolve PR: %w", err)
		}
	}

	// Expand globs and validate files exist — or, in stdin mode,
	// drain stdin into a temp file under the user-chosen basename.
	// Skipping expandFiles in stdin mode avoids interpreting `name`
	// as a glob pattern and keeps gh.PushAttachments unchanged
	// (it still receives a real filesystem path).
	var files []string
	if useStdin {
		tmpPath, cleanup, stdinErr := materializeStdin(deps.stdin, opts.name)
		if stdinErr != nil {
			return stdinErr
		}
		defer cleanup()
		files = []string{tmpPath}
	} else {
		files, err = deps.expandFiles(opts.filePaths)
		if err != nil {
			return err
		}
	}

	// --gif: collapse the frame files into a single animated GIF and
	// upload that. Downstream (push, render, comment) is unchanged —
	// a .gif is already an inline-image extension in comment.go.
	if opts.gif {
		gifPath, cleanup, warning, gerr := assembleGIF(files, gifAssembleOptions{
			name:        opts.name,
			delayMS:     opts.delayMS,
			numColors:   opts.numColors,
			maxFrames:   opts.maxFrames,
			sizeCeiling: opts.sizeCeiling,
		})
		if gerr != nil {
			return fmt.Errorf("assemble gif: %w", gerr)
		}
		defer cleanup()
		if warning != "" && !opts.asJSON {
			_, _ = fmt.Fprintf(stderr, "warning: %s\n", warning)
		}
		files = []string{gifPath}
	}

	// Build the ref path, commit message, and user-facing target
	// descriptor from the mode. gh.PushAttachments is namespace-agnostic
	// — it takes whatever refPath it's given.
	var refPath, commitMessage, target string
	if opts.key != "" {
		refPath = "uploads/misc/" + opts.key
		commitMessage = "upload for misc/" + opts.key
		target = "misc/" + opts.key
	} else {
		refPath = fmt.Sprintf("uploads/issues/%d", number)
		commitMessage = fmt.Sprintf("upload for #%d", number)
		target = fmt.Sprintf("#%d", number)
	}

	// Progress line (suppressed in JSON mode so stderr is quiet).
	if !opts.asJSON {
		_, _ = fmt.Fprintf(stderr, "Uploading %d file(s) to %s in %s...\n", len(files), target, repo)
	}

	// Push images to refs/<refPath> via Git Data API
	client, err := deps.newGitClient()
	if err != nil {
		return fmt.Errorf("create git client: %w", err)
	}
	paths, commitSHA, err := client.PushAttachments(repo, refPath, commitMessage, files)
	if err != nil {
		return fmt.Errorf("push attachments: %w", err)
	}

	// Render the markdown once. It's used either directly as stdout
	// output (non-JSON mode) or embedded as a field in the JSON
	// result (--json mode).
	markdown := strings.TrimSpace(gh.FormatSection(repo, paths, commitSHA, opts.title))

	// Opt-in side-effect: also post/upsert the markdown as a PR/issue
	// comment. Runs before the final stdout emit so the comment URL
	// can be included in the JSON result when both flags are set.
	var commentURL string
	if opts.postComment {
		cc, err := deps.newCmtClient()
		if err != nil {
			return fmt.Errorf("create comment client: %w", err)
		}
		commentURL, err = cc.UpsertComment(repo, number, paths, commitSHA, opts.title)
		if err != nil {
			return fmt.Errorf("upsert comment: %w", err)
		}
	}

	if opts.asJSON {
		result := uploadResult{
			Repo:       repo.String(),
			Target:     target,
			Ref:        "refs/" + refPath,
			CommitSHA:  commitSHA,
			Markdown:   markdown,
			CommentURL: commentURL, // omitempty hides it when unset
			Files:      make([]uploadFile, 0, len(paths)),
		}
		if opts.key != "" {
			result.Namespace = "misc"
			result.Key = opts.key
		} else {
			result.Namespace = "issue"
			result.Number = number
		}
		for _, p := range paths {
			result.Files = append(result.Files, uploadFile{
				Name: p.Path,
				URL:  gh.EmbedURL(repo, commitSHA, p.Path),
			})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		return nil
	}

	// Non-JSON mode: always emit the rendered markdown to stdout so
	// the caller can embed it anywhere — PR body, Slack, issue
	// template, pbcopy, etc.
	_, _ = fmt.Fprintln(stdout, markdown)

	// Always emit the raw, directly-embeddable URLs to stderr so the user
	// sees actionable references in their terminal even when stdout is piped.
	// Filename is URL-encoded inside gh.EmbedURL so files containing
	// spaces, `#`, `?`, or non-ASCII characters produce valid, clickable
	// URLs.
	_, _ = fmt.Fprintln(stderr, "Uploaded:")
	for _, p := range paths {
		_, _ = fmt.Fprintf(stderr, "  %s\n", gh.EmbedURL(repo, commitSHA, p.Path))
	}

	if opts.postComment {
		_, _ = fmt.Fprintf(stderr, "Commented: %s\n", commentURL)
	}

	return nil
}
