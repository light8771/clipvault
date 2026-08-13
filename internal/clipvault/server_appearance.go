package clipvault

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *SearchServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveApplicationPage(w)
}

const maxBrandAssetBytes = 10 << 20

func (s *SearchServer) handleBrandLogo(w http.ResponseWriter, r *http.Request) {
	s.serveBrandAsset(w, r, "logo")
}

func (s *SearchServer) handleBrandFavicon(w http.ResponseWriter, r *http.Request) {
	s.serveBrandAsset(w, r, "favicon")
}

func (s *SearchServer) handleBackgroundMedia(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get().Background
	if !isBackgroundContentType(cfg.ContentType) || !assetVersionMatches(r, cfg.Version) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	fileName := assetFileName(backgroundMediaAsset, cfg.Version)
	file, err := os.Open(filepath.Join(s.env.DataDir, fileName))
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", cfg.ContentType)
	w.Header().Set("Cache-Control", assetCacheControl(r))
	http.ServeContent(w, r, fileName, time.Time{}, file)
}

func (s *SearchServer) serveBrandAsset(w http.ResponseWriter, r *http.Request, kind string) {
	cfg := s.cfgMgr.Get().Brand
	contentType := cfg.LogoContentType
	assetKind := brandLogoAsset
	version := cfg.LogoVersion
	if kind == "favicon" {
		contentType = cfg.FaviconContentType
		assetKind = brandFaviconAsset
		version = cfg.FaviconVersion
	}
	if !isBrandContentType(contentType, kind) || !assetVersionMatches(r, version) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.env.DataDir, assetFileName(assetKind, version)))
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", assetCacheControl(r))
	_, _ = w.Write(data)
}

func (s *SearchServer) handleBrandLogoUpload(w http.ResponseWriter, r *http.Request) {
	s.handleBrandAssetUpload(w, r, "logo")
}

func (s *SearchServer) handleBrandFaviconUpload(w http.ResponseWriter, r *http.Request) {
	s.handleBrandAssetUpload(w, r, "favicon")
}

func (s *SearchServer) handleBackgroundMediaUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBrandAssetBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_media", "无效的背景媒体", nil)
		return
	}
	contentType := detectBackgroundContentType(data)
	if contentType == "" {
		s.writeAPIError(w, http.StatusBadRequest, "unsupported_media", "不支持的背景媒体格式", nil)
		return
	}
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	cfg := s.cfgMgr.Get()
	previousVersion := cfg.Background.Version
	version := nextAssetVersion(previousVersion)
	fileName := assetFileName(backgroundMediaAsset, version)
	if err := writeBrandAsset(filepath.Join(s.env.DataDir, fileName), data); err != nil {
		s.writeError(w, err)
		return
	}
	cfg.Background.ContentType = contentType
	cfg.Background.Version = version
	runtimeError, hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		_ = os.Remove(filepath.Join(s.env.DataDir, fileName))
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(backgroundMediaAsset, previousVersion)))
	}
	response := map[string]any{"ok": true, "background": s.cfgMgr.Get().Background}
	addRuntimeWarnings(response, runtimeError, hotkeyError)
	s.writeJSON(w, response)
}

func (s *SearchServer) handleBrandAssetUpload(w http.ResponseWriter, r *http.Request, kind string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBrandAssetBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_image", "无效的图片", nil)
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType == "image/vnd.microsoft.icon" {
		contentType = "image/x-icon"
	}
	if !isBrandContentType(contentType, kind) || !isValidBrandImage(data, contentType) {
		s.writeAPIError(w, http.StatusBadRequest, "unsupported_image", "不支持的图片格式", nil)
		return
	}
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	cfg := s.cfgMgr.Get()
	assetKind := brandLogoAsset
	previousVersion := cfg.Brand.LogoVersion
	if kind == "favicon" {
		assetKind = brandFaviconAsset
		previousVersion = cfg.Brand.FaviconVersion
	}
	version := nextAssetVersion(previousVersion)
	fileName := assetFileName(assetKind, version)
	if err := writeBrandAsset(filepath.Join(s.env.DataDir, fileName), data); err != nil {
		s.writeError(w, err)
		return
	}
	if kind == "favicon" {
		cfg.Brand.FaviconContentType = contentType
		cfg.Brand.FaviconVersion = version
	} else {
		cfg.Brand.LogoContentType = contentType
		cfg.Brand.LogoVersion = version
	}
	runtimeError, hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		_ = os.Remove(filepath.Join(s.env.DataDir, fileName))
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(assetKind, previousVersion)))
	}
	response := map[string]any{"ok": true, "brand": s.cfgMgr.Get().Brand}
	addRuntimeWarnings(response, runtimeError, hotkeyError)
	s.writeJSON(w, response)
}

