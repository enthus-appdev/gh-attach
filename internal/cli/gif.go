package cli

import (
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG decoder for image.Decode
	_ "image/png"  // register PNG decoder for image.Decode
	"os"
	"path/filepath"

	"github.com/enthus-appdev/gh-attach/internal/gifenc"
)

// gifAssembleOptions carries the flag-derived tunables for --gif mode.
type gifAssembleOptions struct {
	name      string // output basename; empty → "clip.gif"
	delayMS   int
	numColors int
}

// assembleGIF decodes the ordered frame files (PNG or JPEG), encodes
// them into one animated GIF, and writes it to a fresh temp dir.
// framePaths are used in slice order — globbed inputs arrive
// lexicographically, so zero-padded frame-000.png … frame-NNN.png
// names sort into playback order. The returned cleanup closure removes
// the temp dir and must be deferred by the caller.
func assembleGIF(framePaths []string, opts gifAssembleOptions) (string, func(), error) {
	if len(framePaths) == 0 {
		return "", nil, fmt.Errorf("no frames to assemble")
	}

	frames := make([]image.Image, 0, len(framePaths))
	for _, p := range framePaths {
		img, err := decodeImageFile(p)
		if err != nil {
			return "", nil, err
		}
		frames = append(frames, img)
	}

	data, err := gifenc.Encode(frames, gifenc.Options{
		DelayMS:   opts.delayMS,
		NumColors: opts.numColors,
	})
	if err != nil {
		return "", nil, err
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
	tmpDir, err := os.MkdirTemp("", "gh-attach-gif-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }

	gifPath := filepath.Join(tmpDir, name)
	if err := os.WriteFile(gifPath, data, 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write gif: %w", err)
	}
	return gifPath, cleanup, nil
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
