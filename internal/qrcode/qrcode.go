// Package qrcode generates QR codes (PNG and SVG) for subscription URLs,
// individual protocol share links, and generated configs. Rendering is done
// directly from the encoder's module bitmap so size, margin, and colors are
// fully configurable — skip2/go-qrcode's built-in PNG() only supports a
// fixed quiet zone and doesn't produce SVG at all.
package qrcode

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"

	"github.com/skip2/go-qrcode"
)

// Options configures QR rendering. Size is the total output width/height in
// pixels (square). Margin is the quiet-zone width expressed in QR "modules"
// (the standard recommends >= 4). Foreground/Background are hex colors like
// "#000000".
type Options struct {
	Size       int
	Margin     int
	Foreground string
	Background string
}

// DefaultOptions returns sane defaults matching the standard's recommended
// quiet zone.
func DefaultOptions() Options {
	return Options{Size: 256, Margin: 4, Foreground: "#000000", Background: "#FFFFFF"}
}

func bitmapFor(content string) ([][]bool, error) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("qrcode: encode: %w", err)
	}
	q.DisableBorder = true // we render our own configurable quiet zone
	return q.Bitmap(), nil
}

func parseHexColor(s string) (color.RGBA, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.RGBA{}, fmt.Errorf("qrcode: invalid hex color %q, want RRGGBB", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("qrcode: invalid hex color %q: %w", s, err)
	}
	return color.RGBA{
		R: uint8(v >> 16),
		G: uint8(v >> 8),
		B: uint8(v),
		A: 0xFF,
	}, nil
}

// GeneratePNG renders content as a PNG-encoded QR code per opts.
func GeneratePNG(content string, opts Options) ([]byte, error) {
	bitmap, err := bitmapFor(content)
	if err != nil {
		return nil, err
	}

	fg, err := parseHexColor(opts.Foreground)
	if err != nil {
		return nil, err
	}
	bg, err := parseHexColor(opts.Background)
	if err != nil {
		return nil, err
	}

	moduleCount := len(bitmap) + 2*opts.Margin
	if moduleCount <= 0 || opts.Size <= 0 {
		return nil, fmt.Errorf("qrcode: invalid size/margin combination")
	}
	pixelsPerModule := opts.Size / moduleCount
	if pixelsPerModule < 1 {
		pixelsPerModule = 1
	}
	actualSize := pixelsPerModule * moduleCount

	img := image.NewRGBA(image.Rect(0, 0, actualSize, actualSize))
	for y := 0; y < actualSize; y++ {
		for x := 0; x < actualSize; x++ {
			img.Set(x, y, bg)
		}
	}

	for row := range bitmap {
		for col := range bitmap[row] {
			if !bitmap[row][col] {
				continue
			}
			x0 := (col + opts.Margin) * pixelsPerModule
			y0 := (row + opts.Margin) * pixelsPerModule
			for y := y0; y < y0+pixelsPerModule; y++ {
				for x := x0; x < x0+pixelsPerModule; x++ {
					img.Set(x, y, fg)
				}
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("qrcode: png encode: %w", err)
	}
	return buf.Bytes(), nil
}

// GenerateSVG renders content as an SVG QR code per opts. The SVG viewBox
// is sized in modules (bitmap size + 2*margin) and scales to opts.Size via
// width/height attributes, so it stays crisp at any display size.
func GenerateSVG(content string, opts Options) (string, error) {
	bitmap, err := bitmapFor(content)
	if err != nil {
		return "", err
	}

	if _, err := parseHexColor(opts.Foreground); err != nil {
		return "", err
	}
	if _, err := parseHexColor(opts.Background); err != nil {
		return "", err
	}

	dim := len(bitmap) + 2*opts.Margin
	if dim <= 0 || opts.Size <= 0 {
		return "", fmt.Errorf("qrcode: invalid size/margin combination")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges">`,
		dim, dim, opts.Size, opts.Size)
	fmt.Fprintf(&sb, `<rect width="%d" height="%d" fill="%s"/>`, dim, dim, opts.Background)

	for row := range bitmap {
		for col := range bitmap[row] {
			if !bitmap[row][col] {
				continue
			}
			fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="1" height="1" fill="%s"/>`,
				col+opts.Margin, row+opts.Margin, opts.Foreground)
		}
	}

	sb.WriteString(`</svg>`)
	return sb.String(), nil
}
