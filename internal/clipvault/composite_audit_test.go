package clipvault

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

func TestCompositeNetworkCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	load, release := newCompositeImageLoader(ctx, []byte("SourceURL:"+server.URL+"/page\r\n"))
	defer release()
	done := make(chan error, 1)
	go func() { _, err := load(server.URL+"/slow", 1024); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("request ignored capture cancellation")
	}
}

func TestCompositePreviewPrefersHTMLAndFallsBack(t *testing.T) {
	for _, markup := range []string{"<p>standard HTML</p>", "<script>hidden()</script>"} {
		c := &domain.CompositeContent{Representations: []domain.ContentRepresentation{
			{Format: compositeQQ, Data: []byte(`<QQRichEditFormat></QQRichEditFormat>`)},
			{Format: compositeWeChat, Data: []byte(`<WeChatRichEditFormat><EditElement type="0">application fallback</EditElement></WeChatRichEditFormat>`)},
			{Format: compositeHTML, Data: []byte(markup)},
		}}
		before := compositeJSONString(c)
		preview := compositePreviewHTML(c, func(string) string { return "" })
		want := "standard HTML"
		if strings.HasPrefix(markup, "<script>") {
			want = "application fallback"
		}
		if !strings.Contains(preview, want) || strings.Contains(preview, "hidden") {
			t.Fatalf("preview = %q, want %q", preview, want)
		}
		if before != compositeJSONString(c) {
			t.Fatal("preview mutated source")
		}
	}
}

func TestCompositeResourceLimitHasAccurateDiagnostic(t *testing.T) {
	raw := []byte(`<img src="https://example.test/large.png">`)
	snapshot, err := buildCompositeSnapshot(DefaultConfig(), nil, nil, map[string][]byte{"HTML Format": raw}, nil,
		func(string, int) ([]byte, error) { return nil, ErrImageLimitExceeded })
	if err != nil {
		t.Fatal(err)
	}
	issues := snapshot.Content.Issues
	if len(issues) != 1 || issues[0].Reason != "resource_limit" {
		t.Fatalf("issues = %+v", issues)
	}
	if string(snapshot.Content.Representations[0].Data) != string(raw) {
		t.Fatal("original lost")
	}
}
