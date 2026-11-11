package ocr

import (
	"encoding/binary"
	"errors"
	"image"
)

// jpegOrientation 只读取 JPEG APP1 的 TIFF 方向标签，不信任文件中的偏移。
func jpegOrientation(raw []byte) (int, error) {
	for offset := 2; offset+4 <= len(raw); {
		if raw[offset] != 0xff {
			return 1, errors.New("ocr.error.jpeg_marker")
		}
		marker := raw[offset+1]
		if marker == 0xda || marker == 0xd9 {
			return 1, nil
		}
		size := int(binary.BigEndian.Uint16(raw[offset+2 : offset+4]))
		if size < 2 || size > len(raw)-offset-2 {
			return 1, errors.New("ocr.error.jpeg_length")
		}
		segment := raw[offset+4 : offset+2+size]
		offset += 2 + size
		if marker != 0xe1 || len(segment) < 6 || string(segment[:6]) != "Exif\x00\x00" {
			continue
		}
		data := segment[6:]
		if len(data) < 8 {
			return 1, errors.New("ocr.error.orientation_corrupt")
		}
		var order binary.ByteOrder
		switch string(data[:2]) {
		case "II":
			order = binary.LittleEndian
		case "MM":
			order = binary.BigEndian
		default:
			return 1, errors.New("ocr.error.orientation_byte_order")
		}
		if order.Uint16(data[2:4]) != 42 {
			return 1, errors.New("ocr.error.orientation_metadata")
		}
		position := uint64(order.Uint32(data[4:8]))
		if position+2 > uint64(len(data)) {
			return 1, errors.New("ocr.error.orientation_offset")
		}
		count := int(order.Uint16(data[position : position+2]))
		position += 2
		for i := 0; i < count; i++ {
			if position+12 > uint64(len(data)) {
				return 1, errors.New("ocr.error.orientation_tag")
			}
			entry := data[position : position+12]
			position += 12
			if order.Uint16(entry[:2]) != 0x112 {
				continue
			}
			value := int(order.Uint16(entry[8:10]))
			if order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 || value < 1 || value > 8 {
				return 1, errors.New("ocr.error.orientation_value")
			}
			return value, nil
		}
		return 1, nil
	}
	return 1, nil
}

func orientImage(src image.Image, orientation int) image.Image {
	if orientation == 1 {
		return src
	}
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	ow, oh := w, h
	if orientation >= 5 {
		ow, oh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			switch orientation {
			case 2:
				dx = w - 1 - x
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dy = h - 1 - y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return dst
}
