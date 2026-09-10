//go:build windows

package clipvault

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image/png"
	"reflect"
	"strings"
	"testing"
)

func TestReadClipboardImageFallsBackAcrossAdvertisedFormats(t *testing.T) {
	const fakePNGFormat = uint32(50000)
	available := map[uint32]bool{
		fakePNGFormat: true,
		cfDIBV5:       true,
		cfDIB:         true,
	}
	var reads []uint32
	access := clipboardImageAccess{
		pngFormatIDs: []uint32{fakePNGFormat},
		available:    func(format uint32) bool { return available[format] },
		readBytes: func(format uint32) ([]byte, error) {
			reads = append(reads, format)
			switch format {
			case fakePNGFormat:
				return []byte("not a png"), nil
			case cfDIBV5:
				return nil, &clipboardOperationError{
					stage:  clipboardReadStageGetData,
					format: "CF_DIBV5",
					err:    errClipboardDataUnavailable,
				}
			case cfDIB:
				return onePixelDIB(), nil
			default:
				return nil, errors.New("unexpected format")
			}
		},
		readBitmap: func() ([]byte, error) {
			t.Fatal("CF_BITMAP must not be read after CF_DIB succeeds")
			return nil, nil
		},
	}

	snapshot, ok, err := readClipboardImageWith(DefaultConfig(), access)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || snapshot == nil || len(snapshot.ImageBytes) == 0 {
		t.Fatalf("image was not captured: ok=%v snapshot=%+v", ok, snapshot)
	}
	if got := snapshot.Meta["sourceFormat"]; got != "CF_DIB" {
		t.Fatalf("source format = %v, want CF_DIB", got)
	}
	if want := []uint32{fakePNGFormat, cfDIBV5, cfDIB}; !reflect.DeepEqual(reads, want) {
		t.Fatalf("read order = %v, want %v", reads, want)
	}
}

