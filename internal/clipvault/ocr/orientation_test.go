package ocr

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestJPEGOrientationAndMetadataRemoval(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 12; x++ {
			source.Set(x, y, color.RGBA{uint8(x * 20), uint8(y * 30), 0, 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, source, nil); err != nil {
		t.Fatal(err)
	}
	for orientation := 1; orientation <= 8; orientation++ {
		tiff := make([]byte, 26)
		copy(tiff, []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0})
		binary.LittleEndian.PutUint16(tiff[10:12], 0x112)
		binary.LittleEndian.PutUint16(tiff[12:14], 3)
		binary.LittleEndian.PutUint32(tiff[14:18], 1)
		binary.LittleEndian.PutUint16(tiff[18:20], uint16(orientation))
		segment := append([]byte("Exif\x00\x00"), tiff...)
		raw := append([]byte{}, encoded.Bytes()[:2]...)
		raw = append(raw, 0xff, 0xe1, 0, byte(len(segment)+2))
		raw = append(raw, segment...)
		raw = append(raw, encoded.Bytes()[2:]...)
		prepared, _, err := Prepare(raw)
		if err != nil {
			t.Fatal(err)
		}
		result, err := png.Decode(bytes.NewReader(prepared))
		if err != nil {
			t.Fatal(err)
		}
		wantW, wantH := 12, 8
		if orientation >= 5 {
			wantW, wantH = 8, 12
		}
		if result.Bounds().Dx() != wantW || result.Bounds().Dy() != wantH {
			t.Fatalf("orientation %d: bounds %v", orientation, result.Bounds())
		}
		if bytes.Contains(prepared, []byte("Exif")) {
			t.Fatal("metadata retained")
		}
	}
}

func TestOrientationMovesCorners(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	red := color.NRGBA{255, 0, 0, 255}
	source.SetNRGBA(0, 0, red)
	positions := []image.Point{{0, 0}, {2, 0}, {2, 1}, {0, 1}, {0, 0}, {1, 0}, {1, 2}, {0, 2}}
	for index, want := range positions {
		result := orientImage(source, index+1)
		if color.NRGBAModel.Convert(result.At(want.X, want.Y)) != red {
			t.Fatalf("orientation %d: corner mismatch", index+1)
		}
	}
}
