package clipvault

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/iotest"
	"time"
)

func TestBackgroundUploadOutlivesDefaultRequestTimeouts(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	app := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}
	server := httptest.NewUnstartedServer(http.HandlerFunc(app.handleBackgroundMediaUpload))
	server.Config.ReadTimeout = 100 * time.Millisecond
	server.Config.WriteTimeout = 100 * time.Millisecond
	server.Start()
	defer server.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	go func() {
		defer writer.Close()
		_, _ = writer.Write([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'm', 'p', '4', '1'})
		time.Sleep(300 * time.Millisecond)
		_, _ = writer.Write(bytes.Repeat([]byte{0}, 4096))
	}()
	request, err := http.NewRequest(http.MethodPut, server.URL, reader)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("progressing upload failed: status %d, error %v, body %s", response.StatusCode, err, body)
	}
	if manager.Get().Background.ContentType != "video/mp4" {
		t.Fatal("generic MIME video was not stored")
	}
}

func TestMediaTransferRenewsIdleDeadlines(t *testing.T) {
	recorder := &importDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	controller := http.NewResponseController(recorder)
	reader := mediaUploadReader{bytes.NewReader([]byte("abc")), controller}
	writer := mediaResponseWriter{recorder, controller}
	for range 3 {
		recorder.readDeadline = time.Time{}
		recorder.writeDeadline = time.Time{}
		before := time.Now()
		var data [1]byte
		if _, err := reader.Read(data[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data[:]); err != nil {
			t.Fatal(err)
		}
		for _, deadline := range []time.Time{recorder.readDeadline, recorder.writeDeadline} {
			if deadline.Before(before.Add(mediaTransferIdleTimeout)) || deadline.After(time.Now().Add(mediaTransferIdleTimeout)) {
				t.Fatalf("idle deadline not refreshed: %v", deadline)
			}
		}
	}
	if recorder.Body.String() != "abc" {
		t.Fatal("stream contents changed")
	}
}

func TestBackgroundInterruptedUploadRemovesTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	app := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}
	body := io.MultiReader(bytes.NewReader([]byte("partial upload")), iotest.ErrReader(io.ErrUnexpectedEOF))
	request := httptest.NewRequest(http.MethodPut, "/api/v1/appearance/background", body)
	response := httptest.NewRecorder()
	app.handleBackgroundMediaUpload(response, request)
	if response.Code != http.StatusBadRequest || manager.Get().Background.Version != 0 {
		t.Fatalf("interrupted upload changed background: %d %s", response.Code, response.Body.String())
	}
	paths, err := filepath.Glob(filepath.Join(dir, ".background-upload-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("interrupted upload left temporary files: %v, %v", paths, err)
	}
}