func TestReadClipboardImageAcceptsFullyDecodableRegisteredPNG(t *testing.T) {
	const fakePNGFormat = uint32(50000)
	payload, err := dibToPNG(onePixelDIB())
	if err != nil {
		t.Fatal(err)
	}
	access := clipboardImageAccess{
		pngFormatIDs: []uint32{fakePNGFormat},
		available:    func(format uint32) bool { return format == fakePNGFormat },
		readBytes:    func(uint32) ([]byte, error) { return payload, nil },
		readBitmap: func() ([]byte, error) {
			t.Fatal("lower-priority bitmap reader ran after valid PNG")
			return nil, nil
		},
	}

	snapshot, found, err := readClipboardImageWith(DefaultConfig(), access)
	if err != nil || !found || snapshot == nil || !reflect.DeepEqual(snapshot.ImageBytes, payload) {
		t.Fatalf("valid registered PNG result: snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
	if snapshot.Meta["width"] != 1 || snapshot.Meta["height"] != 1 {
		t.Fatalf("registered PNG dimensions = %v x %v", snapshot.Meta["width"], snapshot.Meta["height"])
	}
	if snapshot.ByteLen != len(payload) || snapshot.CharLen != 0 {
		t.Fatalf("managed image lengths = %d bytes/%d chars, want %d/0", snapshot.ByteLen, snapshot.CharLen, len(payload))
	}
}

func TestReadClipboardImageStopsOnPreCopySizeLimit(t *testing.T) {
	const fakePNGFormat = uint32(50000)
	access := clipboardImageAccess{
		pngFormatIDs: []uint32{fakePNGFormat},
		available:    func(format uint32) bool { return format == fakePNGFormat },
		readBytes: func(uint32) ([]byte, error) {
			return nil, &clipboardOperationError{
				stage: clipboardReadStageGetData, format: "PNG", err: ErrImageLimitExceeded,
			}
		},
		readBitmap: func() ([]byte, error) {
			t.Fatal("size-limited PNG must stop image fallback")
			return nil, nil
		},
	}
	snapshot, ok, err := readClipboardImageWith(DefaultConfig(), access)
	if snapshot != nil || !ok || !errors.Is(err, ErrImageLimitExceeded) {
		t.Fatalf("pre-copy limit result: snapshot=%v ok=%v err=%v", snapshot, ok, err)
	}
}

func TestReadClipboardImageFallsBackAfterHigherPrioritySizeLimit(t *testing.T) {
	const oversizedPNG = uint32(50000)
	access := clipboardImageAccess{
		pngFormatIDs: []uint32{oversizedPNG},
		available: func(format uint32) bool {
			return format == oversizedPNG || format == cfDIB
		},
		readBytes: func(format uint32) ([]byte, error) {
			if format == oversizedPNG {
				return nil, ErrImageLimitExceeded
			}
			return onePixelDIB(), nil
		},
		readBitmap: func() ([]byte, error) { return nil, nil },
	}
	snapshot, found, err := readClipboardImageWith(DefaultConfig(), access)
	if err != nil || !found || snapshot == nil || snapshot.Meta["sourceFormat"] != "CF_DIB" {
		t.Fatalf("lower-priority DIB was hidden by PNG limit: snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestReadClipboardImageBitmapLimitOverridesRetryableCandidate(t *testing.T) {
	const delayedPNG = uint32(50000)
	access := clipboardImageAccess{
		pngFormatIDs: []uint32{delayedPNG},
		available:    func(format uint32) bool { return format == delayedPNG || format == cfBitmap },
		readBytes: func(uint32) ([]byte, error) {
			return nil, &clipboardOperationError{
				stage: clipboardReadStageGetData, format: "PNG", err: errClipboardDataUnavailable,
			}
		},
		readBitmap: func() ([]byte, error) { return nil, ErrImageLimitExceeded },
	}

	snapshot, found, err := readClipboardImageWith(DefaultConfig(), access)
	if snapshot != nil || !found || !errors.Is(err, ErrImageLimitExceeded) {
		t.Fatalf("bitmap limit was hidden by a retryable candidate: snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestReadClipboardImagePreservesDelayedFormatForRetry(t *testing.T) {
	const (
		delayedPNG = uint32(50000)
		brokenPNG  = uint32(50001)
	)
	wantErr := &clipboardOperationError{
		stage: clipboardReadStageGetData, format: "PNG", err: errClipboardDataUnavailable,
	}
	access := clipboardImageAccess{
		pngFormatIDs: []uint32{delayedPNG, brokenPNG},
		available:    func(format uint32) bool { return format == delayedPNG || format == brokenPNG },
		readBytes: func(format uint32) ([]byte, error) {
			if format == delayedPNG {
				return nil, wantErr
			}
			return []byte("not a PNG"), nil
		},
		readBitmap: func() ([]byte, error) {
			t.Fatal("CF_BITMAP was not advertised")
			return nil, nil
		},
	}

	snapshot, found, err := readClipboardImageWith(DefaultConfig(), access)
	if snapshot != nil || !found || err != wantErr {
		t.Fatalf("delayed image format was not preserved: snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestReadClipboardImageClassifiesAdvertisedEmptyPayloadsAsDecodeFailures(t *testing.T) {
	const fakePNGFormat = uint32(50000)
	tests := []struct {
		name   string
		access clipboardImageAccess
	}{
		{
			name: "registered PNG",
			access: clipboardImageAccess{
				pngFormatIDs: []uint32{fakePNGFormat},
				available:    func(format uint32) bool { return format == fakePNGFormat },
				readBytes:    func(uint32) ([]byte, error) { return nil, nil },
				readBitmap:   func() ([]byte, error) { return nil, nil },
			},
		},
		{
			name: "DIB",
			access: clipboardImageAccess{
				available:  func(format uint32) bool { return format == cfDIB },
				readBytes:  func(uint32) ([]byte, error) { return nil, nil },
				readBitmap: func() ([]byte, error) { return nil, nil },
			},
		},
		{
			name: "bitmap",
			access: clipboardImageAccess{
				available:  func(format uint32) bool { return format == cfBitmap },
				readBytes:  func(uint32) ([]byte, error) { return nil, nil },
				readBitmap: func() ([]byte, error) { return nil, nil },
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, found, err := readClipboardImageWith(DefaultConfig(), test.access)
			var operationErr *clipboardOperationError
			if snapshot != nil || !found || !errors.As(err, &operationErr) || operationErr.stage != clipboardReadStageDecode {
				t.Fatalf("empty advertised image result: snapshot=%#v found=%v err=%v", snapshot, found, err)
			}
		})
	}
}

func TestReadClipboardImageRejectsTruncatedPNGAfterValidHeader(t *testing.T) {
	const fakePNGFormat = uint32(50000)
	valid, err := dibToPNG(onePixelDIB())
	if err != nil {
		t.Fatal(err)
	}
	truncated := valid[:len(valid)-1]
	access := clipboardImageAccess{
		pngFormatIDs: []uint32{fakePNGFormat},
		available:    func(format uint32) bool { return format == fakePNGFormat },
		readBytes:    func(uint32) ([]byte, error) { return truncated, nil },
		readBitmap:   func() ([]byte, error) { return nil, nil },
	}

	snapshot, found, err := readClipboardImageWith(DefaultConfig(), access)
	var operationErr *clipboardOperationError
	if snapshot != nil || !found || !errors.As(err, &operationErr) || operationErr.stage != clipboardReadStageDecode {
		t.Fatalf("truncated PNG result: snapshot=%#v found=%v err=%v", snapshot, found, err)
	}
}

func TestValidateClipboardPNGRejectsPixelBudgetBeforeDecoding(t *testing.T) {
	payload, err := dibToPNG(onePixelDIB())
	if err != nil {
		t.Fatal(err)
	}
	binary.BigEndian.PutUint32(payload[16:20], 10_000)
	binary.BigEndian.PutUint32(payload[20:24], 5_000)
	binary.BigEndian.PutUint32(payload[29:33], crc32.ChecksumIEEE(payload[12:29]))
	if err := validateClipboardPNG(payload); !errors.Is(err, ErrImageLimitExceeded) {
		t.Fatalf("oversized PNG error = %v, want ErrImageLimitExceeded", err)
	}
}

func TestDIBRejectsPixelBudgetBeforeAllocatingImage(t *testing.T) {
	dib := make([]byte, 40)
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], 10_000)
	binary.LittleEndian.PutUint32(dib[8:12], 5_000)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	if _, err := dibToPNG(dib); !errors.Is(err, ErrImageLimitExceeded) {
		t.Fatalf("oversized DIB error = %v, want ErrImageLimitExceeded", err)
	}
}

func TestDIBBitfieldsUsesOnlyDeclaredAlphaMask(t *testing.T) {
	tests := []struct {
		name       string
		headerSize int
		alphaMask  uint32
		pixelAlpha byte
		wantAlpha  uint32
	}{
		{name: "three masks reserve fourth byte", headerSize: 40, pixelAlpha: 0, wantAlpha: 0xffff},
		{name: "v2 header embeds color masks", headerSize: 52, pixelAlpha: 0, wantAlpha: 0xffff},
		{name: "explicit alpha mask", headerSize: 56, alphaMask: 0xFF000000, pixelAlpha: 0x7f, wantAlpha: 0x7f7f},
		{name: "explicit zero mask is opaque", headerSize: 56, pixelAlpha: 0, wantAlpha: 0xffff},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := dibToPNG(onePixelBitfieldsDIB(test.headerSize, test.alphaMask, test.pixelAlpha))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := png.Decode(bytes.NewReader(encoded))
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, alpha := decoded.At(0, 0).RGBA()
			if alpha != test.wantAlpha {
				t.Fatalf("decoded alpha = %#x, want %#x", alpha, test.wantAlpha)
			}
		})
	}
}

func TestDIBSkipsDeclaredTrueColorTable(t *testing.T) {
	const headerSize = 108
	dib := make([]byte, headerSize+4+4)
	binary.LittleEndian.PutUint32(dib[0:4], headerSize)
	binary.LittleEndian.PutUint32(dib[4:8], 1)
	binary.LittleEndian.PutUint32(dib[8:12], 1)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[32:36], 1)
	copy(dib[headerSize:headerSize+4], []byte{0xaa, 0xbb, 0xcc, 0xdd})
	copy(dib[headerSize+4:], []byte{0x33, 0x22, 0x11, 0x00})

	encoded, err := dibToPNG(dib)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	red, green, blue, alpha := decoded.At(0, 0).RGBA()
	if red != 0x1111 || green != 0x2222 || blue != 0x3333 || alpha != 0xffff {
		t.Fatalf("decoded RGBA = %#x/%#x/%#x/%#x", red, green, blue, alpha)
	}
}

func TestDIBRejectsTruncatedDeclaredColorTable(t *testing.T) {
	dib := onePixelDIB()
	binary.LittleEndian.PutUint32(dib[32:36], 2)
	if _, err := dibToPNG(dib); err == nil || !strings.Contains(err.Error(), "color table truncated") {
		t.Fatalf("truncated color table error = %v", err)
	}
}

func TestValidateBitmapScanlineCountRequiresCompleteCopy(t *testing.T) {
	for _, rows := range []uintptr{0, 2} {
		if err := validateBitmapScanlineCount(rows, 3); err == nil {
			t.Fatalf("partial bitmap copy of %d rows was accepted", rows)
		}
	}
	if err := validateBitmapScanlineCount(3, 3); err != nil {
		t.Fatalf("complete bitmap copy was rejected: %v", err)
	}
}

func onePixelBitfieldsDIB(headerSize int, alphaMask uint32, alpha byte) []byte {
	pixelOffset := headerSize
	if headerSize == 40 {
		pixelOffset += 12
	}
	dib := make([]byte, pixelOffset+4)
	binary.LittleEndian.PutUint32(dib[0:4], uint32(headerSize))
	binary.LittleEndian.PutUint32(dib[4:8], 1)
	binary.LittleEndian.PutUint32(dib[8:12], 1)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	binary.LittleEndian.PutUint32(dib[16:20], 3)
	binary.LittleEndian.PutUint32(dib[40:44], 0x00FF0000)
	binary.LittleEndian.PutUint32(dib[44:48], 0x0000FF00)
	binary.LittleEndian.PutUint32(dib[48:52], 0x000000FF)
	if headerSize >= 56 {
		binary.LittleEndian.PutUint32(dib[52:56], alphaMask)
	}
	dib[pixelOffset], dib[pixelOffset+1], dib[pixelOffset+2], dib[pixelOffset+3] = 0x33, 0x22, 0x11, alpha
	return dib
}

func onePixelDIB() []byte {
	dib := make([]byte, 44)
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], 1)
	binary.LittleEndian.PutUint32(dib[8:12], 1)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 24)
	dib[40], dib[41], dib[42] = 0x33, 0x22, 0x11
	return dib
}
func TestClipboardAcceptsLongScreenshots(t *testing.T) {
	for _, size := range [][2]int{{32, 20000}, {20000, 32}} {
		width, height := size[0], size[1]
		dib := make([]byte, 40+width*height*4)
		binary.LittleEndian.PutUint32(dib[0:4], 40)
		binary.LittleEndian.PutUint32(dib[4:8], uint32(width))
		binary.LittleEndian.PutUint32(dib[8:12], uint32(height))
		binary.LittleEndian.PutUint16(dib[12:14], 1)
		binary.LittleEndian.PutUint16(dib[14:16], 32)
		data, err := dibToPNG(dib)
		if err != nil {
			t.Fatalf("long DIB %v rejected: %v", size, err)
		}
		if err := validateClipboardPNG(data); err != nil {
			t.Fatalf("long PNG %v rejected: %v", size, err)
		}
		if w, h := pngDimensions(data); w != width || h != height {
			t.Fatalf("dimensions changed to %dx%d", w, h)
		}
	}
}
