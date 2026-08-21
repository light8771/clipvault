package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// newExportHTTPTestServer 搭建与生产路由一致的处理链
// （ensureAPIErrorContract → envelopeJSON → handleExportData），
// 并挂到真实 HTTP 传输层。ResponseRecorder 不校验 Content-Length
// 超写，导出类缺陷只有走 net/http 全链路才能暴露，故必须用 NewServer。
func newExportHTTPTestServer(t *testing.T, store *Store) *httptest.Server {
	t.Helper()
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	server := &SearchServer{store: store, logger: logger}
	ts := httptest.NewServer(ensureAPIErrorContract(server.envelopeJSON(server.handleExportData)))
	t.Cleanup(ts.Close)
	return ts
}

func newExportTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	for _, text := range []string{"hello world", "你好世界"} {
		if err := store.Save(ctx, ClipboardItem{
			Kind: "text", Format: "CF_UNICODETEXT", Text: text, MetaJSON: "{}",
		}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	return store
}

// assertDownloadIntegrity 校验文件下载响应的完整性契约：
// 声明的 Content-Length 与实际响应体字节一致（Envelope 缓冲重包
// 破坏该契约时，net/http 会中止连接，ReadAll 直接报错）。
func assertDownloadIntegrity(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	if declared := resp.Header.Get("Content-Length"); declared != "" {
		if declared != strconv.Itoa(len(body)) {
			t.Fatalf("Content-Length = %s, actual body = %d bytes", declared, len(body))
		}
	} else {
		t.Fatal("download response has no Content-Length")
	}
	if len(body) == 0 {
		t.Fatal("download body is empty")
	}
	if disposition := resp.Header.Get("Content-Disposition"); disposition == "" {
		t.Fatal("download response has no Content-Disposition")
	}
	return body
}

// TestExportJSONOverFullHTTPStack 是 BUG-1 的回归防线：JSON 明文导出
// 走完整 HTTP 链路必须原样送达（不被信封重包、不被 Content-Length 截断）。
func TestExportJSONOverFullHTTPStack(t *testing.T) {
	ts := newExportHTTPTestServer(t, newExportTestStore(t))

	resp, err := http.Get(ts.URL + "/api/v1/transfer/export?format=json")
	if err != nil {
		t.Fatalf("GET json export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("json export status = %d", resp.StatusCode)
	}
	body := assertDownloadIntegrity(t, resp)

	var export ExportData
	if err := json.Unmarshal(body, &export); err != nil {
		t.Fatalf("json export body is not ExportData: %v\nbody=%s", err, body)
	}
	if export.App != "ClipVault" {
		t.Fatalf("export app = %q", export.App)
	}
	if len(export.Items) != 2 {
		t.Fatalf("export items = %d, want 2", len(export.Items))
	}
}

func TestExportXLSXOverFullHTTPStack(t *testing.T) {
	ts := newExportHTTPTestServer(t, newExportTestStore(t))

	resp, err := http.Get(ts.URL + "/api/v1/transfer/export?format=xlsx")
	if err != nil {
		t.Fatalf("GET xlsx export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("xlsx export status = %d", resp.StatusCode)
	}
	body := assertDownloadIntegrity(t, resp)

	book, err := decodeImportXLSX(body)
	if err != nil {
		t.Fatalf("xlsx export body is not a ClipVault workbook: %v", err)
	}
	if len(book.Items) != 2 {
		t.Fatalf("xlsx export items = %d, want 2", len(book.Items))
	}
}

func TestExportCVExportOverFullHTTPStack(t *testing.T) {
	ts := newExportHTTPTestServer(t, newExportTestStore(t))

	resp, err := http.Post(ts.URL+"/api/v1/transfer/export?format=cvexport",
		"application/json", bytes.NewReader([]byte(`{"password":"test-password"}`)))
	if err != nil {
		t.Fatalf("POST cvexport export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cvexport export status = %d", resp.StatusCode)
	}
	body := assertDownloadIntegrity(t, resp)

	if !IsCVExport(body) {
		t.Fatalf("cvexport export body has no CVEXP magic, got %q", body[:min(len(body), 8)])
	}
	plain, err := DecryptExport(body, "test-password")
	if err != nil {
		t.Fatalf("decrypt cvexport export: %v", err)
	}
	var export ExportData
	if err := json.Unmarshal(plain, &export); err != nil {
		t.Fatalf("decrypted cvexport payload is not ExportData: %v", err)
	}
	if len(export.Items) != 2 {
		t.Fatalf("cvexport items = %d, want 2", len(export.Items))
	}
}

// TestExportErrorKeepsEnvelopeOverHTTPStack 确认附件直通只影响成功下载：
// 错误响应（无 Content-Disposition）仍重包为 {"error":{...}} API 契约。
func TestExportErrorKeepsEnvelopeOverHTTPStack(t *testing.T) {
	ts := newExportHTTPTestServer(t, newExportTestStore(t))

	resp, err := http.Get(ts.URL + "/api/v1/transfer/export?format=invalid")
	if err != nil {
		t.Fatalf("GET invalid export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid export status = %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read error body: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("error body = %s", body)
	}
	errObj, ok := payload["error"].(map[string]any)
	if !ok || errObj["code"] != "unsupported_format" {
		t.Fatalf("error envelope = %s", body)
	}
}
