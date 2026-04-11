# Contributing to gh-attach

Thanks for taking the time to contribute! `gh-attach` is a small, focused
`gh` CLI extension, and the contributing workflow is intentionally kept
proportional to that scope — expect a friendly, fast review, but also
expect us to keep the tool small and the diffs narrow.

## Before you start

- **Questions and ideas** → open an [issue](https://github.com/enthus-appdev/gh-attach/issues)
  so we can discuss scope before you write code. For anything non-trivial,
  it saves everyone time to align on the shape of a change first.
- **Bug reports** → also go in the issue tracker; please include your
  `gh-attach` version (`gh extension list | grep gh-attach`) and your OS.
- **Security issues** → do **not** open a public issue. See
  [SECURITY.md](./SECURITY.md) for the private reporting flow.
- **Be respectful.** This is a public, open-source project — treat other
  contributors the way you'd want to be treated yourself.

## Prerequisites

- **Go** — see `go.mod` for the required version (`go 1.26` at the time
  of writing; CI uses `go-version-file: ./go.mod`, so stay in sync with
  that).
- **`gh` CLI** — `gh-attach` is a `gh` extension, so you need a working
  `gh` installation to test it. Authenticate with `gh auth login` before
  running against a real repo.
- **`golangci-lint`** — install from
  [golangci-lint.run](https://golangci-lint.run/welcome/install/); CI runs
  the default linter set with no `.golangci.yml` overrides, so what passes
  locally will pass in CI.

`gh-attach` has **zero external dependencies** — `go.mod` declares only
the module itself. That keeps build, test, and lint all fast on a cold
clone.

## Clone and build

```bash
git clone git@github.com:enthus-appdev/gh-attach.git
cd gh-attach
go build ./...
```

To install your local working copy as a `gh` extension for end-to-end
testing against a real repo:

```bash
gh extension install .
gh attach --help   # confirm it's wired up
```

To switch back to the released version later:

```bash
gh extension remove gh-attach
gh extension install enthus-appdev/gh-attach
```

## Common commands

```bash
# Build
go build ./...

# Full test suite
go test ./...

# Tests with the race detector (run before submitting anything that
# touches goroutines or shared state)
go test -race ./...

# Test a single package with verbose output
go test -v ./internal/cli/

# Coverage report — same invocation CI uses
go test -coverpkg=./internal/... -coverprofile=cover.out ./...
go tool cover -func=cover.out     # per-function totals
go tool cover -html=cover.out     # browser view of uncovered lines

# Lint — same rules CI uses
golangci-lint run ./...

# Static analysis
go vet ./...
```

CI runs `go build`, `go test` (with coverage measurement on
`./internal/...`), and `golangci-lint` on every push and PR. Draft PRs are
skipped to save CI minutes — mark a PR as ready-for-review when you want
the checks to run.

## Project layout

```
cmd/gh-attach/               # Thin entrypoint wrapper (os.Exit(cli.Run(...)))
internal/cli/                # Subcommand runners, flag parsing, orchestration
  run.go / run_test.go       # Upload flow + subcommand router
  list.go / list_test.go     # gh attach list
  delete.go / delete_test.go # gh attach delete
  get.go / get_test.go       # gh attach get
  files.go / files_test.go   # expandFiles, validateName, materializeStdin
  args.go                    # parseArgs
internal/gh/                 # GitHub API clients
  gitdata.go                 # GitDataClient: PushAttachments, ListRefs,
                             #                DeleteRef, GetAttachments
  comment.go                 # CommentClient: UpsertComment + FormatSection
  repo.go                    # Repo struct, ValidateKey, remote-URL parsing
  resolver.go                # ResolveRepo, ResolvePR, ghAuthToken
.github/workflows/           # CI (Go test + coverage, lint, release,
                             # cleanup workflow)
```

Architecture details — the 4-step Git Data API upload flow, the dual-auth
model, the ad-hoc key namespace — live in the [README](./README.md). Read
that first if you're changing how uploads or downloads work.

The CLI layer uses dependency injection via a `runDeps` struct so tests
can swap in fakes instead of touching the network. See
`internal/cli/run.go` for the pattern and `internal/cli/run_test.go` for
how the `fakeGitClient` and `fakeCmtClient` are wired up. New subcommands
should follow the same pattern.

## Making a change

1. **Branch off `main`.** Keep branches short-lived; avoid long-running
   forks. Any name works — we squash-merge on the way in, so the branch
   name isn't preserved.
2. **Write tests.** Every behavior change should come with a test.
   - `internal/cli/*_test.go` uses in-process fakes via `runDeps`.
   - `internal/gh/*_test.go` uses `net/http/httptest` to mock the GitHub
     API surface.
   - Table-driven tests are the norm for validation and arg-parsing code.
3. **Keep coverage high.** CI posts a per-PR coverage diff comment. There
   is no hard threshold, but significant drops will get flagged in review.
4. **Run the full suite locally before pushing:**
   ```bash
   go build ./... && go vet ./... && go test ./... && golangci-lint run ./...
   ```
5. **Smoke-test against a real repo** if your change touches the upload,
   download, or comment flow. Create a throwaway issue on a repo you own,
   run your modified binary against it end-to-end, and clean up (`gh
   attach delete` + close the issue) when you're done. This catches real
   API behavior the fakes can't model — it's caught real bugs in this
   repo's history already.

## Commit messages

We follow [Conventional Commits](https://www.conventionalcommits.org/).
Merged commits look like:

```
feat: add --name flag for reading file bytes from stdin (#20)
fix: address PR review — humanizeBytes out-of-bounds on large sizes
docs: add SECURITY.md (#26)
chore: use demo-repo as neutral test fixture name (#23)
refactor: cmd/ + internal/ layout, test injection, 56% → 97% coverage (#15)
```

The common types are `feat`, `fix`, `refactor`, `docs`, `chore`, `test`,
and `ci`. Breaking changes use `feat!:` or `chore!:` (both appear in the
history). Short subject line, then an empty line, then a body explaining
the *why* — not just the *what*.

PRs are squash-merged, so the PR title becomes the merge commit message.
Pick a good title up front.

## Submitting a pull request

PR descriptions in this repo follow a loose template — look at any recent
merged PR for the pattern. The minimum useful shape:

```markdown
## Summary
One or two paragraphs on what changed and why.

## Test plan
- [x] New unit tests for X
- [x] `go test ./...` + `golangci-lint run ./...` clean
- [x] End-to-end smoke test against a throwaway issue (if upload/download
      path was touched)
```

Larger changes often add a "How it works" section and a "Migration from"
section when behavior changes for existing users. Use whatever structure
helps the reviewer.

CI runs on every push to your PR branch — wait for it to go green before
asking for review. Draft PRs skip CI, so switch to ready-for-review when
you're ready.

## Review, follow-ups, and merge

- **Reviews** are handled in GitHub. Address review comments in new
  commits on the same branch rather than force-pushing — it makes the
  review conversation easier to follow. Everything gets squashed on merge
  anyway.
- **Merge** is squash-merge, with the branch auto-deleted afterward.
- **Follow-ups** to a merged PR (e.g. fixing a review comment that came
  in late, or addressing CI feedback) go on a fresh branch and open a new
  PR — keep the history linear and each PR focused on a single concern.

## License

`gh-attach` is released under the [MIT License](./LICENSE). By submitting
a pull request, you agree that your contribution will be licensed under
the same terms. There is no separate CLA or DCO process.
