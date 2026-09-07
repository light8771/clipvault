//go:build windows

package clipvault

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"sync"
	"syscall"
	"unsafe"

	"clipboard_ext/internal/clipvault/domain"
)

// 剪贴板图片采集：优先注册格式 "PNG"（浏览器 Copy Image），其次
// CF_DIBV5 / CF_DIB（Win+Shift+S、微信/QQ 截图），最后 CF_BITMAP。
// 所有来源统一转码为 PNG 字节，由 Recorder 写入托管目录。

var (
	procGetObjectW = gdi32.NewProc("GetObjectW")
	procGetDIBits  = gdi32.NewProc("GetDIBits")
	procGetDC      = user32.NewProc("GetDC")
	procReleaseDC  = user32.NewProc("ReleaseDC")
)

var (
	pngFormatOnce     sync.Once
	pngFormatID       uint32
	mimePNGFormatOnce sync.Once
	mimePNGFormatID   uint32
)

// clipboardPNGFormat 返回注册格式 "PNG" 的 id（进程内缓存）。
func clipboardPNGFormat() uint32 {
	pngFormatOnce.Do(func() {
		pngFormatID = registerClipboardFormat("PNG")
	})
	return pngFormatID
}

func clipboardMIMEPNGFormat() uint32 {
	mimePNGFormatOnce.Do(func() {
		mimePNGFormatID = registerClipboardFormat("image/png")
	})
	return mimePNGFormatID
}

var errImageTooLarge = ErrImageLimitExceeded

// 40 million pixels covers an 8K frame while bounding each RGBA working
// buffer to about 153 MiB. DIB input is uncompressed, so its byte cap can be
// enforced before copying the clipboard handle.
const (
	maxClipboardImagePixels = maxDecodedImagePixels
	maxClipboardDIBBytes    = maxClipboardImagePixels*4 + 1<<20
)

type clipboardImageAccess struct {
	pngFormatIDs []uint32
	available    func(uint32) bool
	readBytes    func(uint32) ([]byte, error)
	readBitmap   func() ([]byte, error)
}

func configuredImageMaxBytes(cfg Config) int {
	if cfg.Image.MaxBytes > 0 {
		return cfg.Image.MaxBytes
	}
	return defaultImageMaxBytes
}

// readClipboardImageWith treats advertised clipboard formats as candidates,
// not guarantees. Delayed-rendering producers commonly advertise several image
// formats while one of their GetClipboardData calls still returns no handle.
// A broken higher-priority candidate must therefore never prevent a lower-
// priority representation of the same image from being captured.
func readClipboardImageWith(cfg Config, access clipboardImageAccess) (*ClipboardSnapshot, bool, error) {
	maxBytes := configuredImageMaxBytes(cfg)
	var failures clipboardCandidateErrors
	for _, pngID := range access.pngFormatIDs {
		if pngID == 0 || !access.available(pngID) {
			continue
		}
		raw, err := access.readBytes(pngID)
		if err != nil {
			failures.add(err)
		} else if len(raw) == 0 {
			failures.add(&clipboardOperationError{
				stage: clipboardReadStageDecode, format: clipboardFormatName(pngID),
				err: errors.New("registered PNG format payload is empty"),
			})
		} else {
			if len(raw) > maxBytes {
				failures.add(errImageTooLarge)
				continue
			}
			if err := validateClipboardPNG(raw); err != nil {
				if errors.Is(err, ErrImageLimitExceeded) {
					failures.add(errImageTooLarge)
				} else {
					failures.add(&clipboardOperationError{stage: clipboardReadStageDecode, format: clipboardFormatName(pngID), err: err})
				}
			} else {
				return newImageSnapshot(raw, clipboardFormatName(pngID)), true, nil
			}
		}
	}
	for _, format := range []uint32{cfDIBV5, cfDIB} {
		if !access.available(format) {
			continue
		}
		raw, err := access.readBytes(format)
		if err != nil {
			failures.add(err)
			continue
		}
		if len(raw) == 0 {
			failures.add(&clipboardOperationError{
				stage: clipboardReadStageDecode, format: clipboardFormatName(format),
				err: errors.New("DIB payload is empty"),
			})
			continue
		}
		pngBytes, err := dibToPNG(raw)
		if err != nil {
			if errors.Is(err, ErrImageLimitExceeded) {
				failures.add(errImageTooLarge)
				continue
			}
			// DIB 解析失败时继续尝试下一个图片格式；全部失败后由
			// 候选错误策略返回结构化失败，不能把损坏数据误记为 unsupported。
			failures.add(&clipboardOperationError{stage: clipboardReadStageDecode, format: clipboardFormatName(format), err: err})
			continue
		}
		if len(pngBytes) > maxBytes {
			failures.add(errImageTooLarge)
			continue
		}
		return newImageSnapshot(pngBytes, clipboardFormatName(format)), true, nil
	}
	if access.available(cfBitmap) {
		pngBytes, err := access.readBitmap()
		if err != nil {
			failures.add(err)
		} else if len(pngBytes) == 0 {
			failures.add(&clipboardOperationError{
				stage: clipboardReadStageDecode, format: "CF_BITMAP",
				err: errors.New("bitmap conversion returned an empty PNG"),
			})
		} else {
			if len(pngBytes) > maxBytes {
				failures.add(errImageTooLarge)
			} else {
				return newImageSnapshot(pngBytes, "CF_BITMAP"), true, nil
			}
		}
	}
	if err := failures.err(); err != nil {
		return nil, true, err
	}
	return nil, false, nil
}

