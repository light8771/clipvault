package clipvault

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"testing"
)

func TestDecodedImageDimensionsUsePixelBudget(t *testing.T) {
	for _, size := range [][2]int{{32, 20000}, {20000, 32}, {8000, 5000}} {
		if err := validateDecodedImageDimensions(size[0], size[1]); err != nil {
			t.Fatalf("valid dimensions %v rejected: %v", size, err)
		}
	}
	for _, size := range [][2]int{{8001, 5000}, {int(^uint(0) >> 1), int(^uint(0) >> 1)}} {
		if err := validateDecodedImageDimensions(size[0], size[1]); !errors.Is(err, ErrImageLimitExceeded) {
			t.Fatalf("oversized dimensions %v: %v", size, err)
		}
	}
	for _, size := range [][2]int{{0, 20}, {20, -1}} {
		if err := validateDecodedImageDimensions(size[0], size[1]); err == nil {
			t.Fatalf("invalid dimensions %v accepted", size)
		}
	}
}

func TestManagedImageAcceptsLongScreenshots(t *testing.T) {
	for _, size := range [][2]int{{32, 20000}, {20000, 32}} {
		var data bytes.Buffer
		if err := png.Encode(&data, image.NewGray(image.Rect(0, 0, size[0], size[1]))); err != nil {
			t.Fatal(err)
		}
		if err := validateManagedImagePNG(data.Bytes()); err != nil {
			t.Fatalf("long image %v rejected: %v", size, err)
		}
	}
}
