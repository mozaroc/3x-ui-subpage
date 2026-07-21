package qrcode

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestGeneratePNG_ValidImage(t *testing.T) {
	opts := DefaultOptions()
	data, err := GeneratePNG("https://sub.example.com/sub/tok-abc", opts)
	if err != nil {
		t.Fatalf("GeneratePNG: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode generated PNG: %v", err)
	}

	b := img.Bounds()
	if b.Dx() != b.Dy() {
		t.Errorf("expected square image, got %dx%d", b.Dx(), b.Dy())
	}
	if b.Dx() == 0 {
		t.Errorf("expected non-zero image dimensions")
	}
}

func TestGeneratePNG_CustomSizeAndMargin(t *testing.T) {
	opts := Options{Size: 512, Margin: 8, Foreground: "#111111", Background: "#EEEEEE"}
	data, err := GeneratePNG("https://sub.example.com/sub/tok-abc", opts)
	if err != nil {
		t.Fatalf("GeneratePNG: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if img.Bounds().Dx() > 512 || img.Bounds().Dx() < 400 {
		t.Errorf("expected image size near 512px, got %d", img.Bounds().Dx())
	}
}

func TestGeneratePNG_InvalidColor(t *testing.T) {
	opts := Options{Size: 256, Margin: 4, Foreground: "notacolor", Background: "#FFFFFF"}
	if _, err := GeneratePNG("content", opts); err == nil {
		t.Fatal("expected error for invalid foreground color")
	}
}

func TestGenerateSVG_WellFormed(t *testing.T) {
	opts := DefaultOptions()
	svg, err := GenerateSVG("https://sub.example.com/sub/tok-abc", opts)
	if err != nil {
		t.Fatalf("GenerateSVG: %v", err)
	}
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Errorf("expected well-formed svg wrapper, got: %s", svg[:min(80, len(svg))])
	}
	if !strings.Contains(svg, `fill="#000000"`) {
		t.Errorf("expected foreground color in svg output")
	}
	if !strings.Contains(svg, `<rect`) {
		t.Errorf("expected rect elements in svg output")
	}
}

func TestGenerateSVG_InvalidColor(t *testing.T) {
	opts := Options{Size: 256, Margin: 4, Foreground: "#000000", Background: "bogus"}
	if _, err := GenerateSVG("content", opts); err == nil {
		t.Fatal("expected error for invalid background color")
	}
}

func TestGeneratePNG_DifferentContentProducesDifferentOutput(t *testing.T) {
	opts := DefaultOptions()
	a, err := GeneratePNG("https://sub.example.com/sub/tok-abc", opts)
	if err != nil {
		t.Fatalf("GeneratePNG: %v", err)
	}
	b, err := GeneratePNG("https://sub.example.com/sub/tok-xyz", opts)
	if err != nil {
		t.Fatalf("GeneratePNG: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Error("expected different content to produce different PNG output")
	}
}

func TestGenerateSVG_DifferentContentProducesDifferentOutput(t *testing.T) {
	opts := DefaultOptions()
	a, err := GenerateSVG("https://sub.example.com/sub/tok-abc", opts)
	if err != nil {
		t.Fatalf("GenerateSVG: %v", err)
	}
	b, err := GenerateSVG("https://sub.example.com/sub/tok-xyz", opts)
	if err != nil {
		t.Fatalf("GenerateSVG: %v", err)
	}
	if a == b {
		t.Error("expected different content to produce different SVG output")
	}
}
