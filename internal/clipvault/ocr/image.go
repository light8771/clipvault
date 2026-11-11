package ocr

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
)

const ImageVersion = "png-1"
const MaxImageBytes = 20 << 20
const MaxPixels = 16_000_000

// Prepare 验证后重新编码，移除元数据；不缩小文字，不修改保存的原图。
// 第一版拒绝超限图片，长图分段在后续版本实现。
func Prepare(raw []byte) ([]byte, string, error) {
	if len(raw) == 0 || len(raw) > MaxImageBytes {
		return nil, "", errors.New("ocr.error.input_size")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxPixels/cfg.Height {
		return nil, "", errors.New("ocr.error.dimensions")
	}
	orientation := 1
	if format == "jpeg" {
		orientation, err = jpegOrientation(raw)
		if err != nil {
			return nil, "", err
		}
	}
	if format == "gif" {
		return nil, "", errors.New("ocr.error.gif")
	}
	im, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", errors.New("ocr.error.decode")
	}
	var out bytes.Buffer
	if err := png.Encode(&out, orientImage(im, orientation)); err != nil {
		return nil, "", errors.New("ocr.error.encode")
	}
	if out.Len() > MaxImageBytes {
		return nil, "", errors.New("ocr.error.output_size")
	}
	hash := sha256.Sum256(raw)
	return out.Bytes(), hex.EncodeToString(hash[:]), nil
}
