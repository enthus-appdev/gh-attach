# gh-pr-screenshot

> **Deprecated — not maintained.** Use [drogers0/gh-image](https://github.com/drogers0/gh-image) instead, as recommended in [cli/cli#1895 (comment)](https://github.com/cli/cli/issues/1895#issuecomment-4140089593).

A [gh](https://cli.github.com/) extension for uploading screenshots to GitHub PRs.

Images are pushed to an auth-protected `claude/_screenshots` branch and linked inline in a PR comment. No public URLs, no gists — image access is gated by repo visibility.

## Install

```bash
gh extension install enthus-appdev/gh-pr-screenshot
```

## Usage

```bash
# Upload to a specific PR
gh pr-screenshot 123 screenshot.png

# Multiple files
gh pr-screenshot 123 before.png after.png

# Glob pattern
gh pr-screenshot 123 ./screenshots/*.png

# Auto-detect PR from current branch
gh pr-screenshot screenshot.png

# Add a label (flags must come before PR number)
gh pr-screenshot --title "After fix" 123 screenshot.png
```

## How it works

1. Reads image files from disk
2. Pushes them to an orphan `claude/_screenshots` branch via the GitHub Git Data API (no local checkout needed)
3. Posts or updates a PR comment with inline images using `blob?raw=true` URLs
4. Images are accessible only to users who can access the repo

## Cleanup

Add a cleanup workflow to your repo to automatically remove screenshots when PRs are closed. See the [NX-15295 spec](https://enthus.atlassian.net/browse/NX-15295) for the workflow YAML.
