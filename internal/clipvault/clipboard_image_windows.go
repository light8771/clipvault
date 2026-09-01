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

type clipboardImageAccess struct {
	pngFormatIDs []uint32
	available    func(uint32) bool
	readBytes    func(uint32) ([]byte, error)
	readBitmap   func() ([]byte, error)
}

// readClipboardImage 尝试把剪贴板中的图片读取为 PNG 字节。
// 剪贴板必须已由调用方打开。ok=false 表示剪贴板中没有可识别的图片格式。
func readClipboardImage(cfg Config) (*ClipboardSnapshot, bool, error) {
	return readClipboardImageWith(cfg, clipboardImageAccess{
		pngFormatIDs: []uint32{clipboardPNGFormat(), clipboardMIMEPNGFormat()},
		available:    isClipboardFormatAvailable,
		readBytes:    readClipboardBytes,
		readBitmap:   bitmapHandleToPNG,
	})
}

// readClipboardImageWith treats advertised clipboard formats as candidates,
// not guarantees. Delayed-rendering producers commonly advertise several image
// formats while one of their GetClipboardData calls still returns no handle.
// A broken higher-priority candidate must therefore never prevent a lower-
// priority representation of the same image from being captured.
func readClipboardImageWith(cfg Config, access clipboardImageAccess) (*ClipboardSnapshot, bool, error) {
	maxBytes := cfg.Image.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultImageMaxBytes
	}
	var lastErr error
	for _, pngID := range access.pngFormatIDs {
		if pngID == 0 || !access.available(pngID) {
			continue
		}
		raw, err := access.readBytes(pngID)
		if err != nil {
			lastErr = err
		} else if len(raw) > 0 {
			if len(raw) > maxBytes {
				return nil, true, errImageTooLarge
			}
			if !isPNG(raw) {
				lastErr = &clipboardOperationError{stage: clipboardReadStageDecode, format: clipboardFormatName(pngID), err: errors.New("registered PNG format payload is not a PNG")}
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
			lastErr = err
			continue
		}
		if len(raw) == 0 {
			continue
		}
		pngBytes, err := dibToPNG(raw)
		if err != nil {
			// DIB 解析失败时继续尝试下一个图片格式，全部失败后返回
			// 最后一个结构化错误，不能把损坏数据误记为 unsupported。
			lastErr = &clipboardOperationError{stage: clipboardReadStageDecode, format: clipboardFormatName(format), err: err}
			continue
		}
		if len(pngBytes) > maxBytes {
			return nil, true, errImageTooLarge
		}
		return newImageSnapshot(pngBytes, clipboardFormatName(format)), true, nil
	}
	if access.available(cfBitmap) {
		pngBytes, err := access.readBitmap()
		if err != nil {
			lastErr = err
		} else if len(pngBytes) > 0 {
			if len(pngBytes) > maxBytes {
				return nil, true, errImageTooLarge
			}
			return newImageSnapshot(pngBytes, "CF_BITMAP"), true, nil
		}
	}
	if lastErr != nil {
		return nil, true, lastErr
	}
	return nil, false, nil
}

func newImageSnapshot(pngBytes []byte, sourceFormat string) *ClipboardSnapshot {
	width, height := pngDimensions(pngBytes)
	return &ClipboardSnapshot{
		Kind:       domain.KindImage,
		Format:     domain.FormatImageFile,
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
	if bitCount != 24 && bitCount != 32 {
		return nil, fmt.Errorf("unsupported DIB bit count %d", bitCount)
	}

	offset := headerSize
	honorAlpha := false
	switch compression {
	case 0: // BI_RGB：无 alpha 语义，32bpp 的第四字节按不透明处理。
	case 3: // BI_BITFIELDS：仅接受标准 BGR(A) 掩码布局。
		var masks [3]uint32
		if headerSize >= 56 {
			masks[0] = le.Uint32(dib[40:44])
			masks[1] = le.Uint32(dib[44:48])
			masks[2] = le.Uint32(dib[48:52])
		} else {
			if len(dib) < offset+12 {
				return nil, errors.New("DIB bitfields masks missing")
			}
			masks[0] = le.Uint32(dib[offset : offset+4])
			masks[1] = le.Uint32(dib[offset+4 : offset+8])
			masks[2] = le.Uint32(dib[offset+8 : offset+12])
			offset += 12
		}
		if masks != [3]uint32{0x00FF0000, 0x0000FF00, 0x000000FF} || bitCount != 32 {
			return nil, fmt.Errorf("unsupported DIB bitfields masks %v/%d", masks, bitCount)
		}
		honorAlpha = true
	default:
		return nil, fmt.Errorf("unsupported DIB compression %d", compression)
	}

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

// bitmapHandleToPNG 读取 CF_BITMAP（HBITMAP）并转码为 PNG（32bpp top-down DIB）。
func bitmapHandleToPNG() ([]byte, error) {
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
	if rows == 0 {
		return nil, errors.New("GetDIBits failed")
	}

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := pixels[y*width*4:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < width; x++ {
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
