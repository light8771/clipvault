package clipvault

import (
	"io"
	"net/http"
	"time"
)

// Media transfers may run indefinitely while progressing. Each network read or
// write gets its own idle deadline instead of sharing a total request timeout.
const mediaTransferIdleTimeout = 30 * time.Second

type mediaUploadReader struct {
	src        io.Reader
	controller *http.ResponseController
}

func (r mediaUploadReader) Read(p []byte) (int, error) {
	if err := setReadDeadlineIfSupported(r.controller, time.Now().Add(mediaTransferIdleTimeout)); err != nil {
		return 0, err
	}
	return r.src.Read(p)
}

type mediaResponseWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
}

func (w mediaResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w mediaResponseWriter) Write(p []byte) (int, error) {
	if err := setWriteDeadlineIfSupported(w.controller, time.Now().Add(mediaTransferIdleTimeout)); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}
