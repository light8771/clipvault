package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestBrandLogoUploadServeAndReset(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	server := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}

	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	upload := httptest.NewRequest(http.MethodPut, "http://clipvault.local/api/v1/appearance/logo", bytes.NewReader(imageData.Bytes()))
	upload.Header.Set("Content-Type", "image/png")
	uploadResult := httptest.NewRecorder()
	server.handleBrandLogoUpload(uploadResult, upload)
	if uploadResult.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want %d: %s", uploadResult.Code, http.StatusOK, uploadResult.Body.String())
	}
	brand := manager.Get().Brand
	if brand.LogoContentType != "image/png" || brand.LogoVersion <= 0 {
		t.Fatalf("uploaded logo config = %#v", brand)
	}
	if _, err := os.Stat(filepath.Join(dir, assetFileName(brandLogoAsset, brand.LogoVersion))); err != nil {
		t.Fatalf("uploaded logo file: %v", err)
	}
	reloadedManager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("reload config manager: %v", err)
	}
	if got := reloadedManager.Get().Brand; got.LogoContentType != "image/png" || got.LogoVersion != brand.LogoVersion {
		t.Fatalf("persisted logo metadata = %#v", got)
	}

	serve := httptest.NewRecorder()
	server.handleBrandLogo(serve, httptest.NewRequest(http.MethodGet, "http://clipvault.local/brand/logo", nil))
	if serve.Code != http.StatusOK || serve.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("served logo = status %d, content type %q", serve.Code, serve.Header().Get("Content-Type"))
	}
	if got := serve.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("unversioned logo cache control = %q", got)
	}
	if !bytes.Equal(serve.Body.Bytes(), imageData.Bytes()) {
		t.Fatal("served logo differs from uploaded image")
	}
	versioned := httptest.NewRecorder()
	server.handleBrandLogo(versioned, httptest.NewRequest(http.MethodGet, "http://clipvault.local/brand/logo?v="+strconv.FormatInt(brand.LogoVersion, 10), nil))
	if versioned.Code != http.StatusOK || !bytes.Equal(versioned.Body.Bytes(), imageData.Bytes()) {
		t.Fatalf("versioned logo status = %d", versioned.Code)
	}
	if got := versioned.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("versioned logo cache control = %q", got)
	}
	stale := httptest.NewRecorder()
	server.handleBrandLogo(stale, httptest.NewRequest(http.MethodGet, "http://clipvault.local/brand/logo?v="+strconv.FormatInt(brand.LogoVersion-1, 10), nil))
	if stale.Code != http.StatusNoContent {
		t.Fatalf("stale logo version status = %d, want %d", stale.Code, http.StatusNoContent)
	}

	var replacement bytes.Buffer
	replacementImage := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	replacementImage.Set(0, 0, color.NRGBA{G: 255, A: 255})
	if err := png.Encode(&replacement, replacementImage); err != nil {
		t.Fatalf("encode replacement png: %v", err)
	}
	replaceReq := httptest.NewRequest(http.MethodPut, "http://clipvault.local/api/v1/appearance/logo", bytes.NewReader(replacement.Bytes()))
	replaceReq.Header.Set("Content-Type", "image/png")
	replaceRes := httptest.NewRecorder()
	server.handleBrandLogoUpload(replaceRes, replaceReq)
	if replaceRes.Code != http.StatusOK {
		t.Fatalf("replacement upload status = %d, want %d", replaceRes.Code, http.StatusOK)
	}
	serveReplacement := httptest.NewRecorder()
	server.handleBrandLogo(serveReplacement, httptest.NewRequest(http.MethodGet, "http://clipvault.local/brand/logo", nil))
	if !bytes.Equal(serveReplacement.Body.Bytes(), replacement.Bytes()) {
		t.Fatal("replacement logo was not served")
	}
	if _, err := os.Stat(filepath.Join(dir, assetFileName(brandLogoAsset, brand.LogoVersion))); !os.IsNotExist(err) {
		t.Fatalf("old logo file remains after replacement: %v", err)
	}
	replacementVersion := manager.Get().Brand.LogoVersion

	reset := httptest.NewRecorder()
	server.handleBrandLogoReset(reset, httptest.NewRequest(http.MethodDelete, "http://clipvault.local/api/v1/appearance/logo", nil))
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want %d", reset.Code, http.StatusOK)
	}
	if got := manager.Get().Brand; got.LogoContentType != "" || got.LogoVersion != 0 {
		t.Fatalf("reset logo config = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, assetFileName(brandLogoAsset, replacementVersion))); !os.IsNotExist(err) {
		t.Fatalf("logo file remains after reset: %v", err)
	}
}

