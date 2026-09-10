package clipvault

import (
	"bytes"
	"testing"
)

func TestVideoContainerDetection(t *testing.T) {
	mp4 := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'm', 'p', '4', '1'}
	webm := []byte{0x1a, 0x45, 0xdf, 0xa3, 0x87, 0x42, 0x82, 0x84, 'w', 'e', 'b', 'm'}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"mp4", mp4, "video/mp4"},
		{"leading-free-box", append([]byte{0, 0, 0, 8, 'f', 'r', 'e', 'e'}, mp4...), "video/mp4"},
		{"webm", webm, "video/webm"},
		{"truncated-mp4", mp4[:12], ""},
		{"truncated-webm", webm[:8], ""},
		{"avif", []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'a', 'v', 'i', 'f', 0, 0, 0, 0, 'm', 'i', 'f', '1'}, ""},
		{"matroska", []byte{0x1a, 0x45, 0xdf, 0xa3, 0x8b, 0x42, 0x82, 0x88, 'm', 'a', 't', 'r', 'o', 's', 'k', 'a'}, ""},
		{"invalid-box-size", []byte{0, 0, 0, 4, 'f', 't', 'y', 'p'}, ""},
		{"overflow-box-size", []byte{0, 0, 0, 1, 'f', 't', 'y', 'p', 255, 255, 255, 255, 255, 255, 255, 255}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectBackgroundContentType(tc.data); got != tc.want {
				t.Fatalf("type = %q, want %q", got, tc.want)
			}
		})
	}
	for i := 0; i < len(webm); i++ {
		if detectVideoContentType(bytes.NewReader(webm[:i])) != "" {
			t.Fatalf("truncated WebM prefix %d accepted", i)
		}
	}
}
