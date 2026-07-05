package cli

import (
	"bytes"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enthus-appdev/gh-attach/internal/gh"
)

// capturingGitClient records the files handed to PushAttachments so a
// test can assert what actually gets uploaded, and returns canned
// AttachmentPaths derived from those files' basenames.
type capturingGitClient struct {
	gotFiles []string
}

func (c *capturingGitClient) PushAttachments(_ *gh.Repo, _, _ string, files []string) ([]gh.AttachmentPath, string, error) {
	c.gotFiles = files
	paths := make([]gh.AttachmentPath, 0, len(files))
	for _, f := range files {
		paths = append(paths, gh.AttachmentPath{Path: filepath.Base(f)})
	}
	return paths, "deadbeef", nil
}
func (c *capturingGitClient) ListRefs(*gh.Repo, string) ([]gh.RefEntry, error) { return nil, nil }
func (c *capturingGitClient) DeleteRef(*gh.Repo, string) error                 { return nil }
func (c *capturingGitClient) GetAttachments(*gh.Repo, string) ([]gh.Attachment, string, error) {
	return nil, "", nil
}

func TestRun_GifMode_UploadsSingleGif(t *testing.T) {
	dir := t.TempDir()
	// writePNG is the shared helper from gif_test.go (same package).
	f0 := writePNG(t, dir, "frame-000.png", 8, 6, color.RGBA{200, 0, 0, 255})
	f1 := writePNG(t, dir, "frame-001.png", 8, 6, color.RGBA{0, 0, 200, 255})

	cap := &capturingGitClient{}
	deps := runDeps{
		resolveRepo:  func(string) (*gh.Repo, error) { return &gh.Repo{Owner: "o", Name: "r"}, nil },
		resolvePR:    func(*gh.Repo) (int, error) { return 42, nil },
		newGitClient: func() (gitDataClient, error) { return cap, nil },
		newCmtClient: func() (commentClient, error) { return nil, nil },
		expandFiles:  expandFiles,
		stdin:        strings.NewReader(""),
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--gif", "42", f0, f1}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	if len(cap.gotFiles) != 1 {
		t.Fatalf("PushAttachments got %d files, want 1: %v", len(cap.gotFiles), cap.gotFiles)
	}
	if !strings.HasSuffix(cap.gotFiles[0], ".gif") {
		t.Errorf("uploaded file = %q, want a .gif", cap.gotFiles[0])
	}
}

func TestRun_GifMode_RejectsStdin(t *testing.T) {
	deps := runDeps{
		resolveRepo:  func(string) (*gh.Repo, error) { return &gh.Repo{Owner: "o", Name: "r"}, nil },
		resolvePR:    func(*gh.Repo) (int, error) { return 42, nil },
		newGitClient: func() (gitDataClient, error) { return &capturingGitClient{}, nil },
		newCmtClient: func() (commentClient, error) { return nil, nil },
		expandFiles:  expandFiles,
		stdin:        strings.NewReader("x"),
	}
	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--gif", "--name", "clip.gif", "42", "-"}, &stdout, &stderr, deps)
	if code == 0 {
		t.Fatalf("expected non-zero exit for --gif with stdin, got 0")
	}
	if !strings.Contains(stderr.String(), "gif") {
		t.Errorf("stderr should explain the --gif/stdin conflict, got: %s", stderr.String())
	}
}

// gifModeDeps builds the runDeps fixture shared by the validation
// rejection tests below: a resolvable repo/PR and a capturing git
// client that would reveal whether PushAttachments was ever reached.
func gifModeDeps() (runDeps, *capturingGitClient) {
	captured := &capturingGitClient{}
	return runDeps{
		resolveRepo:  func(string) (*gh.Repo, error) { return &gh.Repo{Owner: "o", Name: "r"}, nil },
		resolvePR:    func(*gh.Repo) (int, error) { return 42, nil },
		newGitClient: func() (gitDataClient, error) { return captured, nil },
		newCmtClient: func() (commentClient, error) { return nil, nil },
		expandFiles:  expandFiles,
		stdin:        strings.NewReader(""),
	}, captured
}

func TestRun_GifMode_RejectsOutOfRangeColors(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "frame-000.png", 8, 6, color.RGBA{200, 0, 0, 255})
	deps, captured := gifModeDeps()

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--gif", "--colors", "1", "42", f0}, &stdout, &stderr, deps)
	if code == 0 {
		t.Fatalf("expected non-zero exit for --colors out of range, got 0")
	}
	if !strings.Contains(stderr.String(), "--colors") {
		t.Errorf("stderr should explain the --colors range violation, got: %s", stderr.String())
	}
	if captured.gotFiles != nil {
		t.Errorf("PushAttachments should not have been called, got: %v", captured.gotFiles)
	}
}

func TestRun_GifMode_RejectsOutOfRangeDelay(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "frame-000.png", 8, 6, color.RGBA{200, 0, 0, 255})
	deps, captured := gifModeDeps()

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--gif", "--delay", "5", "42", f0}, &stdout, &stderr, deps)
	if code == 0 {
		t.Fatalf("expected non-zero exit for --delay below 20ms, got 0")
	}
	if !strings.Contains(stderr.String(), "--delay") {
		t.Errorf("stderr should explain the --delay range violation, got: %s", stderr.String())
	}
	if captured.gotFiles != nil {
		t.Errorf("PushAttachments should not have been called, got: %v", captured.gotFiles)
	}
}

