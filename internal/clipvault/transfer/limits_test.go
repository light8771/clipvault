package transfer

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSizeLimitReaderBoundary(t *testing.T) {
	for _, input := range []string{"123", "1234", "12345"} {
		t.Run(input, func(t *testing.T) {
			r := &sizeLimitReader{src: strings.NewReader(input), remaining: 4, limitErr: ErrJSONTooLarge}
			data, err := io.ReadAll(r)
			if string(data) != input[:min(4, len(input))] || errors.Is(err, ErrJSONTooLarge) != (len(input) > 4) {
				t.Fatalf("read = %q, %v", data, err)
			}
		})
	}
}

func TestSizeLimitWriterCountsAcrossWrites(t *testing.T) {
	var dst bytes.Buffer
	w := &sizeLimitWriter{dst: &dst, remaining: 4, limitErr: ErrJSONTooLarge}
	for _, chunk := range []string{"12", "34", ""} {
		if n, err := io.WriteString(w, chunk); n != len(chunk) || err != nil {
			t.Fatalf("write %q = %d, %v", chunk, n, err)
		}
	}
	if n, err := io.WriteString(w, "5"); n != 0 || !errors.Is(err, ErrJSONTooLarge) {
		t.Fatalf("overflow write = %d, %v", n, err)
	}
	if dst.String() != "1234" {
		t.Fatalf("output = %q", dst.String())
	}
}

// Exercise the public wrappers at the real limit without allocating the
// complete document. Both sides must accept exactly the same number of bytes.
func TestJSONLimitsAgree(t *testing.T) {
	w := NewJSONWriter(io.Discard)
	block := make([]byte, 1<<20)
	for remaining := int64(MaxJSONBytes); remaining > 0; {
		n, err := w.Write(block[:min(int64(len(block)), remaining)])
		if err != nil {
			t.Fatal(err)
		}
		remaining -= int64(n)
	}
	if _, err := w.Write([]byte{0}); !errors.Is(err, ErrJSONTooLarge) {
		t.Fatalf("write overflow: %v", err)
	}
	for _, size := range []int64{MaxJSONBytes, MaxJSONBytes + 1} {
		n, err := io.Copy(io.Discard, NewJSONReader(io.LimitReader(zeroReader{}, size)))
		if n != MaxJSONBytes || errors.Is(err, ErrJSONTooLarge) != (size > MaxJSONBytes) {
			t.Fatalf("read %d bytes: n=%d err=%v", size, n, err)
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
