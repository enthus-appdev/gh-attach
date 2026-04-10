# gh-pr-screenshot

> **Deprecated — not maintained.** Use [drogers0/gh-image](https://github.com/drogers0/gh-image) instead, as recommended in [cli/cli#1895 (comment)](https://github.com/cli/cli/issues/1895#issuecomment-4140089593).

A [gh](https://cli.github.com/) extension for uploading screenshots to GitHub PRs.

Images are pushed to an auth-protected ref under `refs/uploads/issues/<N>` (one per PR/issue, invisible in the Branches UI) and linked inline in a PR comment via `blob/<commit-sha>/<file>?raw=true` URLs. No public URLs, no gists — image access is gated by repo visibility (private repos require an authenticated browser session to view).

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
2. Pushes them as a single fast-forwarding commit to `refs/uploads/issues/<N>` via the GitHub Git Data API (no local checkout needed). The ref lives outside `refs/heads/*` and `refs/tags/*`, so it does not appear in the Branches UI, is not subject to branch protection / rulesets, and does not trigger `push` workflows.
3. Posts or updates a PR comment with inline images. The embedded URL uses the *commit SHA* directly (`blob/<sha>/<file>?raw=true`), so URLs from previous uploads remain valid as long as the ref is alive — fast-forwarding adds new commits without invalidating prior ones.
4. Images are accessible only to users who can access the repo. On private repos, the embed URL requires a browser session cookie (not even API PATs work against the embed URL — only the parallel `api.github.com/.../contents/{path}?ref={sha}` endpoint accepts tokens).

## Cleanup

Add a cleanup workflow to your repo to automatically remove screenshots when PRs are closed:

```yaml
# .github/workflows/cleanup-pr-screenshots.yml
name: Cleanup PR screenshots
on:
  pull_request:
    types: [closed]
permissions:
  contents: write
jobs:
  cleanup:
    runs-on: ubuntu-latest
    steps:
      - env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          gh api -X DELETE \
            "repos/${{ github.repository }}/git/refs/uploads/issues/${{ github.event.number }}" \
            || echo "no upload ref"
```

Deleting the ref makes the orphan commits unreachable, and GitHub eventually GCs the blob storage.
