package clipvault

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestServeExportFileSupportsPostDownload(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "export-*.cvexport")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	want := []byte("streamed export")
	if _, err := file.Write(want); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/transfer/export", nil)
	if err := serveExportFile(recorder, request, file, "data.cvexport", "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || recorder.Body.String() != string(want) {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}
