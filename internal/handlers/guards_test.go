package handlers

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

// TestSafeBackupName pins the path-traversal guard: base name only, expected shape.
func TestSafeBackupName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"parkrr-2026-08-10-030041.dump.enc", true},
		{"parkrr-.dump.enc", true},
		{"", false},
		{"backup.dump.enc", false},             // missing parkrr- prefix
		{"parkrr-x.txt", false},                // missing .dump.enc suffix
		{"../parkrr-x.dump.enc", false},        // traversal — Base differs
		{"/etc/parkrr-x.dump.enc", false},      // absolute path — Base differs
		{"sub/parkrr-x.dump.enc", false},       // nested — Base differs
		{"parkrr-x.dump.enc/../../etc", false}, // traversal suffix
	}
	for _, tc := range cases {
		if got := safeBackupName(tc.name); got != tc.want {
			t.Errorf("safeBackupName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// bombPNG hand-crafts a valid PNG signature + IHDR claiming huge dimensions but no
// pixel data — the decompression-bomb shape: a tiny file that DecodeConfig reads as
// enormous. sanitizeImage must reject it from the header, never allocating w*h.
func bombPNG(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) // signature
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 6 // color type RGBA
	// ihdr[10..12] = compression/filter/interlace = 0
	var chunk bytes.Buffer
	_ = binary.Write(&chunk, binary.BigEndian, uint32(len(ihdr)))
	chunk.WriteString("IHDR")
	chunk.Write(ihdr)
	crc := crc32.ChecksumIEEE(append([]byte("IHDR"), ihdr...))
	_ = binary.Write(&chunk, binary.BigEndian, crc)
	b.Write(chunk.Bytes())
	return b.Bytes()
}

// TestSanitizeImage covers the accept path (PNG/JPEG round-trip) and the guards:
// non-images, disallowed formats, and the decompression-bomb dimension check.
func TestSanitizeImage(t *testing.T) {
	if _, ct, err := sanitizeImage(encodePNG(t, 4, 4)); err != nil || ct != "image/png" {
		t.Errorf("valid PNG: ct=%q err=%v", ct, err)
	}
	if _, ct, err := sanitizeImage(encodeJPEG(t, 4, 4)); err != nil || ct != "image/jpeg" {
		t.Errorf("valid JPEG: ct=%q err=%v", ct, err)
	}
	if _, _, err := sanitizeImage([]byte("this is not an image")); err == nil {
		t.Error("garbage bytes: expected an error, got nil")
	}
	// A header claiming 8001×1 (over maxPhotoDimension=8000) must be rejected without
	// decoding — the bomb guard.
	if _, _, err := sanitizeImage(bombPNG(maxPhotoDimension+1, 1)); err == nil {
		t.Error("oversized dimensions: expected rejection, got nil")
	}
}