// gifBytesCapturingGitClient additionally reads the pushed file's
// bytes at push time. assembleGIF's temp file is removed by its
// deferred cleanup before runWithDeps returns to the test, so bytes
// must be captured synchronously inside PushAttachments rather than
// read back afterward.
type gifBytesCapturingGitClient struct {
	capturingGitClient
	gotBytes []byte
}

func (c *gifBytesCapturingGitClient) PushAttachments(repo *gh.Repo, refPath, commitMessage string, files []string) ([]gh.AttachmentPath, string, error) {
	if len(files) == 1 {
		b, err := os.ReadFile(files[0])
		if err != nil {
			return nil, "", err
		}
		c.gotBytes = b
	}
	return c.capturingGitClient.PushAttachments(repo, refPath, commitMessage, files)
}

// TestRun_GifMode_AcceptsMaxDelay locks in the boundary itself (655350
// is the largest --delay whose rounded centisecond value still fits
// the GIF format's 16-bit delay field) so the rejection above is
// proven to be an off-by-one-safe `>`, not an overly strict `>=`. It
// also decodes the produced GIF to confirm the CLI's delayMS actually
// reaches the encoded frame delay (in centiseconds) unchanged, rather
// than just checking that the CLI accepted the flag.
func TestRun_GifMode_AcceptsMaxDelay(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "frame-000.png", 8, 6, color.RGBA{200, 0, 0, 255})
	client := &gifBytesCapturingGitClient{}
	deps := runDeps{
		resolveRepo:  func(string) (*gh.Repo, error) { return &gh.Repo{Owner: "o", Name: "r"}, nil },
		resolvePR:    func(*gh.Repo) (int, error) { return 42, nil },
		newGitClient: func() (gitDataClient, error) { return client, nil },
		newCmtClient: func() (commentClient, error) { return nil, nil },
		expandFiles:  expandFiles,
		stdin:        strings.NewReader(""),
	}

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--gif", "--delay", "655350", "42", f0}, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	if len(client.gotFiles) != 1 || !strings.HasSuffix(client.gotFiles[0], ".gif") {
		t.Fatalf("PushAttachments got %v, want exactly one .gif", client.gotFiles)
	}

	g, err := gif.DecodeAll(bytes.NewReader(client.gotBytes))
	if err != nil {
		t.Fatalf("decode produced gif: %v", err)
	}
	if len(g.Delay) == 0 || g.Delay[0] != 65535 {
		t.Errorf("gif delay = %v, want [65535]", g.Delay)
	}
}

// TestRun_GifMode_RejectsOversizedDelay locks in the upper bound: GIF
// stores each frame's delay as a 16-bit centisecond count, so a
// --delay whose rounded/10 value would exceed that must be rejected
// rather than silently wrapping into a fast, wrong playback speed.
func TestRun_GifMode_RejectsOversizedDelay(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "frame-000.png", 8, 6, color.RGBA{200, 0, 0, 255})
	deps, captured := gifModeDeps()

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--gif", "--delay", "700000", "42", f0}, &stdout, &stderr, deps)
	if code == 0 {
		t.Fatalf("expected non-zero exit for --delay above 655350ms, got 0")
	}
	if !strings.Contains(stderr.String(), "--delay") {
		t.Errorf("stderr should explain the --delay range violation, got: %s", stderr.String())
	}
	if captured.gotFiles != nil {
		t.Errorf("PushAttachments should not have been called, got: %v", captured.gotFiles)
	}
}

// TestRun_GifMode_RejectsNameWithoutGifExtension locks in that a --gif
// --name is required to end in .gif: FormatSection (comment.go) picks
// inline-vs-link rendering purely from the uploaded basename's
// extension, so a mismatched name would silently defeat --gif's whole
// point (autoplay inline instead of a plain download link).
func TestRun_GifMode_RejectsNameWithoutGifExtension(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "frame-000.png", 8, 6, color.RGBA{200, 0, 0, 255})
	deps, captured := gifModeDeps()

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--gif", "--name", "verify.png", "42", f0}, &stdout, &stderr, deps)
	if code == 0 {
		t.Fatalf("expected non-zero exit for --name without a .gif extension, got 0")
	}
	if !strings.Contains(stderr.String(), "--name") {
		t.Errorf("stderr should explain the --name extension violation, got: %s", stderr.String())
	}
	if captured.gotFiles != nil {
		t.Errorf("PushAttachments should not have been called, got: %v", captured.gotFiles)
	}
}

// TestRun_GifMode_ValidatesName locks in the reviewer-flagged behavior:
// --name in gif mode is routed through validateName, not just the
// filepath.Base defense-in-depth inside assembleGIF. A name containing
// a path separator must be rejected before any frames are assembled.
func TestRun_GifMode_ValidatesName(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "frame-000.png", 8, 6, color.RGBA{200, 0, 0, 255})
	deps, captured := gifModeDeps()

	var stdout, stderr bytes.Buffer
	code := runWithDeps([]string{"--gif", "--name", "../evil.gif", "42", f0}, &stdout, &stderr, deps)
	if code == 0 {
		t.Fatalf("expected non-zero exit for --name with a path separator, got 0")
	}
	if !strings.Contains(stderr.String(), "--name") {
		t.Errorf("stderr should explain the --name violation, got: %s", stderr.String())
	}
	if captured.gotFiles != nil {
		t.Errorf("PushAttachments should not have been called, got: %v", captured.gotFiles)
	}
}
