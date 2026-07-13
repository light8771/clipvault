//go:build windows

package clipvault

import (
	"bytes"
	"image"
	_ "image/png"
	"syscall"
	"unsafe"
)

// IconPNG 由 cmd/clipvault/main.go 通过 go:embed 注入，托盘图标和 exe 图标共用同一份 winres/icon.png。
var IconPNG []byte

var gdi32 = syscall.NewLazyDLL("gdi32.dll")

var (
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procGdiSelectObject    = gdi32.NewProc("SelectObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procCreateBitmap       = gdi32.NewProc("CreateBitmap")
	procCreateIconIndirect = user32.NewProc("CreateIconIndirect")
	procDestroyIcon        = user32.NewProc("DestroyIcon")
)

type dibBitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type dibBitmapInfo struct {
	Header dibBitmapInfoHeader
}

type iconInfoStruct struct {
	IsIcon   uint32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

// createClipboardIcon 从嵌入的 icon.png 创建 32×32 托盘图标，失败返回 0。
func createClipboardIcon() uintptr {
	const sz = 32

	// 解码嵌入的 PNG
	img, _, err := image.Decode(bytes.NewReader(IconPNG))
	if err != nil {
		return 0
	}

	// 缩放到 32×32（最近邻）
	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	bi := dibBitmapInfo{
		Header: dibBitmapInfoHeader{
			Size:     40,
			Width:    sz,
			Height:   -sz, // 负数 = 从上到下的 DIB
			Planes:   1,
			BitCount: 32,
		},
	}

	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return 0
	}
	defer procDeleteDC.Call(dc)

	var pixelsPtr uintptr
	hbmColor, _, _ := procCreateDIBSection.Call(
		dc,
		uintptr(unsafe.Pointer(&bi)),
		0, // DIB_RGB_COLORS
		uintptr(unsafe.Pointer(&pixelsPtr)),
		0, 0,
	)
	if hbmColor == 0 {
		return 0
	}
	defer procDeleteObject.Call(hbmColor)
	procGdiSelectObject.Call(dc, hbmColor)

	// 像素格式：BGRA（小端），每像素 4 字节，从上到下
	pixels := (*[sz * sz]uint32)(unsafe.Pointer(pixelsPtr))

	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			// 最近邻采样
			srcX := x * srcW / sz
			srcY := y * srcH / sz
			r, g, b, a := img.At(bounds.Min.X+srcX, bounds.Min.Y+srcY).RGBA()
			// RGBA() 返回 0-65535，转为 0-255
			cr := uint8(r >> 8)
			cg := uint8(g >> 8)
			cb := uint8(b >> 8)
			ca := uint8(a >> 8)
			// Windows DIB 使用预乘 alpha 的 BGRA 格式
			if ca > 0 && ca < 255 {
				cr = premultiply(cr, ca)
				cg = premultiply(cg, ca)
				cb = premultiply(cb, ca)
			}
			pixels[y*sz+x] = uint32(ca)<<24 | uint32(cr)<<16 | uint32(cg)<<8 | uint32(cb)
		}
	}

	// 创建 1bpp AND 蒙版
	maskRowStride := ((sz + 31) / 32) * 4
	maskBits := make([]byte, maskRowStride*sz)

	// 根据 alpha 设置蒙版位（alpha=0 → 蒙版位=1 表示透明）
	for y := 0; y < sz; y++ {
		srcX := 0
		srcY := y * srcH / sz
		for x := 0; x < sz; x++ {
			srcX = x * srcW / sz
			_, _, _, a := img.At(bounds.Min.X+srcX, bounds.Min.Y+srcY).RGBA()
			if a == 0 {
				byteIdx := y*maskRowStride + x/8
				bitIdx := uint(7 - x%8)
				maskBits[byteIdx] |= 1 << bitIdx
			}
		}
	}

	hbmMask, _, _ := procCreateBitmap.Call(
		sz, sz, 1, 1,
		uintptr(unsafe.Pointer(&maskBits[0])),
	)
	if hbmMask == 0 {
		return 0
	}
	defer procDeleteObject.Call(hbmMask)

	ii := iconInfoStruct{
		IsIcon:   1,
		HbmMask:  hbmMask,
		HbmColor: hbmColor,
	}
	hIcon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	return hIcon
}

// premultiply 将颜色分量预乘 alpha
func premultiply(c, a uint8) uint8 {
	return uint8(uint16(c) * uint16(a) / 255)
}
