package gifenc

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
)

// solid returns a w×h image filled with c, origin at (0,0).
func solid(w, h int, c color.Color) image.Image {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, c)
		}
	}
	return m
}

func TestEncode_ThreeFrames(t *testing.T) {
	frames := []image.Image{
		solid(8, 6, color.RGBA{255, 0, 0, 255}),
		solid(8, 6, color.RGBA{0, 255, 0, 255}),
		solid(8, 6, color.RGBA{0, 0, 255, 255}),
	}
	data, err := Encode(frames, Options{DelayMS: 80, NumColors: 256})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	if len(g.Image) != 3 {
		t.Fatalf("frame count = %d, want 3", len(g.Image))
	}
	if g.LoopCount != 0 {
		t.Errorf("LoopCount = %d, want 0 (infinite)", g.LoopCount)
	}
	for i, d := range g.Delay {
		if d != 8 { // 80ms → 8 centiseconds
			t.Errorf("Delay[%d] = %d, want 8", i, d)
		}
	}
	if b := g.Image[0].Bounds(); b.Dx() != 8 || b.Dy() != 6 {
		t.Errorf("frame 0 bounds = %v, want 8x6", b)
	}
}

func TestEncode_Empty(t *testing.T) {
	if _, err := Encode(nil, Options{}); err == nil {
		t.Fatal("expected error for empty frames, got nil")
	}
}

func TestEncode_MismatchedDimensions(t *testing.T) {
	frames := []image.Image{
		solid(8, 6, color.White),
		solid(4, 6, color.White),
	}
	if _, err := Encode(frames, Options{DelayMS: 80}); err == nil {
		t.Fatal("expected error for mismatched frame dimensions, got nil")
	}
}

func TestEncode_DelayClamp(t *testing.T) {
	// 5ms → 0 centiseconds unclamped; must clamp to 2.
	data, err := Encode([]image.Image{solid(2, 2, color.White)}, Options{DelayMS: 5, NumColors: 16})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	g, _ := gif.DecodeAll(bytes.NewReader(data))
	if g.Delay[0] != 2 {
		t.Errorf("clamped Delay = %d, want 2", g.Delay[0])
	}
}
