package clipvault

import (
	"encoding/binary"
	"io"
)

// Identify the container, not its codecs. Playback support belongs to the browser.
// Read only metadata and seek over payloads, regardless of video size.
func detectVideoContentType(r io.ReadSeeker) string {
	end, err := r.Seek(0, io.SeekEnd)
	if err != nil || end < 8 {
		return ""
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return ""
	}
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return ""
	}
	if binary.BigEndian.Uint32(header[:4]) == 0x1a45dfa3 {
		if _, err := r.Seek(4, io.SeekStart); err == nil && isWebMHeader(r, end) {
			return "video/webm"
		}
		return ""
	}
	for offset := int64(0); offset <= end-8; {
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			return ""
		}
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return ""
		}
		size := int64(binary.BigEndian.Uint32(header[:4]))
		headerSize := int64(8)
		if size == 1 {
			var extended [8]byte
			if _, err := io.ReadFull(r, extended[:]); err != nil {
				return ""
			}
			value := binary.BigEndian.Uint64(extended[:])
			if value > uint64(end-offset) {
				return ""
			}
			size, headerSize = int64(value), 16
		} else if size == 0 {
			size = end - offset
		}
		if size < headerSize || size > end-offset {
			return ""
		}
		if string(header[4:]) == "ftyp" {
			payload := size - headerSize
			if payload < 8 || payload%4 != 0 {
				return ""
			}
			var brand [4]byte
			for i := int64(0); i < payload; i += 4 {
				if _, err := io.ReadFull(r, brand[:]); err != nil {
					return ""
				}
				if i == 4 { // minor version
					continue
				}
				switch string(brand[:]) {
				case "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "iso7", "iso8", "iso9", "mp41", "mp42", "avc1", "dash", "M4V ":
					return "video/mp4"
				}
			}
			return ""
		}
		offset += size
	}
	return ""
}

func isWebMHeader(r io.ReadSeeker, fileEnd int64) bool {
	size, ok := readEBMLValue(r, false)
	start, err := r.Seek(0, io.SeekCurrent)
	if !ok || err != nil || size > uint64(fileEnd-start) {
		return false
	}
	end := start + int64(size)
	for start < end {
		id, idOK := readEBMLValue(r, true)
		length, sizeOK := readEBMLValue(r, false)
		position, err := r.Seek(0, io.SeekCurrent)
		if !idOK || !sizeOK || err != nil || position > end || length > uint64(end-position) {
			return false
		}
		if id == 0x4282 { // EBML DocType distinguishes WebM from Matroska.
			var docType [4]byte
			_, err := io.ReadFull(r, docType[:])
			return length == 4 && err == nil && string(docType[:]) == "webm"
		}
		start, err = r.Seek(int64(length), io.SeekCurrent)
		if err != nil {
			return false
		}
	}
	return false
}

func readEBMLValue(r io.Reader, keepMarker bool) (uint64, bool) {
	var data [8]byte
	if _, err := io.ReadFull(r, data[:1]); err != nil || data[0] == 0 {
		return 0, false
	}
	length, marker := 1, byte(0x80)
	for data[0]&marker == 0 {
		length++
		marker >>= 1
	}
	if _, err := io.ReadFull(r, data[1:length]); err != nil {
		return 0, false
	}
	value := uint64(data[0])
	if !keepMarker {
		value &^= uint64(marker)
	}
	for _, b := range data[1:length] {
		value = value<<8 | uint64(b)
	}
	return value, true
}
