//go:build windows

package clipvault

import (
	"encoding/binary"
	"errors"
	"reflect"
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
