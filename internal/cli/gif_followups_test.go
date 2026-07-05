package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enthus-appdev/gh-attach/internal/gifenc"
)

func decodeGIFFrameCount(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open gif: %v", err)
	}
	defer func() { _ = f.Close() }()
	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatalf("decode gif: %v", err)
	}
	return len(g.Image)
}

func TestAssembleGIF_BothGuards(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 6; i++ {
		c := color.RGBA{uint8(i * 40), 0, uint8(255 - i*40), 255}
		paths = append(paths, writePNG(t, dir, fmt.Sprintf("f-%03d.png", i), 64, 64, c))
	}
	// maxFrames caps 6→4; sizeCeiling of 1 byte forces the reduction path.
	gifPath, cleanup, warning, err := assembleGIF(paths, gifAssembleOptions{
		delayMS: 80, numColors: 256, maxFrames: 4, sizeCeiling: 1,
	})
	if err != nil {
		t.Fatalf("assembleGIF: %v", err)
	}
	defer cleanup()
	if !strings.Contains(warning, "capped") {
		t.Errorf("warning missing frame-cap message: %q", warning)
	}
	if !strings.Contains(warning, "1-byte ceiling") {
		t.Errorf("warning missing size-ceiling message: %q", warning)
	}
	if n := decodeGIFFrameCount(t, gifPath); n == 0 {
		t.Errorf("assembled gif has no frames")
	}
}

func TestAssembleGIF_ReencodeFailureFallback(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 4; i++ {
		paths = append(paths, writePNG(t, dir, fmt.Sprintf("f-%03d.png", i), 32, 32, color.RGBA{uint8(i * 60), 10, 10, 255}))
	}
	gifPath, cleanup, warning, err := assembleGIF(paths, gifAssembleOptions{
		delayMS: 80, numColors: 256, sizeCeiling: 1,
		reencode: func([]image.Image, gifenc.Options) ([]byte, error) {
			return nil, fmt.Errorf("boom")
		},
	})
	if err != nil {
		t.Fatalf("assembleGIF should not error when only the re-encode fails: %v", err)
	}
	defer cleanup()
	if !strings.Contains(warning, "re-encode failed") || !strings.Contains(warning, "uploaded the original") {
		t.Errorf("warning should describe the fallback, got: %q", warning)
	}
	// The kept file is the first encode — all 4 frames, not the halved reduction.
	if n := decodeGIFFrameCount(t, gifPath); n != 4 {
		t.Errorf("kept gif has %d frames, want the original 4", n)
	}
}

func TestAssembleGIF_NameWithoutImageExt(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "a.png", 4, 4, color.White)
	gifPath, cleanup, warning, err := assembleGIF([]string{f0}, gifAssembleOptions{name: "verify", delayMS: 80})
	if err != nil {
		t.Fatalf("assembleGIF: %v", err)
	}
	defer cleanup()
	if got := filepath.Base(gifPath); got != "verify.gif" {
		t.Errorf("output basename = %q, want verify.gif", got)
	}
	if !strings.Contains(warning, "not a .gif") {
		t.Errorf("warning should note the forced .gif extension, got: %q", warning)
	}
}

func TestAssembleGIF_NameGifExtUnchanged(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "a.png", 4, 4, color.White)
	// An uppercase .GIF is still a GIF extension — matched case-insensitively,
	// so it is left as-is rather than doubled to "shot.GIF.gif".
	for _, name := range []string{"shot.gif", "shot.GIF"} {
		gifPath, cleanup, warning, err := assembleGIF([]string{f0}, gifAssembleOptions{name: name, delayMS: 80})
		if err != nil {
			t.Fatalf("assembleGIF(%q): %v", name, err)
		}
		if got := filepath.Base(gifPath); got != name {
			t.Errorf("output basename = %q, want %q (unchanged)", got, name)
		}
		if warning != "" {
			t.Errorf("name %q already a .gif, warning should be empty, got: %q", name, warning)
		}
		cleanup()
	}
}

func TestAssembleGIF_NameNonGifExtNormalized(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "a.png", 4, 4, color.White)
	// The payload is a GIF, so a non-.gif image extension is normalized to
	// .gif (not left as-is) — GitHub keys the content-type off the extension.
	gifPath, cleanup, warning, err := assembleGIF([]string{f0}, gifAssembleOptions{name: "chart.png", delayMS: 80})
	if err != nil {
		t.Fatalf("assembleGIF: %v", err)
	}
	defer cleanup()
	if got := filepath.Base(gifPath); got != "chart.gif" {
		t.Errorf("output basename = %q, want chart.gif (normalized)", got)
	}
	if !strings.Contains(warning, "not a .gif") {
		t.Errorf("warning should note the forced .gif extension, got: %q", warning)
	}
}

func TestRun_GifMode_JSONSurfacesWarning(t *testing.T) {
	dir := t.TempDir()
	var frames []string
	for i := 0; i < 5; i++ {
		frames = append(frames, writePNG(t, dir, fmt.Sprintf("frame-%03d.png", i), 8, 6, color.RGBA{uint8(i * 50), 0, 0, 255}))
	}
	deps, _ := gifModeDeps()

	var stdout, stderr bytes.Buffer
	args := append([]string{"--gif", "--json", "--max-frames", "2", "42"}, frames...)
	code := runWithDeps(args, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	var res uploadResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(res.Warning, "capped") {
		t.Errorf("JSON warning field should carry the frame-cap message, got: %q", res.Warning)
	}
	if strings.Contains(stderr.String(), "warning:") {
		t.Errorf("stderr must stay quiet in --json mode, got: %s", stderr.String())
	}
}

func TestRun_GifMode_StderrWarningNonJSON(t *testing.T) {
	dir := t.TempDir()
	var frames []string
	for i := 0; i < 5; i++ {
		frames = append(frames, writePNG(t, dir, fmt.Sprintf("frame-%03d.png", i), 8, 6, color.RGBA{0, uint8(i * 50), 0, 255}))
	}
	deps, _ := gifModeDeps()

	var stdout, stderr bytes.Buffer
	args := append([]string{"--gif", "--max-frames", "2", "42"}, frames...)
	code := runWithDeps(args, &stdout, &stderr, deps)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning:") || !strings.Contains(stderr.String(), "capped") {
		t.Errorf("stderr should carry the frame-cap warning, got: %s", stderr.String())
	}
	if strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Errorf("stdout should be markdown, not JSON, got: %s", stdout.String())
	}
}