func newImageSnapshot(pngBytes []byte, sourceFormat string) *ClipboardSnapshot {
	width, height := pngDimensions(pngBytes)
	return &ClipboardSnapshot{
		Kind:       domain.KindImage,
		Format:     domain.FormatImageFile,
		ByteLen:    len(pngBytes),
		CharLen:    0,
		ImageBytes: pngBytes,
		SHA256:     hashBytes(pngBytes),
		Meta: map[string]any{
			"sourceFormat": sourceFormat,
			"width":        width,
			"height":       height,
			"imageBytes":   len(pngBytes),
		},
	}
}

func isPNG(data []byte) bool {
	magic := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	return len(data) >= 8 && bytes.Equal(data[:8], magic)
}

func validateClipboardPNG(data []byte) error {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid PNG header: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 16384 || config.Height > 16384 {
		return fmt.Errorf("unsupported PNG dimensions %dx%d", config.Width, config.Height)
	}
	if int64(config.Width)*int64(config.Height) > maxClipboardImagePixels {
		return ErrImageLimitExceeded
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("invalid PNG payload: %w", err)
	}
	return nil
}

// pngDimensions 从 PNG 字节中读取 IHDR 尺寸（不完整解码）。
func pngDimensions(data []byte) (int, int) {
	if !isPNG(data) || len(data) < 33 {
		return 0, 0
	}
	width := binary.BigEndian.Uint32(data[16:20])
	height := binary.BigEndian.Uint32(data[20:24])
	return int(width), int(height)
}

