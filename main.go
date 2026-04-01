package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	title := flag.String("title", "", "Label for the screenshot group")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Upload screenshots to a GitHub PR.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  gh pr-screenshot [PR_NUMBER] [flags] FILE...\n\n")
		fmt.Fprintf(os.Stderr, "If PR_NUMBER is omitted, it is auto-detected from the current branch.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	prNumber, files := parseArgs(args)

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "error: no image files specified")
		os.Exit(1)
	}

	if err := run(prNumber, files, *title); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// parseArgs separates an optional leading PR number from the file list.
func parseArgs(args []string) (int, []string) {
	if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
		return n, args[1:]
	}
	return 0, args
}

func run(prNumber int, filePaths []string, title string) error {
	// Resolve repo context
	repo, err := resolveRepo()
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	// Resolve PR number if not provided
	if prNumber == 0 {
		prNumber, err = resolvePR(repo)
		if err != nil {
			return fmt.Errorf("resolve PR: %w", err)
		}
	}

	// Expand globs and validate files exist
	files, err := expandFiles(filePaths)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Uploading %d file(s) to PR #%d in %s/%s...\n", len(files), prNumber, repo.Owner, repo.Name)

	// Push images to _screenshots branch via Git Data API
	client, err := NewGitDataClient()
	if err != nil {
		return fmt.Errorf("create git client: %w", err)
	}
	paths, err := client.PushScreenshots(repo, prNumber, files)
	if err != nil {
		return fmt.Errorf("push screenshots: %w", err)
	}

	// Post/update PR comment
	commentClient, err := NewCommentClient()
	if err != nil {
		return fmt.Errorf("create comment client: %w", err)
	}
	commentURL, err := commentClient.UpsertComment(repo, prNumber, paths, title)
	if err != nil {
		return fmt.Errorf("upsert comment: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Done: %s\n", commentURL)
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

// --- Stubs: replaced by later tasks ---

// ScreenshotPath holds the branch-relative path and display name for an uploaded screenshot.
type ScreenshotPath struct {
	BranchPath string // e.g. "pr-123/20260401-120000-screenshot.png"
	FileName   string // e.g. "screenshot.png"
}

// GitDataClient interacts with the GitHub Git Data API.
type GitDataClient struct {
	BaseURL string
	Token   string
}

func NewGitDataClient() (*GitDataClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *GitDataClient) PushScreenshots(repo *Repo, prNumber int, files []string) ([]ScreenshotPath, error) {
	return nil, fmt.Errorf("not implemented")
}

// CommentClient interacts with the GitHub Issues API for PR comments.
type CommentClient struct {
	BaseURL string
	Token   string
}

func NewCommentClient() (*CommentClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (c *CommentClient) UpsertComment(repo *Repo, prNumber int, paths []ScreenshotPath, title string) (string, error) {
	return "", fmt.Errorf("not implemented")
}
