package cli

import (
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.Decode
	_ "image/png"  // register PNG decoder for image.Decode
	"os"
	"path/filepath"
	"strings"

	"github.com/enthus-appdev/gh-attach/internal/gifenc"
)

// gifAssembleOptions carries the flag-derived tunables for --gif mode.
type gifAssembleOptions struct {
	name        string // output basename; empty → "clip.gif"
	delayMS     int
	numColors   int
	maxFrames   int   // cap on frames; 0 → no cap. Excess frames are evenly sampled out.
	sizeCeiling int64 // byte ceiling; 0 → no ceiling. Over → one reduced re-encode.
	// reencode is the size-ceiling reduction encoder. nil → gifenc.Encode.
	// A per-call seam (not a package var) so a test can force the reduction
	// to fail without a shared mutable global that would race under t.Parallel.
	reencode func([]image.Image, gifenc.Options) ([]byte, error)
}

// assembleGIF decodes the ordered frame files (PNG or JPEG), applies
// the frame cap, encodes an animated GIF, and (if a size ceiling is
// set and exceeded) re-encodes once with a smaller palette and half
// the frames. framePaths are used in slice order — globbed inputs
// arrive lexicographically, so zero-padded frame-000.png … frame-NNN.png
// names sort into playback order. Returns the temp gif path, a cleanup
// closure (removes the temp dir; must be deferred by the caller), and
// a human-readable warning — every guard that fired contributes a
// message, joined with "; "; empty when none fired.
func assembleGIF(framePaths []string, opts gifAssembleOptions) (string, func(), string, error) {
	if len(framePaths) == 0 {
		return "", nil, "", fmt.Errorf("no frames to assemble")
	}

	// Accumulate — both the frame-cap and size-ceiling guards can fire
	// in one call, and neither message should silently clobber the other.
	var warnings []string

	// Frame cap: evenly sample down to maxFrames so playback covers the
	// whole clip rather than truncating the tail.
	paths := framePaths
	if opts.maxFrames > 0 && len(paths) > opts.maxFrames {
		paths = sampleEvenly(paths, opts.maxFrames)
		warnings = append(warnings, fmt.Sprintf("capped %d frames to %d (--max-frames)", len(framePaths), len(paths)))
	}

	frames := make([]image.Image, 0, len(paths))
	for _, p := range paths {
		img, err := decodeImageFile(p)
		if err != nil {
			return "", nil, "", err
		}
		frames = append(frames, img)
	}

	data, err := gifenc.Encode(frames, gifenc.Options{DelayMS: opts.delayMS, NumColors: opts.numColors})
	if err != nil {
		return "", nil, "", err
	}

	// Size ceiling: one reduced re-encode — fewer colors, half the
	// frames (delay doubled so playback speed is preserved — except at
	// extreme per-frame delays, where the doubled value is clamped at
	// the encoder's uint16 centisecond max instead of preserving speed).
	// The palette is never raised above what the caller asked for, or
	// the "reduction" could grow the file for a low --colors value.
	if opts.sizeCeiling > 0 && int64(len(data)) > opts.sizeCeiling {
		reducedColors := 64
		if opts.numColors > 0 && opts.numColors < reducedColors {
			reducedColors = opts.numColors
		}
		reencode := opts.reencode
		if reencode == nil {
			reencode = gifenc.Encode
		}
		reduced := sampleEvenly(frames, (len(frames)+1)/2)
		data2, err2 := reencode(reduced, gifenc.Options{DelayMS: opts.delayMS * 2, NumColors: reducedColors})
		if err2 != nil {
			// The first encode already produced a valid (if oversized) GIF;
			// prefer shipping it over failing outright when only the
			// reduction step errors. Keep `data` as-is and warn.
			warnings = append(warnings, fmt.Sprintf("gif is %d bytes, over the %d-byte ceiling, and the reduction re-encode failed (%v) — uploaded the original anyway", len(data), opts.sizeCeiling, err2))
		} else {
			data = data2
			if int64(len(data)) > opts.sizeCeiling {
				warnings = append(warnings, fmt.Sprintf("gif is %d bytes, over the %d-byte ceiling even after reduction — uploaded anyway", len(data), opts.sizeCeiling))
			} else {
				warnings = append(warnings, fmt.Sprintf("gif exceeded the %d-byte ceiling — reduced to %d colors / %d frames", opts.sizeCeiling, reducedColors, len(reduced)))
			}
		}
	}

	// filepath.Base defends assembleGIF as a standalone function: the CLI
	// validates --name upstream, but a path in opts.name must never let
	// the write escape the temp dir. Base alone isn't enough for three
	// degenerate inputs it returns unchanged (nothing to strip): ".."
	// (Join would resolve outside tmpDir), "." (Join collapses to
	// tmpDir itself), and a bare separator "/" (same collapse — Base
	// of an all-separator path is a single separator). Reject all
	// three and fall back to the default name.
	name := "clip.gif"
	if opts.name != "" {
		base := filepath.Base(opts.name)
		if base != "." && base != ".." && base != "/" && base != string(filepath.Separator) {
			name = base
		}
	}
	// The payload is always a GIF, and GitHub derives an attachment's
	// content-type from its name extension: any non-.gif extension (even
	// another image type like .png) can render as a static still instead of
	// the inline autoplay --gif exists to produce. Force a .gif extension —
	// case-insensitive so an existing .GIF isn't doubled — and warn on change.
	if !strings.EqualFold(filepath.Ext(name), ".gif") {
		fixed := strings.TrimSuffix(name, filepath.Ext(name)) + ".gif"
		warnings = append(warnings, fmt.Sprintf("--name %q is not a .gif; using %q so the clip animates inline", name, fixed))
		name = fixed
	}
	tmpDir, err := os.MkdirTemp("", "gh-attach-gif-*")
	if err != nil {
		return "", nil, "", fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	gifPath := filepath.Join(tmpDir, name)
	if err := os.WriteFile(gifPath, data, 0o600); err != nil {
		cleanup()
		return "", nil, "", fmt.Errorf("write gif: %w", err)
	}
	return gifPath, cleanup, strings.Join(warnings, "; "), nil
}

// sampleEvenly returns n items drawn at even intervals across in,
// always including the first and (when n>1) the last. If n>=len(in)
// or n<=0 it returns in unchanged.
func sampleEvenly[T any](in []T, n int) []T {
	if n <= 0 || n >= len(in) {
		return in
	}
	if n == 1 {
		return in[:1]
	}
	out := make([]T, 0, n)
	// step across [0, len-1] so both ends are represented.
	for i := 0; i < n; i++ {
		idx := i * (len(in) - 1) / (n - 1)
		out = append(out, in[idx])
	}
	return out
}

// decodeImageFile opens and decodes a single frame file. Format is
// detected by image.Decode from the registered PNG/JPEG decoders.
func decodeImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open frame %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode frame %s: %w", path, err)
	}
	return img, nil
}