// dibToPNG 把 CF_DIB / CF_DIBV5（BITMAPINFOHEADER 家族 + 像素，无文件头）
// 转码为 PNG。支持 24/32bpp 的 BI_RGB 与标准掩码的 BI_BITFIELDS。
func dibToPNG(dib []byte) ([]byte, error) {
	if len(dib) < 40 {
		return nil, errors.New("DIB too small")
	}
	le := binary.LittleEndian
	headerSize := int(le.Uint32(dib[0:4]))
	if headerSize < 40 || len(dib) < headerSize {
		return nil, fmt.Errorf("unsupported DIB header size %d", headerSize)
	}
	width := int(int32(le.Uint32(dib[4:8])))
	height := int(int32(le.Uint32(dib[8:12])))
	bitCount := int(le.Uint16(dib[14:16]))
	compression := le.Uint32(dib[16:20])
	topDown := height < 0
	if topDown {
		height = -height
	}
	if width <= 0 || height <= 0 || width > 16384 || height > 16384 {
		return nil, fmt.Errorf("unsupported DIB dimensions %dx%d", width, height)
	}
	if int64(width)*int64(height) > maxClipboardImagePixels {
		return nil, ErrImageLimitExceeded
	}
	if bitCount != 24 && bitCount != 32 {
		return nil, fmt.Errorf("unsupported DIB bit count %d", bitCount)
	}

	offset := headerSize
	honorAlpha := false
	switch compression {
	case 0: // BI_RGB：无 alpha 语义，32bpp 的第四字节按不透明处理。
	case 3: // BI_BITFIELDS：仅接受标准 BGR(A) 掩码布局。
		var masks [3]uint32
		switch {
		case headerSize >= 52:
			// BITMAPV2INFOHEADER and later revisions embed the RGB masks
			// in the header itself.
			masks[0] = le.Uint32(dib[40:44])
			masks[1] = le.Uint32(dib[44:48])
			masks[2] = le.Uint32(dib[48:52])
		case headerSize == 40:
			// BITMAPINFOHEADER stores three masks immediately after its
			// 40-byte header.
			if len(dib) < offset+12 {
				return nil, errors.New("DIB bitfields masks missing")
			}
			masks[0] = le.Uint32(dib[offset : offset+4])
			masks[1] = le.Uint32(dib[offset+4 : offset+8])
			masks[2] = le.Uint32(dib[offset+8 : offset+12])
			offset += 12
		default:
			return nil, fmt.Errorf("unsupported DIB bitfields header size %d", headerSize)
		}
		if masks != [3]uint32{0x00FF0000, 0x0000FF00, 0x000000FF} || bitCount != 32 {
			return nil, fmt.Errorf("unsupported DIB bitfields masks %v/%d", masks, bitCount)
		}
		// BITMAPINFOHEADER carries only three external color masks, so its
		// fourth byte is reserved rather than alpha. V3/V4/V5 headers declare
		// alpha explicitly at offset 52; a zero mask likewise means opaque.
		if headerSize >= 56 {
			alphaMask := le.Uint32(dib[52:56])
			switch alphaMask {
			case 0:
			case 0xFF000000:
				honorAlpha = true
			default:
				return nil, fmt.Errorf("unsupported DIB alpha mask %#x", alphaMask)
			}
		}
	default:
		return nil, fmt.Errorf("unsupported DIB compression %d", compression)
	}
	// True-color DIBs may still carry an optional RGBQUAD table used to
	// optimize palette-based devices. biClrUsed/bV4ClrUsed/bV5ClrUsed gives
	// its exact entry count; the packed pixel array follows that table.
	colorEntries := uint64(le.Uint32(dib[32:36]))
	remaining := len(dib) - offset
	if colorEntries > uint64(remaining/4) {
		return nil, errors.New("DIB color table truncated")
	}
	offset += int(colorEntries * 4)

	stride := ((width*bitCount + 31) / 32) * 4
	if len(dib) < offset+stride*height {
		return nil, errors.New("DIB pixel data truncated")
	}
	pixels := dib[offset : offset+stride*height]

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	bytesPerPixel := bitCount / 8
	for y := 0; y < height; y++ {
		srcY := y
		if !topDown {
			srcY = height - 1 - y
		}
		row := pixels[srcY*stride:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < width; x++ {
			px := row[x*bytesPerPixel:]
			dst[x*4+0] = px[2] // R
			dst[x*4+1] = px[1] // G
			dst[x*4+2] = px[0] // B
			if bitCount == 32 && honorAlpha {
				dst[x*4+3] = px[3]
			} else {
				dst[x*4+3] = 0xFF
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// bitmapHeader 对应 Win32 BITMAP 结构（GetObjectW 输出）。
type bitmapHeader struct {
	Type       int32
	Width      int32
	Height     int32
	WidthBytes int32
	Planes     uint16
	BitsPixel  uint16
	Bits       uintptr
}

type clipboardBitmapCapture struct {
	width  int
	height int
	pixels []byte
}

// captureClipboardBitmap copies CF_BITMAP pixels while the clipboard is open.
// Color conversion and PNG encoding are deliberately deferred until after
// CloseClipboard.
func captureClipboardBitmap() (*clipboardBitmapCapture, error) {
	handle, _, err := procGetClipboardData.Call(cfBitmap)
	if handle == 0 {
		if err == syscall.Errno(0) {
			err = errClipboardDataUnavailable
		}
		return nil, &clipboardOperationError{stage: clipboardReadStageGetData, format: "CF_BITMAP", err: err}
	}
	var bm bitmapHeader
	ok, _, _ := procGetObjectW.Call(handle, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
	if ok == 0 || bm.Width <= 0 || bm.Height <= 0 {
		return nil, errors.New("GetObjectW(CF_BITMAP) failed")
	}
	width, height := int(bm.Width), int(bm.Height)
	if width > 16384 || height > 16384 {
		return nil, fmt.Errorf("bitmap too large %dx%d", width, height)
	}
	if int64(width)*int64(height) > maxClipboardImagePixels {
		return nil, ErrImageLimitExceeded
	}

	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return nil, errors.New("GetDC failed")
	}
	defer procReleaseDC.Call(0, hdc)

	// BITMAPINFOHEADER（40 字节）：32bpp、BI_RGB、负高度 = top-down。
	var bmi [40]byte
	le := binary.LittleEndian
	le.PutUint32(bmi[0:4], 40)
	le.PutUint32(bmi[4:8], uint32(int32(width)))
	le.PutUint32(bmi[8:12], uint32(int32(-height)))
	le.PutUint16(bmi[12:14], 1)
	le.PutUint16(bmi[14:16], 32)

	pixels := make([]byte, width*height*4)
	rows, _, _ := procGetDIBits.Call(
		hdc, handle, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bmi[0])),
		0, // DIB_RGB_COLORS
	)
	if err := validateBitmapScanlineCount(rows, height); err != nil {
		return nil, err
	}

	return &clipboardBitmapCapture{width: width, height: height, pixels: pixels}, nil
}

func validateBitmapScanlineCount(rows uintptr, height int) error {
	if height <= 0 || rows != uintptr(height) {
		return fmt.Errorf("GetDIBits copied %d of %d scanlines", rows, height)
	}
	return nil
}

func (capture *clipboardBitmapCapture) toPNG() ([]byte, error) {
	if capture == nil || capture.width <= 0 || capture.height <= 0 || len(capture.pixels) != capture.width*capture.height*4 {
		return nil, errors.New("invalid captured bitmap")
	}
	img := image.NewNRGBA(image.Rect(0, 0, capture.width, capture.height))
	for y := 0; y < capture.height; y++ {
		row := capture.pixels[y*capture.width*4:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < capture.width; x++ {
			dst[x*4+0] = row[x*4+2]
			dst[x*4+1] = row[x*4+1]
			dst[x*4+2] = row[x*4+0]
			dst[x*4+3] = 0xFF
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
