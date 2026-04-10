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
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Upload images to a GitHub PR or issue and print embeddable markdown to stdout.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  gh attach [flags] [NUMBER] FILE...\n\n")
		fmt.Fprintf(os.Stderr, "If NUMBER is omitted, it is auto-detected as a PR from the current branch.\n\n")
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

	if err := run(number, files, *title, *postComment); err != nil {
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

func run(number int, filePaths []string, title string, postComment bool) error {
	// Resolve repo context
	repo, err := resolveRepo()
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	// Resolve number from the current branch's PR if not provided.
	// Auto-detection only covers PRs (via `gh pr view`); to target an
	// issue, pass its number explicitly.
	if number == 0 {
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

	fmt.Fprintf(os.Stderr, "Uploading %d file(s) to #%d in %s/%s...\n", len(files), number, repo.Owner, repo.Name)

	// Push images to refs/uploads/issues/<N> via Git Data API
	client, err := NewGitDataClient()
	if err != nil {
		return fmt.Errorf("create git client: %w", err)
	}
	paths, commitSHA, err := client.PushAttachments(repo, number, files)
	if err != nil {
		return fmt.Errorf("push attachments: %w", err)
	}

	// Always emit the rendered markdown to stdout so the caller can embed
	// it anywhere — PR body, Slack, issue template, pbcopy, etc.
	markdown := strings.TrimSpace(formatSection(repo, paths, commitSHA, title))
	fmt.Fprintln(os.Stdout, markdown)

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
