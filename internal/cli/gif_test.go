package cli

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"slices"
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

	gifPath, cleanup, _, err := assembleGIF([]string{f0, f1}, gifAssembleOptions{delayMS: 80, numColors: 64})
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
	gifPath, cleanup, _, err := assembleGIF([]string{f0}, gifAssembleOptions{name: "verify.gif", delayMS: 80})
	if err != nil {
		t.Fatalf("assembleGIF: %v", err)
	}
	defer cleanup()
	if filepath.Base(gifPath) != "verify.gif" {
		t.Errorf("gif basename = %q, want verify.gif", filepath.Base(gifPath))
	}
}

func TestAssembleGIF_NoFrames(t *testing.T) {
	if _, _, _, err := assembleGIF(nil, gifAssembleOptions{}); err == nil {
		t.Fatal("expected error for no frames, got nil")
	}
}

func TestAssembleGIF_BadFrame(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "notimage.png")
	if err := os.WriteFile(bad, []byte("not a png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := assembleGIF([]string{bad}, gifAssembleOptions{delayMS: 80}); err == nil {
		t.Fatal("expected decode error for non-image file, got nil")
	}
}

// TestAssembleGIF_NameTraversal pins that a bare "..", ".", or "/" in
// opts.name can't escape or collapse into the temp dir. filepath.Base
// has no separator to strip from any of these, so it returns them
// unchanged — Join would otherwise resolve outside tmpDir (for "..")
// or onto tmpDir itself (for "." and "/"), the latter failing the
// write with an is-a-directory error instead of falling back.
func TestAssembleGIF_NameTraversal(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "a.png", 4, 4, color.White)

	for _, name := range []string{"..", ".", "/"} {
		t.Run(name, func(t *testing.T) {
			gifPath, cleanup, _, err := assembleGIF([]string{f0}, gifAssembleOptions{name: name, delayMS: 80})
			if err != nil {
				t.Fatalf("assembleGIF: %v", err)
			}
			defer cleanup()

			if filepath.Base(gifPath) != "clip.gif" {
				t.Errorf("gif basename = %q, want clip.gif (fallback for %q)", filepath.Base(gifPath), name)
			}
			// The written file must sit inside its own temp dir, not
			// tmpDir's parent or tmpDir itself as a file.
			info, err := os.Stat(gifPath)
			if err != nil {
				t.Fatalf("stat gif: %v", err)
			}
			if info.IsDir() {
				t.Fatalf("gifPath %q is a directory, want a file", gifPath)
			}
		})
	}
}

func TestAssembleGIF_FrameCap(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 10; i++ {
		paths = append(paths, writePNG(t, dir, fmt.Sprintf("f-%03d.png", i), 8, 6, color.White))
	}
	gifPath, cleanup, warning, err := assembleGIF(paths, gifAssembleOptions{delayMS: 80, maxFrames: 4})
	if err != nil {
		t.Fatalf("assembleGIF: %v", err)
	}
	defer cleanup()
	data, _ := os.ReadFile(gifPath)
	g, _ := gif.DecodeAll(bytes.NewReader(data))
	if len(g.Image) > 4 {
		t.Errorf("frame count = %d, want ≤4 after cap", len(g.Image))
	}
	if warning == "" {
		t.Error("expected a warning when frames are capped")
	}
}

func TestAssembleGIF_NoCapNoWarning(t *testing.T) {
	dir := t.TempDir()
	f0 := writePNG(t, dir, "a.png", 4, 4, color.White)
	_, cleanup, warning, err := assembleGIF([]string{f0}, gifAssembleOptions{delayMS: 80, maxFrames: 300})
	if err != nil {
		t.Fatalf("assembleGIF: %v", err)
	}
	defer cleanup()
	if warning != "" {
		t.Errorf("unexpected warning: %q", warning)
	}
}

func TestAssembleGIF_SizeCeilingReencode(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 8; i++ {
		c := color.RGBA{uint8(i * 30), 0, 255, 255}
		paths = append(paths, writePNG(t, dir, fmt.Sprintf("f-%03d.png", i), 64, 64, c))
	}

	// Encode once with no ceiling to learn the full size, then set a
	// ceiling just below it to force exactly one reduced re-encode.
	full, cleanupFull, _, err := assembleGIF(paths, gifAssembleOptions{delayMS: 80, numColors: 256})
	if err != nil {
		t.Fatalf("assembleGIF(full): %v", err)
	}
	fullData, _ := os.ReadFile(full)
	cleanupFull()

	reduced, cleanup, warning, err := assembleGIF(paths, gifAssembleOptions{
		delayMS: 80, numColors: 256, sizeCeiling: int64(len(fullData) - 1),
	})
	if err != nil {
		t.Fatalf("assembleGIF(reduced): %v", err)
	}
	defer cleanup()

	redData, _ := os.ReadFile(reduced)
	if len(redData) >= len(fullData) {
		t.Errorf("re-encoded gif = %d bytes, want smaller than full %d", len(redData), len(fullData))
	}
	if warning == "" {
		t.Error("expected a warning when the size ceiling triggers a re-encode")
	}
}

// TestSampleEvenly pins sampleEvenly's boundary behavior directly:
// the pass-through cases (n<=0, n>=len(in)), the single-element case,
// and even sampling that always keeps both ends.
func TestSampleEvenly(t *testing.T) {
	tests := []struct {
		name string
		in   []int
		n    int
		want []int
	}{
		{"nil input passes through", nil, 3, nil},
		{"empty input passes through", []int{}, 3, []int{}},
		{"n zero passes through", []int{1, 2, 3}, 0, []int{1, 2, 3}},
		{"n negative passes through", []int{1, 2, 3}, -1, []int{1, 2, 3}},
		{"n equal to len passes through", []int{1, 2, 3}, 3, []int{1, 2, 3}},
		{"n greater than len passes through", []int{1, 2, 3}, 5, []int{1, 2, 3}},
		{"n one keeps first", []int{1, 2, 3}, 1, []int{1}},
		{"sample 3 from 5 keeps both ends", []int{1, 2, 3, 4, 5}, 3, []int{1, 3, 5}},
		{"sample 3 from 4 keeps both ends", []int{1, 2, 3, 4}, 3, []int{1, 2, 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sampleEvenly(tt.in, tt.n)
			if !slices.Equal(got, tt.want) {
				t.Errorf("sampleEvenly(%v, %d) = %v, want %v", tt.in, tt.n, got, tt.want)
			}
		})
	}
}
