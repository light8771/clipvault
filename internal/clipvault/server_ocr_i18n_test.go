package clipvault

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"clipboard_ext/internal/clipvault/ocr"
)

func TestOCRPageAndResponsesFollowCurrentLanguage(t *testing.T) {
	svc := newI18nTestService(t, nil)
	previous := templateI18nService.Load()
	SetTemplateI18nService(svc)
	t.Cleanup(func() { SetTemplateI18nService(previous) })
	server, _, session := newMaskTestServer(t, svc.store)
	server.SetI18nService(svc)
	SetTemplateI18nService(svc)
	server.env.DataDir = svc.store.dataDir
	snapshot := compositeFixture(t, `C:\temp\synthetic-ocr.png`)
	if err := svc.store.Save(context.Background(), ClipboardItem{RecordType: RecordTypeContent, Kind: snapshot.Kind, Format: snapshot.Format, Text: snapshot.Text, Content: snapshot.Content, MetaJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	items, err := exportAll(context.Background(), svc.store.db)
	if err != nil {
		t.Fatal(err)
	}
	id := items[0].ID
	server.ocrTasks = map[int64]ocrTask{id: {message: &ocr.MessageError{Key: "ocr.error.http_status", Params: map[string]any{"status": 429}}}}
	for _, tc := range []struct{ language, title, image, network, status string }{
		{I18nChinese, "图片识别设置", "内嵌图片 1", "请求中断或网络超时", "识别接口返回 HTTP 429"},
		{I18nEnglish, "Image recognition settings", "Embedded image 1", "interrupted or timed out", "returned HTTP 429"},
	} {
		if err := svc.SetLanguage(tc.language); err != nil {
			t.Fatal(err)
		}
		body := renderApplicationPage(t, "/settings/ocr")
		if !strings.Contains(body, tc.title) || !strings.Contains(body, `id="s-ocr"`) || !strings.Contains(body, `href="/settings/ocr" aria-current="page"`) {
			t.Fatalf("OCR page not localized/active: %s", tc.language)
		}
		if strings.Contains(renderApplicationPage(t, "/settings/service"), `id="s-ocr"`) {
			t.Fatal("OCR form still nested under service settings")
		}
		request := httptest.NewRequest("GET", "/api/v1/items/ocr?id="+strconv.FormatInt(id, 10), nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		response := httptest.NewRecorder()
		server.handleOCR(response, request)
		if response.Code != 200 || !strings.Contains(response.Body.String(), tc.image) || !strings.Contains(response.Body.String(), tc.status) {
			t.Fatalf("image/task localization: %d %s", response.Code, response.Body.String())
		}
		response = httptest.NewRecorder()
		server.ocrError(response, 502, "ocr.error.network")
		if !strings.Contains(response.Body.String(), tc.network) {
			t.Fatalf("error localization: %s", response.Body.String())
		}
	}
}
