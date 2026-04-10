# gh-attach

A [gh](https://cli.github.com/) extension for uploading images to GitHub PRs and issues, privately scoped to repo visibility.

Images are pushed to an auth-protected ref under `refs/uploads/issues/<N>` (one per PR/issue, invisible in the Branches UI) and linked inline in a PR comment via `blob/<commit-sha>/<file>?raw=true` URLs. No public URLs, no gists — image access is gated by repo visibility (private repos require an authenticated browser session to view).

## Install

```bash
gh extension install enthus-appdev/gh-attach
```

## Usage

```bash
# Upload to a specific PR
gh attach 123 screenshot.png

# Multiple files
gh attach 123 before.png after.png

# Glob pattern
gh attach 123 ./screenshots/*.png

# Auto-detect PR from current branch
gh attach screenshot.png

# Add a label (flags must come before PR number)
gh attach --title "After fix" 123 screenshot.png
```

## How it works

1. Reads image files from disk
2. Pushes them as a single fast-forwarding commit to `refs/uploads/issues/<N>` via the GitHub Git Data API (no local checkout needed). The ref lives outside `refs/heads/*` and `refs/tags/*`, so it does not appear in the Branches UI, is not subject to branch protection / rulesets, and does not trigger `push` workflows.
3. Posts or updates a PR comment with inline images. The embedded URL uses the *commit SHA* directly (`blob/<sha>/<file>?raw=true`), so URLs from previous uploads remain valid as long as the ref is alive — fast-forwarding adds new commits without invalidating prior ones.
4. Images are accessible only to users who can access the repo. On private repos, the embed URL requires a browser session cookie (not even API PATs work against the embed URL — only the parallel `api.github.com/.../contents/{path}?ref={sha}` endpoint accepts tokens).

## Cleanup

To automatically remove uploaded images when a PR or issue is closed, copy [`.github/workflows/cleanup-gh-attach.yml`](.github/workflows/cleanup-gh-attach.yml) from this repo into your repo's `.github/workflows/` directory. The same file is installed in this repo as the canonical source — re-sync from `main` whenever you want to pick up improvements (the copy is a snapshot, not a live link).

No customization required — the workflow uses `github.repository` and the closed event's number to find and delete the upload ref. It listens to both `pull_request: closed` and `issues: closed`, so it covers future issue-comment uploads as well as the current PR-comment use case. The "no upload ref for this PR" case is handled silently (no error if you didn't post any images on that PR).

Deleting the ref makes the orphan commits unreachable, and GitHub eventually GCs the blob storage.
