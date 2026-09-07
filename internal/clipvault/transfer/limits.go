package transfer

import (
	"fmt"
	"io"
)

// MaxJSONBytes bounds the complete JSON document before any ZIP, gzip or
// encryption layer. Every JSON producer and consumer uses the same limit.
const MaxJSONBytes = 500 << 20

var ErrJSONTooLarge = fmt.Errorf("JSON payload exceeds %d bytes (500 MiB)", MaxJSONBytes)

func NewJSONWriter(dst io.Writer) io.Writer {
	return &sizeLimitWriter{dst: dst, remaining: MaxJSONBytes, limitErr: ErrJSONTooLarge}
}

func NewJSONReader(src io.Reader) io.Reader {
	return &sizeLimitReader{src: src, remaining: MaxJSONBytes, limitErr: ErrJSONTooLarge}
}

type sizeLimitWriter struct {
	dst       io.Writer
	remaining int64
	limitErr  error
}

func (w *sizeLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, w.limitErr
	}
	n, err := w.dst.Write(p)
	w.remaining -= int64(n)
	if n < len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}

type sizeLimitReader struct {
	src       io.Reader
	remaining int64
	limitErr  error
}

func (r *sizeLimitReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.src.Read(probe[:])
		if n > 0 {
			return 0, r.limitErr
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.src.Read(p)
	r.remaining -= int64(n)
	return n, err
}
