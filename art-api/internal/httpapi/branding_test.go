package httpapi

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func TestValidateBrandingLogo(t *testing.T) {
	var raster bytes.Buffer
	logo := image.NewRGBA(image.Rect(0, 0, 2, 2))
	logo.Set(0, 0, color.RGBA{R: 20, G: 110, B: 255, A: 255})
	if err := png.Encode(&raster, logo); err != nil {
		t.Fatal(err)
	}
	if mediaType, err := validateBrandingLogo(raster.Bytes()); err != nil || mediaType != "image/png" {
		t.Fatalf("valid PNG rejected: media_type=%q err=%v", mediaType, err)
	}
	validSVG := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><path d="M0 0h32v32H0z"/></svg>`)
	if mediaType, err := validateBrandingLogo(validSVG); err != nil || mediaType != "image/svg+xml" {
		t.Fatalf("valid SVG rejected: media_type=%q err=%v", mediaType, err)
	}
	unsafe := [][]byte{
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://tracker.example/logo.png"/></svg>`),
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`),
		[]byte(`<!DOCTYPE svg><svg xmlns="http://www.w3.org/2000/svg"/>`),
	}
	for _, candidate := range unsafe {
		if _, err := validateBrandingLogo(candidate); err == nil {
			t.Fatalf("unsafe SVG accepted: %s", candidate)
		}
	}
}

func TestReplaceBrandingFile(t *testing.T) {
	path := t.TempDir() + "/logo.custom"
	if err := replaceBrandingFile(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := replaceBrandingFile(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "second" {
		t.Fatalf("logo was not replaced: data=%q err=%v", data, err)
	}
}
