<!--
Thanks for contributing to gh-attach!

The template below is a minimum — the sections contributors actually
fill out on merged PRs. Larger changes often add "## How it works"
(architecture / implementation notes) and "## Migration from vX.Y"
(when existing behavior changes). See any recent merged PR for the
pattern, or read CONTRIBUTING.md for the full rundown.

Linked issues: if this PR closes an existing issue, put `Closes #NN`
somewhere in the Summary so the issue is auto-closed on merge.
-->

## Summary

<!--
One or two paragraphs on what changed and why. Lead with the *why*
and what's different for users; skip narrating the diff line-by-line.
-->

## Test plan

<!--
Check the boxes for the verifications you actually completed. Leave
ones that don't apply unchecked, or delete them. Add items as
appropriate for your change.
-->

- [ ] New or updated unit tests for the changed behavior
- [ ] `go build ./... && go test ./... && golangci-lint run ./...` clean locally
- [ ] End-to-end smoke test against a throwaway issue on a real repo
      (required if you touched the upload, download, or comment flow —
      fakes don't model real GitHub API behavior)
