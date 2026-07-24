//go:build windows

package clipvault

import (
	"strings"
	"testing"
)

func TestCopyUTF16NULTerminatedPreservesTerminatorAndSurrogates(t *testing.T) {
	buffer := make([]uint16, 4)
	copyUTF16NULTerminated(buffer, "😀😀")
	if buffer[len(buffer)-1] != 0 {
		t.Fatalf("buffer is not NUL terminated: %#v", buffer)
	}
	for i, unit := range buffer[:len(buffer)-1] {
		if unit >= 0xD800 && unit <= 0xDBFF && (i+1 == len(buffer)-1 || buffer[i+1] < 0xDC00 || buffer[i+1] > 0xDFFF) {
			t.Fatalf("buffer has dangling high surrogate: %#v", buffer)
		}
	}
}

func TestCopyUTF16NULTerminatedTruncatesNotificationBody(t *testing.T) {
	buffer := make([]uint16, 256)
	copyUTF16NULTerminated(buffer, strings.Repeat("😀", 200))
	if buffer[len(buffer)-1] != 0 {
		t.Fatalf("notification body is not NUL terminated")
	}
}