func TestBrandFaviconUploadServeAndReset(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	server := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}

	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	upload := httptest.NewRequest(http.MethodPut, "http://clipvault.local/api/v1/appearance/favicon", bytes.NewReader(imageData.Bytes()))
	upload.Header.Set("Content-Type", "image/png")
	uploadResult := httptest.NewRecorder()
	server.handleBrandFaviconUpload(uploadResult, upload)
	if uploadResult.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want %d: %s", uploadResult.Code, http.StatusOK, uploadResult.Body.String())
	}
	favicon := manager.Get().Brand
	if favicon.FaviconContentType != "image/png" || favicon.FaviconVersion <= 0 {
		t.Fatalf("uploaded favicon config = %#v", favicon)
	}

	serve := httptest.NewRecorder()
	server.handleBrandFavicon(serve, httptest.NewRequest(http.MethodGet, "http://clipvault.local/favicon.ico", nil))
	if serve.Code != http.StatusOK || serve.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("served favicon = status %d, content type %q", serve.Code, serve.Header().Get("Content-Type"))
	}
	if !bytes.Equal(serve.Body.Bytes(), imageData.Bytes()) {
		t.Fatal("served favicon differs from uploaded image")
	}

	reset := httptest.NewRecorder()
	server.handleBrandFaviconReset(reset, httptest.NewRequest(http.MethodDelete, "http://clipvault.local/api/v1/appearance/favicon", nil))
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want %d", reset.Code, http.StatusOK)
	}
	if got := manager.Get().Brand; got.FaviconContentType != "" || got.FaviconVersion != 0 {
		t.Fatalf("reset favicon config = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, assetFileName(brandFaviconAsset, favicon.FaviconVersion))); !os.IsNotExist(err) {
		t.Fatalf("favicon file remains after reset: %v", err)
	}
}