func (s *SearchServer) handleBrandLogoReset(w http.ResponseWriter, r *http.Request) {
	s.handleBrandAssetReset(w, "logo")
}

func (s *SearchServer) handleBrandFaviconReset(w http.ResponseWriter, r *http.Request) {
	s.handleBrandAssetReset(w, "favicon")
}

func (s *SearchServer) handleBackgroundMediaReset(w http.ResponseWriter, r *http.Request) {
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	cfg := s.cfgMgr.Get()
	previousVersion := cfg.Background.Version
	cfg.Background.ContentType = ""
	cfg.Background.Version = 0
	runtimeError, hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(backgroundMediaAsset, previousVersion)))
	}
	response := map[string]any{"ok": true, "background": s.cfgMgr.Get().Background}
	addRuntimeWarnings(response, runtimeError, hotkeyError)
	s.writeJSON(w, response)
}

func (s *SearchServer) handleBrandAssetReset(w http.ResponseWriter, kind string) {
	s.assetMu.Lock()
	defer s.assetMu.Unlock()
	cfg := s.cfgMgr.Get()
	assetKind := brandLogoAsset
	previousVersion := cfg.Brand.LogoVersion
	if kind == "favicon" {
		assetKind = brandFaviconAsset
		previousVersion = cfg.Brand.FaviconVersion
		cfg.Brand.FaviconContentType = ""
		cfg.Brand.FaviconVersion = 0
	} else {
		cfg.Brand.LogoContentType = ""
		cfg.Brand.LogoVersion = 0
	}
	runtimeError, hotkeyError, err := s.updateAssetConfig(cfg)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if previousVersion > 0 {
		_ = os.Remove(filepath.Join(s.env.DataDir, assetFileName(assetKind, previousVersion)))
	}
	response := map[string]any{"ok": true, "brand": s.cfgMgr.Get().Brand}
	addRuntimeWarnings(response, runtimeError, hotkeyError)
	s.writeJSON(w, response)
}

// updateAssetConfig distinguishes persistence failures from post-commit runtime
// listener failures so callers never delete an asset referenced by saved config.
func (s *SearchServer) updateAssetConfig(cfg Config) (runtimeError, hotkeyError string, err error) {
	err = s.cfgMgr.Update(cfg)
	if err == nil {
		return "", "", nil
	}
	var applyError *ConfigRuntimeApplyError
	if !errors.As(err, &applyError) {
		return "", "", err
	}
	var hkErr *HotkeyError
	if errors.As(applyError, &hkErr) {
		hotkeyError = hkErr.Error()
	}
	return applyError.Error(), hotkeyError, nil
}

func addRuntimeWarnings(response map[string]any, runtimeError, hotkeyError string) {
	if runtimeError != "" {
		response["runtimeError"] = runtimeError
	}
	if hotkeyError != "" {
		response["hotkeyError"] = hotkeyError
	}
}

func isValidBrandImage(data []byte, contentType string) bool {
	if contentType == "image/x-icon" {
		return isValidICO(data)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 4096 || config.Height > 4096 {
		return false
	}
	return (contentType == "image/png" && format == "png") || (contentType == "image/jpeg" && format == "jpeg")
}

func isValidBackgroundMedia(data []byte, contentType string) bool {
	return detectBackgroundContentType(data) == contentType
}

// detectBackgroundContentType trusts the media bytes rather than a browser's
// optional MIME hint. Some Windows file pickers report PNG files as generic
// application/octet-stream even when the file itself is valid.
func detectBackgroundContentType(data []byte) string {
	if config, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil && config.Width > 0 && config.Height > 0 && config.Width <= 4096 && config.Height <= 4096 {
		switch format {
		case "png":
			return "image/png"
		case "jpeg":
			return "image/jpeg"
		}
	}
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		return "video/mp4"
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		return "video/webm"
	}
	return ""
}

func isValidICO(data []byte) bool {
	if len(data) < 22 || !bytes.Equal(data[:4], []byte{0, 0, 1, 0}) {
		return false
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || len(data) < 6+count*16 {
		return false
	}
	for index := 0; index < count; index++ {
		entry := data[6+index*16 : 22+index*16]
		size := int(binary.LittleEndian.Uint32(entry[8:12]))
		offset := int(binary.LittleEndian.Uint32(entry[12:16]))
		if size > 0 && offset >= 6+count*16 && offset <= len(data)-size {
			return true
		}
	}
	return false
}

func writeBrandAsset(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".brand-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func nextAssetVersion(previousVersion int64) int64 {
	next := time.Now().UnixNano()
	if next <= previousVersion {
		return previousVersion + 1
	}
	return next
}
