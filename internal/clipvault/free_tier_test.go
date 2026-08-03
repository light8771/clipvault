package clipvault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"clipboard_ext/internal/licensing"
)

// newUnlicensedManager 构造一个未激活且强制授权的 Manager（测试构建未注入公钥，
// 因此状态为 unconfigured：Valid=false、Enforced=true，CanUsePaidFeatures=false）。
func newUnlicensedManager(t *testing.T) *licensing.Manager {
	t.Helper()
	m := licensing.NewManager(filepath.Join(t.TempDir(), "none.lic"), licensing.BuiltinTrustedKeys())
	m.Load()
	if m.CanUsePaidFeatures() {
		t.Fatal("expected an unlicensed manager")
	}
	return m
}

func newFreeTierTestServer(t *testing.T, store *Store, license *licensing.Manager) *SearchServer {
	t.Helper()
	cfgMgr, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Close() })
	return NewSearchServer(cfgMgr, store, NewRuntimeState(false), logger, Environment{}, nil, license)
}

func saveFreeTierItems(t *testing.T, store *Store, texts []string) {
	t.Helper()
	for _, txt := range texts {
		if err := store.Save(context.Background(), ClipboardItem{
			Kind: "text", Format: "CF_UNICODETEXT", Text: txt,
			ByteLen: len(txt), CharLen: len(txt), SHA256: hashString(txt), MetaJSON: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSearchRestrictToLatestN(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	saveFreeTierItems(t, store, []string{"oldest alpha", "second beta", "third gamma", "fourth delta", "newest epsilon"})

	// 窗口 = 3：只返回最新 3 条，total 也是窗口内计数
	items, total, err := store.Search(ctx, SearchOptions{Limit: 50, RestrictToLatestN: 3})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("window search got total=%d len=%d, want 3/3", total, len(items))
	}
	if items[0].Text != "newest epsilon" || items[2].Text != "third gamma" {
		t.Fatalf("window returned wrong items: %+v", items)
	}

	// 窗口内搜索语义：窗口外的旧记录搜不到
	if _, total, err = store.Search(ctx, SearchOptions{Query: "alpha", Limit: 50, RestrictToLatestN: 3}); err != nil || total != 0 {
		t.Fatalf("out-of-window query total=%d err=%v, want 0", total, err)
	}
	if _, total, err = store.Search(ctx, SearchOptions{Query: "epsilon", Limit: 50, RestrictToLatestN: 3}); err != nil || total != 1 {
		t.Fatalf("in-window query total=%d err=%v, want 1", total, err)
	}

	// 0 = 不限制；窗口大于全量 = 全部
	if _, total, err = store.Search(ctx, SearchOptions{Limit: 50}); err != nil || total != 5 {
		t.Fatalf("unrestricted total=%d err=%v, want 5", total, err)
	}
	if _, total, err = store.Search(ctx, SearchOptions{Limit: 50, RestrictToLatestN: 100}); err != nil || total != 5 {
		t.Fatalf("oversized window total=%d err=%v, want 5", total, err)
	}

	// CountAll 无视窗口与过滤，返回全量总数
	if n, err := store.CountAll(ctx); err != nil || n != 5 {
		t.Fatalf("CountAll=%d err=%v, want 5", n, err)
	}
}

func TestHandleSearchFreeTierWindow(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	texts := make([]string, 0, FreeTierVisibleLimit+10)
	texts = append(texts, "ancient-out-of-window") // id 最小，落在窗口外
	for i := 0; i < FreeTierVisibleLimit+9; i++ {
		texts = append(texts, "record-"+strconv.Itoa(i))
	}
	saveFreeTierItems(t, store, texts)

	search := func(server *SearchServer, url string) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		server.handleSearch(w, httptest.NewRequest(http.MethodGet, url, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("search status = %d, body=%s", w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}

	unlicensed := newFreeTierTestServer(t, store, newUnlicensedManager(t))
	resp := search(unlicensed, "/api/search?limit=500")
	if got := int(resp["total"].(float64)); got != FreeTierVisibleLimit {
		t.Fatalf("unlicensed total=%d, want %d", got, FreeTierVisibleLimit)
	}
	if got := int(resp["fullTotal"].(float64)); got != FreeTierVisibleLimit+10 {
		t.Fatalf("unlicensed fullTotal=%d, want %d", got, FreeTierVisibleLimit+10)
	}
	if items := resp["items"].([]any); len(items) != FreeTierVisibleLimit {
		t.Fatalf("unlicensed items=%d, want %d", len(items), FreeTierVisibleLimit)
	}

	// 窗口内搜索语义：窗口外的记录搜不到
	resp = search(unlicensed, "/api/search?q=ancient&limit=500")
	if got := int(resp["total"].(float64)); got != 0 {
		t.Fatalf("unlicensed out-of-window query total=%d, want 0", got)
	}

	// 无授权 Manager（未配置强制）视为完整功能：不受窗口限制，也不返回 fullTotal
	licensed := newFreeTierTestServer(t, store, nil)
	resp = search(licensed, "/api/search?limit=500")
	if got := int(resp["total"].(float64)); got != FreeTierVisibleLimit+10 {
		t.Fatalf("licensed total=%d, want %d", got, FreeTierVisibleLimit+10)
	}
	if _, ok := resp["fullTotal"]; ok {
		t.Fatal("licensed response must not contain fullTotal")
	}
	resp = search(licensed, "/api/search?q=ancient&limit=500")
	if got := int(resp["total"].(float64)); got != 1 {
		t.Fatalf("licensed query total=%d, want 1", got)
	}
}

func TestHandleItemPositionFreeTier(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	texts := make([]string, 0, FreeTierVisibleLimit+10)
	for i := 0; i < FreeTierVisibleLimit+10; i++ {
		texts = append(texts, "record-"+strconv.Itoa(i))
	}
	saveFreeTierItems(t, store, texts)

	items, _, err := store.Search(ctx, SearchOptions{Limit: 500})
	if err != nil || len(items) != FreeTierVisibleLimit+10 {
		t.Fatalf("setup search len=%d err=%v", len(items), err)
	}
	newestID := items[0].ID                  // 按 id DESC 第一个 = 最新
	oldestID := items[len(items)-1].ID       // 窗口外
	unlicensed := newFreeTierTestServer(t, store, newUnlicensedManager(t))

	// 窗口内：正常返回 position
	w := httptest.NewRecorder()
	unlicensed.handleItemPosition(w, httptest.NewRequest(http.MethodGet, "/api/items/position?id="+strconv.FormatInt(newestID, 10), nil))
	var okResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &okResp); err != nil {
		t.Fatal(err)
	}
	if pos, exists := okResp["position"]; !exists || int(pos.(float64)) != 0 {
		t.Fatalf("newest position resp=%v, want position 0", okResp)
	}

	// 窗口外：返回 402 + 错误提示（#19 统一契约：窗口外定位不再是 200 成功码）
	w = httptest.NewRecorder()
	unlicensed.handleItemPosition(w, httptest.NewRequest(http.MethodGet, "/api/items/position?id="+strconv.FormatInt(oldestID, 10), nil))
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("out-of-window position status = %d, want 402", w.Code)
	}
	var errResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatal(err)
	}
	if _, exists := errResp["error"]; !exists {
		t.Fatalf("out-of-window position resp=%v, want error", errResp)
	}

	// 完整功能：窗口外也能定位
	licensed := newFreeTierTestServer(t, store, nil)
	w = httptest.NewRecorder()
	licensed.handleItemPosition(w, httptest.NewRequest(http.MethodGet, "/api/items/position?id="+strconv.FormatInt(oldestID, 10), nil))
	var licResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &licResp); err != nil {
		t.Fatal(err)
	}
	if pos, exists := licResp["position"]; !exists || int(pos.(float64)) != FreeTierVisibleLimit+9 {
		t.Fatalf("licensed oldest position resp=%v, want position %d", licResp, FreeTierVisibleLimit+9)
	}
}

func TestHandleExportDataFreeTierBlocked(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	saveFreeTierItems(t, store, []string{"export-me"})

	// 未激活：402 + license_required
	unlicensed := newFreeTierTestServer(t, store, newUnlicensedManager(t))
	w := httptest.NewRecorder()
	unlicensed.handleExportData(w, httptest.NewRequest(http.MethodGet, "/api/export/data?format=json", nil))
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("unlicensed export status = %d, want 402", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["error"] != "license_required" {
		t.Fatalf("unlicensed export error = %v, want license_required", resp["error"])
	}

	// 完整功能：正常导出
	licensed := newFreeTierTestServer(t, store, nil)
	w = httptest.NewRecorder()
	licensed.handleExportData(w, httptest.NewRequest(http.MethodGet, "/api/export/data?format=json", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("licensed export status = %d, body=%s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); cd == "" {
		t.Fatal("licensed export missing Content-Disposition")
	}
}

// TestRecordedFileFreeTierWindow 覆盖 #2b(a)：文件类端点的免费版读窗口门控。
//
// resolveRecordedFilePath 是 handleFileCheck / handleFileServe / handleFileHash /
// handleFileOpenDir 四个端点的共同入口，门控逻辑集中于此，故以 handleFileCheck 为代表验证。
// 门控刻意排在 GetByID 之前：窗口外请求在触碰记录内容之前就被拒绝，
// 避免通过响应差异探测窗口外记录是否存在。
func TestRecordedFileFreeTierWindow(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 真实落盘一个文件，使窗口内断言能验到 exists=true，而不只是 HTTP 200。
	p := filepath.Join(t.TempDir(), "recorded.png")
	if err := os.WriteFile(p, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	saveFileRecord := func() {
		t.Helper()
		if err := store.Save(ctx, ClipboardItem{
			Kind: "image", Format: "CF_HDROP", Text: p,
			ByteLen: len(p), CharLen: len(p), SHA256: hashString(p), MetaJSON: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 先存一条文件记录，再灌满窗口把它挤出去，最后补一条落在窗口内。
	// Store.Save 是纯 INSERT（不按 sha256 去重），两条同路径记录会拿到不同 id。
	saveFileRecord()
	texts := make([]string, 0, FreeTierVisibleLimit+5)
	for i := 0; i < FreeTierVisibleLimit+5; i++ {
		texts = append(texts, "filler-"+strconv.Itoa(i))
	}
	saveFreeTierItems(t, store, texts)
	saveFileRecord()

	items, _, err := store.Search(ctx, SearchOptions{Limit: 500})
	if err != nil || len(items) != FreeTierVisibleLimit+7 {
		t.Fatalf("setup search len=%d err=%v, want %d", len(items), err, FreeTierVisibleLimit+7)
	}
	// items 按 id DESC 排序：第一条是最新（position 0，窗口内），
	// 最后一条是最旧（position = FreeTierVisibleLimit+6，窗口外）。
	inWindowID := items[0].ID
	outOfWindowID := items[len(items)-1].ID

	checkFile := func(server *SearchServer, itemID int64) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		server.handleFileCheck(w, httptest.NewRequest(http.MethodGet,
			"/api/file/check?item_id="+strconv.FormatInt(itemID, 10)+"&file_index=0", nil))
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode body %q: %v", w.Body.String(), err)
		}
		return w.Code, resp
	}

	unlicensed := newFreeTierTestServer(t, store, newUnlicensedManager(t))

	// 1) 未激活 + 窗口内：门控放行，正常解析出路径
	code, resp := checkFile(unlicensed, inWindowID)
	if code != http.StatusOK || resp["exists"] != true {
		t.Fatalf("in-window check status=%d resp=%v, want 200 exists=true", code, resp)
	}
	if resp["name"] != "recorded.png" {
		t.Fatalf("in-window name=%v, want recorded.png", resp["name"])
	}

	// 2) 未激活 + 窗口外：402 + window_outside（与 handleItemPosition 的 #19 契约一致，
	//    不能退化成 400 invalid_file_ref，否则前端无法区分「限额」与「参数错误」）
	code, resp = checkFile(unlicensed, outOfWindowID)
	if code != http.StatusPaymentRequired {
		t.Fatalf("out-of-window check status=%d resp=%v, want 402", code, resp)
	}
	if resp["code"] != "window_outside" {
		t.Fatalf("out-of-window code=%v, want window_outside", resp["code"])
	}

	// 3) license 为 nil：门控整体不生效，窗口外记录也能解析。
	//    注意此处验的是「s.license == nil 时跳过门控」这一条分支，
	//    **不等于**「已激活用户不受限」——测试构建未注入内置公钥，造不出 Valid=true 的
	//    Manager，已激活分支至今仍无覆盖（审查报告 #26 范围）。
	ungated := newFreeTierTestServer(t, store, nil)
	code, resp = checkFile(ungated, outOfWindowID)
	if code != http.StatusOK || resp["exists"] != true {
		t.Fatalf("ungated out-of-window check status=%d resp=%v, want 200 exists=true", code, resp)
	}
}