func TestBrandUploadKeepsAssetWhenRuntimeApplyFails(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	manager.OnChange(func(Config) error {
		return errors.New("appearance sync failed")
	})
	server := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}

	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, "http://clipvault.local/api/v1/appearance/logo", bytes.NewReader(imageData.Bytes()))
	request.Header.Set("Content-Type", "image/png")
	result := httptest.NewRecorder()
	server.handleBrandLogoUpload(result, request)
	if result.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want %d: %s", result.Code, http.StatusOK, result.Body.String())
	}
	var response struct {
		RuntimeError string `json:"runtimeError"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.RuntimeError != "appearance sync failed" {
		t.Fatalf("runtime error = %q", response.RuntimeError)
	}
	brand := manager.Get().Brand
	if _, err := os.Stat(filepath.Join(dir, assetFileName(brandLogoAsset, brand.LogoVersion))); err != nil {
		t.Fatalf("committed logo file is missing: %v", err)
	}
}

func TestBackgroundImageUploadServeRangeAndReset(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	server := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}

	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	upload := httptest.NewRequest(http.MethodPut, "http://clipvault.local/api/v1/appearance/background", bytes.NewReader(imageData.Bytes()))
	upload.Header.Set("Content-Type", "image/png")
	uploadResult := httptest.NewRecorder()
	server.handleBackgroundMediaUpload(uploadResult, upload)
	if uploadResult.Code != http.StatusOK {
		t.Fatalf("upload status = %d, want %d: %s", uploadResult.Code, http.StatusOK, uploadResult.Body.String())
	}
	background := manager.Get().Background
	if background.ContentType != "image/png" || background.Version <= 0 {
		t.Fatalf("uploaded background config = %#v", background)
	}
	backgroundFile := assetFileName(backgroundMediaAsset, background.Version)
	reloadedManager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("reload config manager: %v", err)
	}
	if got := reloadedManager.Get().Background; got.ContentType != "image/png" || got.Version != background.Version || got.Overlay != DefaultConfig().Background.Overlay {
		t.Fatalf("persisted background metadata = %#v", got)
	}

	serve := httptest.NewRecorder()
	rangeRequest := httptest.NewRequest(http.MethodGet, "http://clipvault.local/background/media", nil)
	rangeRequest.Header.Set("Range", "bytes=0-7")
	server.handleBackgroundMedia(serve, rangeRequest)
	if serve.Code != http.StatusPartialContent || serve.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("ranged background response = status %d, content type %q", serve.Code, serve.Header().Get("Content-Type"))
	}
	if !bytes.Equal(serve.Body.Bytes(), imageData.Bytes()[:8]) {
		t.Fatal("ranged background response has unexpected bytes")
	}

	var replacement bytes.Buffer
	replacementImage := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	replacementImage.Set(0, 0, color.NRGBA{B: 255, A: 255})
	if err := png.Encode(&replacement, replacementImage); err != nil {
		t.Fatalf("encode replacement PNG: %v", err)
	}
	replaceRequest := httptest.NewRequest(http.MethodPut, "http://clipvault.local/api/v1/appearance/background", bytes.NewReader(replacement.Bytes()))
	replaceRequest.Header.Set("Content-Type", "image/png")
	replaceResult := httptest.NewRecorder()
	server.handleBackgroundMediaUpload(replaceResult, replaceRequest)
	if replaceResult.Code != http.StatusOK {
		t.Fatalf("replacement upload status = %d, want %d", replaceResult.Code, http.StatusOK)
	}
	if _, err := os.Stat(filepath.Join(dir, backgroundFile)); !os.IsNotExist(err) {
		t.Fatalf("old background media remains after replacement: %v", err)
	}
	backgroundFile = assetFileName(backgroundMediaAsset, manager.Get().Background.Version)

	reset := httptest.NewRecorder()
	server.handleBackgroundMediaReset(reset, httptest.NewRequest(http.MethodDelete, "http://clipvault.local/api/v1/appearance/background", nil))
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want %d", reset.Code, http.StatusOK)
	}
	if got := manager.Get().Background; got.ContentType != "" || got.Version != 0 {
		t.Fatalf("reset background config = %#v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, backgroundFile)); !os.IsNotExist(err) {
		t.Fatalf("background media file remains after reset: %v", err)
	}
}

func TestBackgroundMediaValidation(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	if !isValidBackgroundMedia(pngData.Bytes(), "image/png") {
		t.Fatal("valid PNG background rejected")
	}
	validMP4 := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	if !isValidBackgroundMedia(validMP4, "video/mp4") {
		t.Fatal("valid MP4 container header rejected")
	}
	if isValidBackgroundMedia([]byte("not a video"), "video/mp4") {
		t.Fatal("invalid MP4 container header accepted")
	}
	validWebM := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F}
	if !isValidBackgroundMedia(validWebM, "video/webm") {
		t.Fatal("valid WebM container header rejected")
	}
	if isValidBackgroundMedia(validWebM, "video/mp4") {
		t.Fatal("WebM accepted as MP4")
	}
}

func TestBackgroundPNGUploadAcceptsGenericMIMEType(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	server := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/appearance/background", bytes.NewReader(pngData.Bytes()))
	req.Header.Set("Content-Type", "application/octet-stream")
	result := httptest.NewRecorder()
	server.handleBackgroundMediaUpload(result, req)
	if result.Code != http.StatusOK {
		t.Fatalf("generic MIME PNG upload status = %d, want %d: %s", result.Code, http.StatusOK, result.Body.String())
	}
	if got := manager.Get().Background.ContentType; got != "image/png" {
		t.Fatalf("detected background type = %q, want image/png", got)
	}
}

func TestPublicAppearanceContainsOnlyVisualSettings(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	cfg := manager.Get()
	cfg.Brand.Title = "Locked ClipVault"
	cfg.Theme.FontScale = 130
	lockHash, err := newPasswordHash("AppearancePass1")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Security.LockPasswordHash = lockHash
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("update config: %v", err)
	}
	server := &SearchServer{cfgMgr: manager}
	result := httptest.NewRecorder()
	server.handlePublicAppearance(result, httptest.NewRequest(http.MethodGet, "/api/v1/appearance", nil))
	if result.Code != http.StatusOK {
		t.Fatalf("appearance status = %d", result.Code)
	}
	if bytes.Contains(result.Body.Bytes(), []byte(lockHash)) || bytes.Contains(result.Body.Bytes(), []byte("security")) {
		t.Fatalf("public appearance exposes sensitive configuration: %s", result.Body.String())
	}
	var appearance struct {
		Brand BrandConfig `json:"brand"`
		Theme ThemeConfig `json:"theme"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &appearance); err != nil {
		t.Fatal(err)
	}
	if appearance.Brand.Title != cfg.Brand.Title || appearance.Theme.FontScale != 130 {
		t.Fatalf("appearance = %#v", appearance)
	}
}

