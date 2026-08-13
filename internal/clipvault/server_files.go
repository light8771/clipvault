package clipvault

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// handleFileCheck checks if a file exists and returns its metadata.
func (s *SearchServer) handleFileCheck(w http.ResponseWriter, r *http.Request) {
	p, sensitive, err := s.resolveRecordedFile(r)
	if err != nil {
		s.writeFileRefError(w, err)
		return
	}
	if sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	info, err := os.Stat(p)
	resp := map[string]any{"exists": false}
	if err == nil && !info.IsDir() {
		resp["exists"] = true
		resp["size"] = info.Size()
		resp["name"] = filepath.Base(p)
		resp["ext"] = strings.ToLower(filepath.Ext(p))
	}
	s.writeJSON(w, resp)
}

// handleFileServe serves a file for inline viewing or download.
func (s *SearchServer) handleFileServe(w http.ResponseWriter, r *http.Request) {
	p, sensitive, err := s.resolveRecordedFile(r)
	if err != nil {
		s.writeFileRefError(w, err)
		return
	}
	if sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		s.writeAPIError(w, http.StatusNotFound, "file_not_found", "文件不存在", nil)
		return
	}
	if r.URL.Query().Get("download") == "1" || !safeInlineImageExt(filepath.Ext(p)) {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	}
	http.ServeFile(w, r, p)
}

// handleFileHash computes and returns the MD5 and SHA256 hashes of a file on demand.
func (s *SearchServer) handleFileHash(w http.ResponseWriter, r *http.Request) {
	p, sensitive, err := s.resolveRecordedFile(r)
	if err != nil {
		s.writeFileRefError(w, err)
		return
	}
	if sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		s.writeAPIError(w, http.StatusNotFound, "file_not_found", "文件不存在", nil)
		return
	}
	f, err := os.Open(p)
	if err != nil {

		s.writeAPIError(w, http.StatusInternalServerError, "file_open_failed", "无法读取文件", err)
		return
	}
	defer f.Close()
	m := md5.New()
	sh := sha256.New()
	if _, err := io.Copy(io.MultiWriter(m, sh), f); err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "file_read_failed", "读取文件失败", err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "md5": hex.EncodeToString(m.Sum(nil)), "sha256": hex.EncodeToString(sh.Sum(nil))})
}

// handleFileOpenDir opens the file's containing directory in the system file explorer.
func (s *SearchServer) handleFileOpenDir(w http.ResponseWriter, r *http.Request) {
	p, sensitive, err := s.resolveRecordedFile(r)
	if err != nil {
		s.writeFileRefError(w, err)
		return
	}
	if sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	if err := openFolderAndSelectItem(p); err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// ErrFreeWindowOutside 表示请求的文件所属记录在免费版可见窗口之外。
// 调用方据此返回 402（与 enforceFreeWindow / handleItemPosition 语义一致，#2b）。
var ErrFreeWindowOutside = errors.New("file record is outside the free-tier visible window")

// ErrFileRefLookup 表示 resolveRecordedFile 在存储层/IO 查询（GetPositionByID、GetByID）
// 失败时返回。调用方据此返回 5xx（与 enforceFreeWindow / handleItemPosition 对同类错误的处理
// 对齐），避免把 DuckDB 查询故障误报成 400 客户端参数错误。原始错误通过 %v 保留在链中便于日志。
var ErrFileRefLookup = errors.New("file reference lookup failed")

// resolveRecordedFile 返回路径以及该记录是否为敏感记录。敏感属性必须和
// 路径在同一次记录读取中取得，避免授权检查与实际读取之间发生错配。
func (s *SearchServer) resolveRecordedFile(r *http.Request) (string, bool, error) {
	itemID, err := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	if err != nil || itemID <= 0 {
		return "", false, fmt.Errorf("invalid item_id")
	}
	fileIndex, err := strconv.Atoi(r.URL.Query().Get("file_index"))
	if err != nil || fileIndex < 0 {
		return "", false, fmt.Errorf("invalid file_index")
	}
	item, err := s.store.GetByID(r.Context(), itemID)
	if errors.Is(err, ErrItemNotFound) {
		return "", false, fmt.Errorf("item not found")
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: load item: %v", ErrFileRefLookup, err)
	}

	if s.license != nil && !s.license.CanUsePaidFeatures() {
		pos, perr := s.store.GetPositionByID(r.Context(), itemID)
		if perr != nil {
			return "", false, fmt.Errorf("%w: resolve position: %v", ErrFileRefLookup, perr)
		}
		if pos >= FreeTierVisibleLimit {
			return "", false, ErrFreeWindowOutside
		}
	}
	if item.Format != "CF_HDROP" || !isStoredFileKind(item.Kind) {
		return "", false, fmt.Errorf("item is not a file record")
	}
	lines := strings.Split(strings.ReplaceAll(item.Text, "\r\n", "\n"), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			paths = append(paths, line)
		}
	}
	if fileIndex >= len(paths) {
		return "", false, fmt.Errorf("file_index out of range")
	}
	return paths[fileIndex], item.Sensitive, nil
}

func isStoredFileKind(kind string) bool {
	switch kind {
	case "image", "video", "archive", "office", "files":
		return true
	default:
		return false
	}
}

func safeInlineImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".ico", ".tiff", ".tif", ".avif":
		return true
	default:
		return false
	}
}
