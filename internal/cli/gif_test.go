package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writePNG writes a solid w×h PNG to dir/name and returns its path.
func writePNG(t *testing.T, dir, name string, w, h int, c color.Color) string {
	t.Helper()
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, c)
		}
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create %s: %v", p, err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, m); err != nil {
		t.Fatalf("encode %s: %v", p, err)
	}
	return p
}

func TestAssembleGIF_TwoFrames(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "frame-000.png", 8, 6, color.RGBA{255, 0, 0, 255})
	f1 := writePNG(t, dir, "frame-001.png", 8, 6, color.RGBA{0, 0, 255, 255})

	gifPath, cleanup, err := assembleGIF([]string{f0, f1}, gifAssembleOptions{delayMS: 80, numColors: 64})
	if err != nil {
		t.Fatalf("assembleGIF: %v", err)
	}
	defer cleanup()

	if filepath.Base(gifPath) != "clip.gif" {
		t.Errorf("gif basename = %q, want clip.gif", filepath.Base(gifPath))
	}
	data, err := os.ReadFile(gifPath)
	if err != nil {
		t.Fatalf("read gif: %v", err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	if len(g.Image) != 2 {
		t.Fatalf("frame count = %d, want 2", len(g.Image))
	}

	cleanup()
	if _, err := os.Stat(gifPath); !os.IsNotExist(err) {
		t.Errorf("temp gif still present after cleanup: %v", err)
	}
}

func TestAssembleGIF_CustomName(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "a.png", 4, 4, color.White)
	gifPath, cleanup, err := assembleGIF([]string{f0}, gifAssembleOptions{name: "verify.gif", delayMS: 80})
	if err != nil {
		t.Fatalf("assembleGIF: %v", err)
	}
	defer cleanup()
	if filepath.Base(gifPath) != "verify.gif" {
		t.Errorf("gif basename = %q, want verify.gif", filepath.Base(gifPath))
	}
}

func TestAssembleGIF_NoFrames(t *testing.T) {
	if _, _, err := assembleGIF(nil, gifAssembleOptions{}); err == nil {
		t.Fatal("expected error for no frames, got nil")
	}
}

func TestAssembleGIF_BadFrame(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "notimage.png")
	if err := os.WriteFile(bad, []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := assembleGIF([]string{bad}, gifAssembleOptions{delayMS: 80}); err == nil {
		t.Fatal("expected decode error for non-image file, got nil")
	}
}