func TestNormalizeBackground(t *testing.T) {
	background := BackgroundConfig{
		ContentType: "video/mp4", Version: 5, Opacity: 0, Overlay: 100, Blur: 60,
		Brightness: 20, Contrast: 200, Saturation: 250, Scale: 200, PositionX: -1, PositionY: 101,
	}
	normalizeBackground(&background, DefaultConfig().Background)
	if background.Opacity != 0 || background.Overlay != 90 || background.Blur != 32 ||
		background.Brightness != 40 || background.Contrast != 160 || background.Saturation != 200 ||
		background.Scale != 200 || background.PositionX != 0 || background.PositionY != 100 {
		t.Fatalf("background normalization = %#v", background)
	}
	background = BackgroundConfig{ContentType: "application/octet-stream", Version: 4}
	normalizeBackground(&background, DefaultConfig().Background)
	if background.ContentType != "" || background.Version != 0 {
		t.Fatalf("invalid background metadata retained: %#v", background)
	}
	background = BackgroundConfig{ContentType: "image/png"}
	normalizeBackground(&background, DefaultConfig().Background)
	if background.ContentType != "" || background.Version != 0 {
		t.Fatalf("background without a version retained metadata: %#v", background)
	}
}

func TestBrandUploadRejectsFilesOverTenMegabytes(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	server := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}

	body := bytes.Repeat([]byte{0}, maxBrandAssetBytes+1)
	req := httptest.NewRequest(http.MethodPut, "http://clipvault.local/api/v1/appearance/logo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "image/png")
	res := httptest.NewRecorder()
	server.handleBrandLogoUpload(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("oversized upload status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	files, err := filepath.Glob(filepath.Join(dir, brandLogoAsset+"-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("oversized upload left files behind: %v", files)
	}
}

func TestBrandEndpointsThroughHTTPServer(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	cfg := manager.Get()
	cfg.Brand.Title = "Locked ClipVault"
	cfg.Theme.FontScale = 130
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("update visual config: %v", err)
	}
	logger, err := NewDailyLogger(t.TempDir())
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	defer logger.Close()
	server := NewSearchServer(manager, store, NewRuntimeState(false), logger, Environment{DataDir: dir}, nil, nil)
	url, err := server.Start()
	if err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("close server: %v", err)
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}

	indexRes, err := client.Get(url + "settings/appearance")
	if err != nil {
		t.Fatalf("get index: %v", err)
	}
	indexData, readErr := io.ReadAll(indexRes.Body)
	indexRes.Body.Close()
	if readErr != nil {
		t.Fatalf("read index: %v", readErr)
	}
	if indexRes.StatusCode != http.StatusOK || !bytes.Contains(indexData, []byte("cfg-brandTitle")) {
		t.Fatalf("embedded index response = status %d, has brand controls = %t", indexRes.StatusCode, bytes.Contains(indexData, []byte("cfg-brandTitle")))
	}

	appearanceRes, err := client.Get(url + "api/v1/appearance")
	if err != nil {
		t.Fatalf("get public appearance: %v", err)
	}
	var appearance struct {
		Data struct {
			Brand BrandConfig `json:"brand"`
			Theme ThemeConfig `json:"theme"`
		} `json:"data"`
	}
	appearanceDecodeErr := json.NewDecoder(appearanceRes.Body).Decode(&appearance)
	appearanceRes.Body.Close()
	if appearanceRes.StatusCode != http.StatusOK || appearanceDecodeErr != nil || appearance.Data.Brand.Title != cfg.Brand.Title || appearance.Data.Theme.FontScale != cfg.Theme.FontScale {
		t.Fatalf("public appearance = status %d, value %#v, error %v", appearanceRes.StatusCode, appearance, appearanceDecodeErr)
	}

	styleRes, err := client.Get(url + "settings.css")
	if err != nil {
		t.Fatalf("get stylesheet: %v", err)
	}
	styleData, readErr := io.ReadAll(styleRes.Body)
	styleRes.Body.Close()
	if readErr != nil {
		t.Fatalf("read stylesheet: %v", readErr)
	}
	if styleRes.StatusCode != http.StatusOK || !bytes.Contains(styleData, []byte("brand-assets-row")) {
		t.Fatalf("embedded stylesheet response = status %d, has brand styles = %t", styleRes.StatusCode, bytes.Contains(styleData, []byte("brand-assets-row")))
	}

	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	blockedReq, err := http.NewRequest(http.MethodPut, url+"api/v1/appearance/logo", bytes.NewReader(imageData.Bytes()))
	if err != nil {
		t.Fatalf("new blocked request: %v", err)
	}
	blockedReq.Header.Set("Content-Type", "image/png")
	blockedRes, err := client.Do(blockedReq)
	if err != nil {
		t.Fatalf("send blocked request: %v", err)
	}
	blockedRes.Body.Close()
	if blockedRes.StatusCode != http.StatusForbidden {
		t.Fatalf("upload without CSRF status = %d, want %d", blockedRes.StatusCode, http.StatusForbidden)
	}

	csrfRes, err := client.Get(url + "api/v1/session/csrf")
	if err != nil {
		t.Fatalf("get csrf token: %v", err)
	}
	var csrf struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeErr := json.NewDecoder(csrfRes.Body).Decode(&csrf)
	csrfRes.Body.Close()
	if csrfRes.StatusCode != http.StatusOK || decodeErr != nil || csrf.Data.Token == "" {
		t.Fatalf("csrf response = status %d, token %q, error %v", csrfRes.StatusCode, csrf.Data.Token, decodeErr)
	}

	uploadReq, err := http.NewRequest(http.MethodPut, url+"api/v1/appearance/logo", bytes.NewReader(imageData.Bytes()))
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	uploadReq.Header.Set("Content-Type", "image/png")
	uploadReq.Header.Set(csrfHeaderName, csrf.Data.Token)
	uploadRes, err := client.Do(uploadReq)
	if err != nil {
		t.Fatalf("send upload request: %v", err)
	}
	uploadRes.Body.Close()
	if uploadRes.StatusCode != http.StatusOK {
		t.Fatalf("authenticated upload status = %d, want %d", uploadRes.StatusCode, http.StatusOK)
	}
	securedCfg := manager.Get()
	secondaryHash, err := newPasswordHash("BrandEndpointPass1")
	if err != nil {
		t.Fatal(err)
	}
	securedCfg.Security.SecondaryPasswordHash = secondaryHash
	if err := manager.Update(securedCfg); err != nil {
		t.Fatalf("set secondary password requirement: %v", err)
	}
	resetReq, err := http.NewRequest(http.MethodDelete, url+"api/v1/appearance/logo", nil)
	if err != nil {
		t.Fatalf("new secondary-protected request: %v", err)
	}
	resetReq.Header.Set(csrfHeaderName, csrf.Data.Token)
	resetRes, err := client.Do(resetReq)
	if err != nil {
		t.Fatalf("send secondary-protected request: %v", err)
	}
	resetRes.Body.Close()
	if resetRes.StatusCode != http.StatusForbidden {
		t.Fatalf("upload reset without secondary authentication status = %d, want %d", resetRes.StatusCode, http.StatusForbidden)
	}

	logoRes, err := client.Get(url + "brand/logo?v=" + strconv.FormatInt(manager.Get().Brand.LogoVersion, 10))
	if err != nil {
		t.Fatalf("get uploaded logo: %v", err)
	}
	logoData, readErr := io.ReadAll(logoRes.Body)
	logoRes.Body.Close()
	if readErr != nil {
		t.Fatalf("read uploaded logo: %v", readErr)
	}
	if logoRes.StatusCode != http.StatusOK || logoRes.Header.Get("Content-Type") != "image/png" || !bytes.Equal(logoData, imageData.Bytes()) {
		t.Fatalf("public logo response = status %d, content type %q, correct body = %t", logoRes.StatusCode, logoRes.Header.Get("Content-Type"), bytes.Equal(logoData, imageData.Bytes()))
	}
}

