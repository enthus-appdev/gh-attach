// Package gifenc assembles a sequence of still frames into an animated
// GIF. Each frame is independently quantized to its own ≤256-color
// palette (median cut) and dithered, then written as one image block.
package gifenc

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"

	"github.com/ericpauley/go-quantize/quantize"
)

// Options tunes the animated-GIF output.
type Options struct {
	DelayMS   int // per-frame on-screen time in milliseconds
	NumColors int // per-frame palette size, clamped to [2,256]; 0 → 256
}

// Encode assembles frames into a single infinitely-looping animated
// GIF and returns its encoded bytes. Every frame must share the
// dimensions of frames[0] — the GIF canvas is fixed by the first
// image block, so a differently-sized frame cannot be represented
// without repositioning the whole animation.
func Encode(frames []image.Image, opts Options) ([]byte, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("gifenc: no frames")
	}

	numColors := opts.NumColors
	if numColors < 2 || numColors > 256 {
		numColors = 256
	}
	// GIF stores delay in 1/100 s. Round to nearest (not truncate) so
	// e.g. --delay 25 → 3, and clamp to ≥2 — sub-20ms rounds toward 0,
	// which most renderers reinterpret as 100ms.
	delay := (opts.DelayMS + 5) / 10
	if delay < 2 {
		delay = 2
	}
	// GIF stores delay as a uint16; clamp so a large --delay (or the
	// doubled delay on the size-ceiling re-encode path) cannot wrap.
	if delay > 65535 {
		delay = 65535
	}

	b0 := frames[0].Bounds()
	w, h := b0.Dx(), b0.Dy()
	if w == 0 || h == 0 {
		return nil, fmt.Errorf("gifenc: first frame is zero-sized")
	}

	g := &gif.GIF{LoopCount: 0}
	q := quantize.MedianCutQuantizer{}
	for i, fr := range frames {
		fb := fr.Bounds()
		if fb.Dx() != w || fb.Dy() != h {
			return nil, fmt.Errorf("gifenc: frame %d is %dx%d, want %dx%d — all frames must share dimensions", i, fb.Dx(), fb.Dy(), w, h)
		}
		pal := q.Quantize(make(color.Palette, 0, numColors), fr)
		dst := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		draw.FloydSteinberg.Draw(dst, dst.Bounds(), fr, fb.Min)
		g.Image = append(g.Image, dst)
		g.Delay = append(g.Delay, delay)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}

	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		return nil, fmt.Errorf("gifenc: encode: %w", err)
	}
	return buf.Bytes(), nil
}
