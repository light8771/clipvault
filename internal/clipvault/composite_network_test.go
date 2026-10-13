package clipvault

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompositeNetworkImageSnapshot(t *testing.T) {
	png := managedImageTestPNG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credentials forwarded")
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer server.Close()
	load, close := newCompositeImageLoader(context.Background(), []byte("SourceURL:"+server.URL+"/page\r\n"))
	defer close()
	for _, ref := range []string{server.URL + "/a.png", "/a.png", strings.TrimPrefix(server.URL, "http:") + "/a.png"} {
		data, err := load(ref, 1<<20)
		if err != nil || !bytes.Equal(data, png) {
			t.Fatalf("snapshot: %v", err)
		}
	}
	if _, err := load(server.URL+"/a.png", len(png)-1); !errors.Is(err, ErrImageLimitExceeded) {
		t.Fatalf("size limit: %v", err)
	}
}

func TestCompositeNetworkDoesNotReachOtherPrivateOrigins(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/secret", http.StatusFound)
	}))
	defer origin.Close()
	load, close := newCompositeImageLoader(context.Background(), []byte("SourceURL:"+origin.URL+"/page\r\n"))
	defer close()
	if _, err := load(target.URL+"/secret", 1024); err == nil {
		t.Fatal("cross-origin private resource allowed")
	}
	if _, err := load(origin.URL+"/redirect", 1024); err == nil {
		t.Fatal("redirect silently followed")
	}
	if hits.Load() != 0 {
		t.Fatal("unexpected request reached target")
	}
}

func TestCompositeNetworkCaptureDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	load, close := newCompositeImageLoader(context.Background(), []byte("SourceURL:"+server.URL+"/page\r\n"))
	defer close()
	start := time.Now()
	for i := 0; i < 4; i++ {
		if _, err := load(server.URL+"/slow", 1024); err == nil {
			t.Fatal("unexpected success")
		}
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("capture exceeded shared deadline")
	}
}