func TestNormalizeBrand(t *testing.T) {
	brand := BrandConfig{
		Title:              "  A very focused workspace  ",
		LogoContentType:    "image/png",
		LogoVersion:        10,
		FaviconContentType: "image/jpeg",
		FaviconVersion:     20,
	}
	normalizeBrand(&brand, DefaultConfig().Brand)
	if brand.Title != "A very focused workspace" {
		t.Fatalf("normalized title = %q", brand.Title)
	}
	if brand.LogoContentType != "image/png" || brand.LogoVersion != 10 {
		t.Fatalf("valid logo metadata changed: %#v", brand)
	}
	if brand.FaviconContentType != "" || brand.FaviconVersion != 0 {
		t.Fatalf("invalid favicon metadata retained: %#v", brand)
	}

	brand = BrandConfig{Title: "   ", LogoContentType: "text/plain", LogoVersion: 1}
	normalizeBrand(&brand, DefaultConfig().Brand)
	if brand.Title != "ClipVault" || brand.LogoContentType != "" || brand.LogoVersion != 0 {
		t.Fatalf("invalid brand did not normalize: %#v", brand)
	}

	brand = BrandConfig{Title: "ClipVault", LogoContentType: "image/png"}
	normalizeBrand(&brand, DefaultConfig().Brand)
	if brand.LogoContentType != "" || brand.LogoVersion != 0 {
		t.Fatalf("logo without a version retained metadata: %#v", brand)
	}
}

