package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// downloadResult is the shape emitted to stdout when `gh attach get
// --json` is used. It mirrors uploadResult in run.go (repo, target,
// namespace-tagged identity, ref, commit SHA, files) with two
// additions specific to downloading: the resolved OutputDir and a
// per-file Path containing the full written location on disk.
//
// Consumers piping to `jq -r '.files[].path' | xargs ...` get
// immediately-usable file paths; `jq -r '.output_dir'` gives the
// parent for anyone who wants to cd into it.
type downloadResult struct {
	Repo      string         `json:"repo"`
	Target    string         `json:"target"`
	Namespace string         `json:"namespace"`
	Number    int            `json:"number,omitempty"`
	Key       string         `json:"key,omitempty"`
	Ref       string         `json:"ref"`
	CommitSHA string         `json:"sha"`
	OutputDir string         `json:"output_dir"`
	Files     []downloadFile `json:"files"`
}

// downloadFile is one entry in downloadResult.Files. Name is the raw
// basename from the git tree; Path is the full written path on disk
// (the value the user can pass straight to `open`, `xdg-open`, or
// any follow-up command).
type downloadFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	SHA  string `json:"sha"`
}

// runGet implements the `gh attach get` subcommand: resolves a PR/
// issue-scoped or ad-hoc upload ref, fetches every file stored under
// that ref's tip commit tree, and writes them to the local disk.
//
// It's the exact inverse of the upload flow — `gh attach NUMBER file
// && gh attach get NUMBER -o ./restored` round-trips the same bytes
// because gh.GetAttachments walks the same ref → commit → tree →
// blobs chain that PushAttachments builds.
func runGet(args []string, stdout, stderr io.Writer, deps runDeps) int {
	fs := flag.NewFlagSet("gh-attach get", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoOverride := fs.String("repo", "", "Target repo as OWNER/NAME or a GitHub URL (default: origin of the current clone)")
	key := fs.String("key", "", "Ad-hoc source (refs/uploads/misc/KEY). Mutually exclusive with NUMBER.")
	outputDir := fs.String("output", ".", "Directory to write files into (created if missing)")
	force := fs.Bool("force", false, "Overwrite existing files in the output directory")
	asJSON := fs.Bool("json", false, "Emit a JSON result object instead of text output")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Download files from an upload ref (refs/uploads/*) to the local disk.\n\n")
		_, _ = fmt.Fprintf(stderr, "Usage:\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach get [flags] [NUMBER]\n")
		_, _ = fmt.Fprintf(stderr, "  gh attach get [flags] --key KEY\n\n")
		_, _ = fmt.Fprintf(stderr, "If NUMBER is omitted, it is auto-detected as a PR from the current branch.\n")
		_, _ = fmt.Fprintf(stderr, "NUMBER or --key must be passed explicitly whenever --repo is used.\n\n")
		_, _ = fmt.Fprintf(stderr, "Existing files in the output directory are left alone unless --force is set.\n\n")
		_, _ = fmt.Fprintf(stderr, "Flags:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 1
	}

	// Extract optional positional NUMBER. Reject any extras so users
	// see a clear "unexpected extra argument" error instead of having
	// their input silently dropped.
	positional := fs.Args()
	number, remaining := parseArgs(positional)
	if len(remaining) > 0 {
		_, _ = fmt.Fprintf(stderr, "error: unexpected extra argument(s): %s\n", strings.Join(remaining, " "))
		return 1
	}

	if number != 0 && *key != "" {
		_, _ = fmt.Fprintln(stderr, "error: cannot combine NUMBER with --key — they target different ref namespaces")
		return 1
	}
	if *key != "" {
		if err := gh.ValidateKey(*key); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}

	repo, err := deps.resolveRepo(*repoOverride)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: resolve repo: %v\n", err)
		return 1
	}

	// --repo means the caller is being explicit about the target repo,
	// so require NUMBER or --key explicitly too. PR auto-detection only
	// makes sense inside a clone of the target repo.
	if number == 0 && *key == "" && *repoOverride != "" {
		_, _ = fmt.Fprintln(stderr, "error: --repo requires an explicit NUMBER or --key (PR auto-detection only works inside a clone of the target repo)")
		return 1
	}

	// Auto-detect PR from current branch if neither NUMBER nor --key
	// was supplied. Mirrors the upload flow.
	if number == 0 && *key == "" {
		n, err := deps.resolvePR(repo)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: resolve PR: %v\n", err)
			return 1
		}
		number = n
	}

	// Build the ref path + user-facing target descriptor.
	var refPath, target string
	if *key != "" {
		refPath = "uploads/misc/" + *key
		target = "misc/" + *key
	} else {
		refPath = fmt.Sprintf("uploads/issues/%d", number)
		target = fmt.Sprintf("#%d", number)
	}

	client, err := deps.newGitClient()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: create git client: %v\n", err)
		return 1
	}

	// Progress line (suppressed in JSON mode so stderr is quiet).
	if !*asJSON {
		_, _ = fmt.Fprintf(stderr, "Downloading from %s in %s...\n", target, repo)
	}

	attachments, commitSHA, err := client.GetAttachments(repo, refPath)
	if err != nil {
		if errors.Is(err, gh.ErrNotFound) {
			_, _ = fmt.Fprintf(stderr, "error: refs/%s: not found in %s (%s)\n", refPath, repo, target)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "error: get attachments: %v\n", err)
		return 1
	}

	if len(attachments) == 0 {
		// This shouldn't normally happen — a ref that exists was put
		// there by PushAttachments and contains at least one blob —
		// but handle the empty case gracefully rather than silently
		// creating an unused output dir.
		_, _ = fmt.Fprintf(stderr, "No files in refs/%s (ref exists but tree is empty)\n", refPath)
		return 0
	}

	// Ensure the output directory exists. MkdirAll is idempotent and
	// also handles the "missing parent" case for users passing
	// multi-level paths like `--output ./restored/2026-04-11`.
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: create output dir %q: %v\n", *outputDir, err)
		return 1
	}

	// Pre-flight check: if any target path already exists and --force
	// is not set, fail before writing anything so a partial download
	// can't leave the directory in a surprising state.
	if !*force {
		var conflicts []string
		for _, a := range attachments {
			dst := filepath.Join(*outputDir, a.Path)
			if _, statErr := os.Stat(dst); statErr == nil {
				conflicts = append(conflicts, dst)
			}
		}
		if len(conflicts) > 0 {
			_, _ = fmt.Fprintf(stderr, "error: refusing to overwrite existing file(s) (pass --force to replace):\n")
			for _, c := range conflicts {
				_, _ = fmt.Fprintf(stderr, "  %s\n", c)
			}
			return 1
		}
	}

	// Write each file. Failures are reported but don't roll back —
	// the partial download is useful for debugging.
	writtenPaths := make([]string, 0, len(attachments))
	for _, a := range attachments {
		dst := filepath.Join(*outputDir, a.Path)
		if err := os.WriteFile(dst, a.Content, 0644); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: write %s: %v\n", dst, err)
			return 1
		}
		writtenPaths = append(writtenPaths, dst)
	}

	if *asJSON {
		result := downloadResult{
			Repo:      repo.String(),
			Target:    target,
			Ref:       "refs/" + refPath,
			CommitSHA: commitSHA,
			OutputDir: *outputDir,
			Files:     make([]downloadFile, 0, len(attachments)),
		}
		if *key != "" {
			result.Namespace = "misc"
			result.Key = *key
		} else {
			result.Namespace = "issue"
			result.Number = number
		}
		for i, a := range attachments {
			result.Files = append(result.Files, downloadFile{
				Name: a.Path,
				Path: writtenPaths[i],
				Size: a.Size,
				SHA:  a.SHA,
			})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: encode json: %v\n", err)
			return 1
		}
		return 0
	}

	// Non-JSON mode: written paths go to stdout (one per line) so
	// pipelines like `gh attach get 42 | xargs gimp` work. Narrative
	// progress (what landed where + humanized sizes) goes to stderr
	// so interactive users see it without polluting pipe consumers.
	for i, a := range attachments {
		_, _ = fmt.Fprintln(stdout, writtenPaths[i])
		_, _ = fmt.Fprintf(stderr, "  %s → %s (%s)\n", a.Path, writtenPaths[i], humanizeBytes(a.Size))
	}
	_, _ = fmt.Fprintf(stderr, "Downloaded %d file(s) to %s\n", len(attachments), *outputDir)
	return 0
}

// humanizeBytes renders a byte count as B / KiB / MiB / GiB / TiB /
// PiB / EiB using binary (1024) units. Chosen for consistency with
// `ls -lh` / `du -h`. Values below 1 KiB show the raw byte count
// with a "B" suffix; larger values use one decimal place.
//
// The units slice covers the full int64 range: int64 maxes out at
// 2^63 - 1 ≈ 8 EiB, and the loop's exp can reach at most 5 for any
// representable byte count, so units[exp] is always in bounds.
// PiB/EiB are overkill for screenshots but cheap to include and
// eliminate any risk of an out-of-bounds panic if a future caller
// passes a much larger size.
func humanizeBytes(n int64) string {
	const kib = 1024
	if n < kib {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(kib), 0
	for m := n / kib; m >= kib; m /= kib {
		div *= kib
		exp++
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}
