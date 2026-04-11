# Security Policy

`gh-attach` reads the signed-in user's OAuth token from the [`gh` CLI](https://cli.github.com/)
(via `gh auth token`) and uses it to push file contents under that user's
identity via GitHub's Git Data API. A security bug in this tool could leak
the token, let an attacker write content under someone else's identity, or
escape out of the intended filesystem boundaries on upload or download, so
please report issues privately so they can be fixed before public disclosure.

## Supported versions

Only the **latest tagged release** receives security fixes. The project is
pre-1.0 and releases often — please upgrade before reporting:

```bash
gh extension upgrade gh-attach
```

If you can only reproduce the issue on an older version, mention that in the
report, but the fix will ship on the latest release line.

## Reporting a vulnerability

**Do not open a public issue for security problems.** Public issues are
indexable and broadcast the vulnerability before a fix is available.

Use GitHub's Private Vulnerability Reporting:

👉 **[Report a vulnerability](https://github.com/enthus-appdev/gh-attach/security/advisories/new)**

Private Vulnerability Reporting is enabled on this repository. The report is
visible only to you and the maintainers; GitHub handles the advisory workflow
and credit tracking. Please include as much of the following as you can:

- A description of the issue and its impact
- Reproduction steps, including the installed version (`gh extension list`
  shows the gh-attach version) and your platform
- Any proof-of-concept code, scripts, or sample output
- Affected code paths or commits, if you've found them
- A proposed fix if you have one
- Your GitHub handle if you'd like public credit after the fix ships

## Response expectations

This is a small-team open-source project. Realistic targets:

| Event | Timeline |
|---|---|
| Initial acknowledgment | within **5 business days** of your report |
| Triage and severity assessment | within **10 business days** |
| Fix development | depends on severity and complexity |
| Coordinated disclosure | typically **30–90 days** after a fix ships, sooner if actively exploited |

If you have not heard back within the acknowledgment window, please nudge via
the security advisory thread.

## Scope

### In scope

Anything in the `gh-attach` binary or its upload/download flow. High-value
areas to probe:

- **Authentication handling** — the `gh auth token` exec path in
  `internal/gh/resolver.go` that resolves the user's OAuth token, how that
  token is passed to the HTTP client (`Authorization: token ...` on every
  request in `internal/gh/gitdata.go` and `internal/gh/comment.go`), and
  whether the token can leak into error messages, log output, or the
  `--json` result on stderr/stdout.
- **`gh` CLI delegation** — the three subprocess call sites that
  `gh-attach` goes through (all in `internal/gh/resolver.go`):
  `git remote get-url origin` (static args), `gh auth token` (static
  args), and `gh pr view --json number --repo <repo>` where `<repo>`
  is the string passed to `--repo` or parsed from the git remote. The
  third one is the only path where user-influenced input reaches argv,
  so it's the candidate worth probing for argument-injection edge
  cases.
- **Git Data API flow** — the 4-step upload sequence (blob → tree →
  commit → ref create or fast-forward) in `internal/gh/gitdata.go`, the
  matching 4-step download sequence (ref → commit → tree → blob) in
  `GetAttachments`, and the ref create/delete endpoints in
  `DeleteRef`/`ListRefs`.
- **File handling** — `--name` basename validation, stdin materialization
  to temp files, path traversal in both the upload and `get` paths,
  `filepath.Base` assumptions on arbitrary input, symlink handling in
  `expandFiles`, and the pre-flight conflict check + force-overwrite
  behavior of `gh attach get`.
- **Ref handling** — `refs/uploads/*` namespace construction, ref name
  validation (`gh.ValidateKey`), and the commit/tree walking performed by
  `gh attach get`.
- **Git remote parsing** — the SSH and HTTPS URL parsers in
  `internal/gh/repo.go`, including any input that could lead to command
  or argument injection in the `gh` CLI calls that follow, or to
  targeting the wrong repository.
- **Output rendering** — markdown injection via filename fields in
  `FormatSection`, URL encoding in embed URLs (`EmbedURL`), and the
  `--json` output contract for `upload`/`list`/`get` results.

### Out of scope

Please report the following upstream rather than here — they are not
`gh-attach` vulnerabilities:

- Issues in the [`gh` CLI](https://github.com/cli/cli) itself, including
  how it stores and retrieves OAuth tokens on the local machine
- Issues in the Go standard library, toolchain, or `net/http` defaults
- Issues in GitHub's own API, storage infrastructure, or rate limits
- An authenticated user uploading content to their own repositories —
  that is the intended behavior of the tool

## Disclosure policy

We practice coordinated disclosure:

1. You report the vulnerability privately via the link above.
2. We acknowledge, triage, and develop a fix.
3. We release a patched version on a new tag.
4. After a mutually agreed delay, we publish the security advisory, credit
   the reporter (unless anonymity is requested), and link to the fix commit
   and release.

Reporters are credited in the advisory and in the release notes for the fix
unless they prefer to remain anonymous.