func TestAssetVersionMatches(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/brand/logo", nil)
	if assetVersionMatches(request, 0) {
		t.Fatal("unversioned request matched an absent asset")
	}
	if !assetVersionMatches(request, 10) {
		t.Fatal("unversioned request did not match the current asset")
	}
	request = httptest.NewRequest(http.MethodGet, "/brand/logo?v=10", nil)
	if !assetVersionMatches(request, 10) {
		t.Fatal("current versioned request did not match")
	}
}

func TestBrandImageValidation(t *testing.T) {
	var pngData bytes.Buffer
	if err := png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	if !isValidBrandImage(pngData.Bytes(), "image/png") {
		t.Fatal("valid PNG rejected")
	}
	if isValidBrandImage(pngData.Bytes(), "image/jpeg") {
		t.Fatal("PNG accepted as JPEG")
	}
	validICO := append([]byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0, 1, 0, 0, 0, 22, 0, 0, 0}, 0)
	if !isValidBrandImage(validICO, "image/x-icon") {
		t.Fatal("valid ICO structure rejected")
	}
	if isValidBrandImage([]byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0, 10, 0, 0, 0, 22, 0, 0, 0}, "image/x-icon") {
		t.Fatal("invalid ICO structure accepted")
	}
}

func TestBrandUploadRejectsMismatchedContentType(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	server := &SearchServer{cfgMgr: manager, env: Environment{DataDir: dir}}

	var imageData bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	if err := png.Encode(&imageData, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, "http://clipvault.local/api/v1/appearance/logo", bytes.NewReader(imageData.Bytes()))
	req.Header.Set("Content-Type", "image/jpeg")
	res := httptest.NewRecorder()
	server.handleBrandLogoUpload(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("mismatched content type status = %d, want %d", res.Code, http.StatusBadRequest)
	}
}
