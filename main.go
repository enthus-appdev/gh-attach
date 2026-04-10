package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	title := flag.String("title", "", "Label for the upload group")
	postComment := flag.Bool("comment", false, "Also post (or upsert) the markdown as a PR/issue comment")
	repoOverride := flag.String("repo", "", "Target repo as OWNER/NAME or a GitHub URL (default: origin of the current clone)")
	key := flag.String("key", "", "Upload to an ad-hoc key under refs/uploads/misc/KEY instead of a PR/issue (mutually exclusive with NUMBER and --comment)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Upload images to a GitHub PR or issue and print embeddable markdown to stdout.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  gh attach [flags] [NUMBER] FILE...\n")
		fmt.Fprintf(os.Stderr, "  gh attach [flags] --key KEY FILE...\n\n")
		fmt.Fprintf(os.Stderr, "If NUMBER is omitted, it is auto-detected as a PR from the current branch.\n")
		fmt.Fprintf(os.Stderr, "NUMBER or --key must be passed explicitly whenever --repo is used.\n\n")
		fmt.Fprintf(os.Stderr, "Use --key to upload without a PR/issue — e.g. for a README image or a\n")
		fmt.Fprintf(os.Stderr, "not-yet-created issue. Ad-hoc uploads are stored under refs/uploads/misc/KEY\n")
		fmt.Fprintf(os.Stderr, "and are NOT auto-cleaned by the cleanup workflow — see README for manual removal.\n\n")
		fmt.Fprintf(os.Stderr, "By default, the rendered markdown is written to stdout and no comment is\n")
		fmt.Fprintf(os.Stderr, "posted. Pass --comment to also upsert the markdown as a PR/issue comment.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	number, files := parseArgs(args)

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "error: no image files specified")
		os.Exit(1)
	}

	if err := run(number, files, *title, *postComment, *repoOverride, *key); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// parseArgs separates an optional leading PR or issue number from the file list.
func parseArgs(args []string) (int, []string) {
	if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
		return n, args[1:]
	}
	return 0, args
}

func run(number int, filePaths []string, title string, postComment bool, repoOverride, key string) error {
	// Argument conflicts — fail fast before any network work.
	if number != 0 && key != "" {
		return fmt.Errorf("cannot combine NUMBER with --key — they target different ref namespaces")
	}
	if key != "" && postComment {
		return fmt.Errorf("--comment requires a PR/issue number and is incompatible with --key")
	}
	if key != "" {
		if err := validateKey(key); err != nil {
			return err
		}
	}

	// Resolve repo context
	repo, err := resolveRepo(repoOverride)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	// PR auto-detection uses `gh pr view` on the current branch, which
	// only makes sense inside a clone of the target repo. Any use of
	// --repo means the caller is being explicit about the target, so
	// require NUMBER (or --key) explicitly too — we don't try to detect
	// whether the override happens to match the current clone.
	if number == 0 && key == "" && repoOverride != "" {
		return fmt.Errorf("--repo requires an explicit NUMBER or --key (PR auto-detection only works inside a clone of the target repo)")
	}

	// Resolve number from the current branch's PR if not provided.
	// Auto-detection only covers PRs (via `gh pr view`); to target an
	// issue, pass its number explicitly. Skipped entirely in key mode.
	if number == 0 && key == "" {
		number, err = resolvePR(repo)
		if err != nil {
			return fmt.Errorf("resolve PR: %w", err)
		}
	}

	// Expand globs and validate files exist
	files, err := expandFiles(filePaths)
	if err != nil {
		return err
	}

	// Build the ref path, commit message, and user-facing target descriptor
	// from the mode. gitdata.go's PushAttachments is namespace-agnostic —
	// it takes whatever refPath it's given.
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

	fmt.Fprintf(os.Stderr, "Uploading %d file(s) to %s in %s/%s...\n", len(files), target, repo.Owner, repo.Name)

	// Push images to refs/<refPath> via Git Data API
	client, err := NewGitDataClient()
	if err != nil {
		return fmt.Errorf("create git client: %w", err)
	}
	paths, commitSHA, err := client.PushAttachments(repo, refPath, commitMessage, files)
	if err != nil {
		return fmt.Errorf("push attachments: %w", err)
	}

	// Always emit the rendered markdown to stdout so the caller can embed
	// it anywhere — PR body, Slack, issue template, pbcopy, etc.
	markdown := strings.TrimSpace(formatSection(repo, paths, commitSHA, title))
	_, _ = fmt.Fprintln(os.Stdout, markdown)

	// Always emit the raw, directly-embeddable URLs to stderr so the user
	// sees actionable references in their terminal even when stdout is piped.
	fmt.Fprintln(os.Stderr, "Uploaded:")
	for _, p := range paths {
		fmt.Fprintf(os.Stderr, "  https://github.com/%s/%s/blob/%s/%s?raw=true\n", repo.Owner, repo.Name, commitSHA, p.Path)
	}

	// Opt-in side-effect: also post/upsert the markdown as a PR/issue comment.
	if postComment {
		commentClient, err := NewCommentClient()
		if err != nil {
			return fmt.Errorf("create comment client: %w", err)
		}
		commentURL, err := commentClient.UpsertComment(repo, number, paths, commitSHA, title)
		if err != nil {
			return fmt.Errorf("upsert comment: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Commented: %s\n", commentURL)
	}

	return nil
}

// expandFiles resolves globs and verifies each file exists and is a regular file.
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
