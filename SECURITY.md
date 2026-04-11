# Security Policy

`gh-attach` handles GitHub authentication cookies on the local machine and
uploads file contents on behalf of the signed-in user via GitHub's Git Data
API. A security bug in this tool could expose credentials or let an attacker
write content under someone else's identity, so please report issues privately
so they can be fixed before public disclosure.

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

- **Authentication handling** — `user_session` cookie extraction via `kooky`,
  cookie jar construction, the 3-step upload token chain (`uploadToken` →
  `asset_upload_authenticity_token` → S3 presigned POST), and `gh` CLI
  delegation for repo ID resolution.
- **File handling** — `--name` basename validation, stdin materialization to
  temp files, path traversal in both the upload and `get` paths, `filepath.Base`
  assumptions, and symlink handling.
- **Ref handling** — `refs/uploads/*` namespace construction, ref name
  validation (`gh.ValidateKey`), and commit/tree walking in `get`.
- **Git remote parsing** — the SSH and HTTPS URL parsers in
  `internal/gh/repo.go`, including any input that could lead to command or
  argument injection in the `gh` CLI calls that follow.
- **Output rendering** — markdown injection via filename fields, URL encoding
  in embed URLs, and the `--json` output contract.

### Out of scope

Please report the following upstream rather than here — they are not
`gh-attach` vulnerabilities:

- Issues in the [`gh` CLI](https://github.com/cli/cli) itself
- Issues in the [`kooky`](https://github.com/browserutils/kooky) browser
  cookie library
- Issues in the Go standard library or toolchain
- Issues in GitHub's own API or storage infrastructure
- An authenticated user uploading content to their own repositories — that
  is the intended behavior of the tool

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
