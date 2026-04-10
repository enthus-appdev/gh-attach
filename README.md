# gh-attach

[![Go](https://github.com/enthus-appdev/gh-attach/actions/workflows/go.yml/badge.svg)](https://github.com/enthus-appdev/gh-attach/actions/workflows/go.yml)
[![Coverage](https://github.com/enthus-appdev/gh-attach/raw/badges/.badges/main/coverage.svg)](https://github.com/enthus-appdev/gh-attach/actions/workflows/go.yml)

A [gh](https://cli.github.com/) extension for uploading images to GitHub PRs and issues, privately scoped to repo visibility.

Images are pushed to an auth-protected ref under `refs/uploads/issues/<N>` (one per PR/issue, invisible in the Branches UI) and rendered as inline markdown via `blob/<commit-sha>/<file>?raw=true` URLs — written to stdout by default, optionally upserted as a PR/issue comment with `--comment`. No public URLs, no gists — image access is gated by repo visibility (private repos require an authenticated browser session to view).

## Install

```bash
gh extension install enthus-appdev/gh-attach
```

## Usage

By default, `gh attach` uploads the files and prints the rendered markdown to **stdout** — it does *not* post a comment unless you ask. The caller decides what to do with the markdown: embed it in a PR body, pipe it to `gh pr comment`, paste it into Slack, or tee it to a file.

```bash
# Upload and print embeddable markdown to stdout
gh attach 123 mockup.png

# Multiple files in one upload group
gh attach 123 before.png after.png

# Glob patterns
gh attach 123 ./images/*.png

# Auto-detect PR from current branch
gh attach screenshot.png

# Label the group with --title (flags must come before the number)
gh attach --title "After fix" 123 diagram.png

# Also post the markdown as an upserted PR/issue comment (pre-v0.3 behavior)
gh attach --comment 123 screenshot.png

# Target a different repo (or run from outside any git clone)
gh attach --repo enthus-appdev/gh-attach 123 screenshot.png
gh attach --repo https://github.com/enthus-appdev/gh-attach 123 screenshot.png

# Ad-hoc upload with no PR or issue (see "Ad-hoc uploads" below)
gh attach --key design-v2 mockup.png
gh attach --key docs/arch-diagram diagram.png
```

By default `gh attach` reads the target repo from the current clone's `origin` remote. Pass `--repo OWNER/NAME` (or a full GitHub URL) to target a different repo or to run from outside any git clone. Whenever `--repo` is used, `NUMBER` or `--key` must be passed explicitly — PR auto-detection only works inside a clone of the target repo.

### Ad-hoc uploads (no PR or issue)

Not every upload belongs to a PR or issue. Screenshots for a README, diagrams for a docs site, images for a not-yet-created issue, photos for release notes — all of these want a stable repo-scoped URL without the tracking overhead of a placeholder issue.

Pass `--key KEY` to upload to `refs/uploads/misc/KEY` instead of `refs/uploads/issues/<N>`:

```bash
# Upload a README banner
gh attach --key readme-banner banner.png

# Prepare an image for an issue you haven't created yet, then use the markdown in the issue body
MARKDOWN=$(gh attach --key feature-mockup screenshot.png)
gh issue create --title "New feature" --body "## Design

$MARKDOWN"

# Hierarchical keys are allowed — useful for organization
gh attach --key docs/arch-diagram diagram.png
gh attach --key releases/v1.0/hero hero.png
```

**Key rules**: 1–100 characters, letters/digits/`._-` plus `/` for subpaths, must start with a letter/digit/underscore, and cannot be purely numeric (that would collide visually with PR/issue numbers). Leading `.`, `..`, `//`, trailing `/`, and `.lock` suffix are rejected per git's ref name rules.

**What's different from the PR/issue mode**:
- No PR auto-detection — `--key` always targets the key you supply.
- `--comment` is not allowed (there's no PR/issue to comment on).
- The cleanup workflow (see below) does **not** touch ad-hoc refs — they're user-managed.

**Manual cleanup** when you're done with an ad-hoc upload:

```bash
gh api -X DELETE repos/OWNER/NAME/git/refs/uploads/misc/KEY
```

Deleting the ref orphans the blob storage and GitHub eventually GCs it.

### Composing with other tools

Because the markdown goes to stdout, `gh attach` plays well with shell pipelines:

```bash
# Embed uploads directly in a PR body
MARKDOWN=$(gh attach 123 dist/report.png)
gh pr edit 123 --body "Build passed.

$MARKDOWN"

# Copy to clipboard for manual pasting (Wayland / X11 / macOS)
gh attach 123 screenshot.png | wl-copy
gh attach 123 screenshot.png | xclip -selection clipboard
gh attach 123 screenshot.png | pbcopy

# Pipe into gh-cli's own commenter instead of gh-attach's upsert
gh attach 123 file.png | gh pr comment 123 --body-file -

# Save for later
gh attach 123 file.png > upload.md
```

On stderr you get the progress line plus one directly-embeddable URL per file, so interactive users see copy-pasteable links in their terminal even when stdout is piped:

```
Uploading 1 file(s) to #123 in owner/repo...
Uploaded:
  https://github.com/owner/repo/blob/abc1234/screenshot.png?raw=true
```

## How it works

1. Reads image files from disk.
2. Pushes them as a single fast-forwarding commit to `refs/uploads/issues/<N>` (default) or `refs/uploads/misc/<key>` (when `--key` is used) via the GitHub Git Data API. No local checkout needed. The ref lives outside `refs/heads/*` and `refs/tags/*`, so it does not appear in the Branches UI, is not subject to branch protection / rulesets, and does not trigger `push` workflows.
3. Renders a markdown table of inline images and writes it to stdout. Each embed URL uses the *commit SHA* directly (`blob/<sha>/<file>?raw=true`), so URLs from previous uploads remain valid as long as the ref is alive — fast-forwarding adds new commits without invalidating prior ones.
4. With `--comment`, also posts or updates a PR/issue comment carrying the same markdown, tracked via an HTML-comment marker so repeated calls upsert into a single comment instead of piling up. Not available in ad-hoc (`--key`) mode.
5. Images are accessible only to users who can access the repo. On private repos, the embed URL requires a browser session cookie (not even API PATs work against the embed URL — only the parallel `api.github.com/.../contents/{path}?ref={sha}` endpoint accepts tokens).

## Cleanup

### PR/issue uploads — automatic

To automatically remove uploaded images when a PR or issue is closed, copy [`.github/workflows/cleanup-gh-attach.yml`](.github/workflows/cleanup-gh-attach.yml) from this repo into your repo's `.github/workflows/` directory. The same file is installed in this repo as the canonical source — re-sync from `main` whenever you want to pick up improvements (the copy is a snapshot, not a live link).

No customization required — the workflow uses `github.repository` and the closed event's number to find and delete the upload ref. It listens to both `pull_request: closed` and `issues: closed`, so it covers issue uploads as well as PR uploads. The "no upload ref for this PR" case is handled silently (no error if you didn't post any images on that PR).

The workflow only touches `refs/uploads/issues/<N>` — ad-hoc (`refs/uploads/misc/<key>`) refs are **not** affected.

### Ad-hoc (`--key`) uploads — manual

Because ad-hoc uploads have no close event to hook, you manage their lifetime yourself. One `gh api` call per ref:

```bash
gh api -X DELETE repos/OWNER/NAME/git/refs/uploads/misc/KEY
```

Deleting the ref makes the orphan commits unreachable, and GitHub eventually GCs the blob storage.
