package clipvault

import (
	"net/http"
)

// 日志接口的默认/最大分页大小。
const (
	defaultLogPageSize = 20
	maxLogPageSize     = 100
)

func parseLogPage(r *http.Request) (limit, offset int, err error) {
	limit, err = parseOptionalInt(r.URL.Query().Get("limit"), "limit", 1, maxLogPageSize)
	if err != nil {
		return 0, 0, err
	}
	if limit == 0 {
		limit = defaultLogPageSize
	}
	offset, err = parseOptionalInt(r.URL.Query().Get("offset"), "offset", 0, 0)
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

// handleConfigChangeLog 返回系统设置修改记录（含新旧值），按时间倒序分页。
func (s *SearchServer) handleConfigChangeLog(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLogPage(r)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_log_page", "分页参数无效", err)
		return
	}
	entries, total, err := s.store.ListConfigChanges(r.Context(), limit, offset)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"entries": entries, "total": total})
}

// handleConfigChangeLogClear 清空设置修改记录，需要二次认证。
func (s *SearchServer) handleConfigChangeLogClear(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearConfigChanges(r.Context()); err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("config_change_log_cleared", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleSearchHistory 返回剪贴板搜索历史，按时间倒序分页。
func (s *SearchServer) handleSearchHistory(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := parseLogPage(r)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_log_page", "分页参数无效", err)
		return
	}
	entries, total, err := s.store.ListSearchHistory(r.Context(), limit, offset)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"entries": entries, "total": total})
}

// handleSearchHistoryClear 清空搜索历史，需要二次认证。
func (s *SearchServer) handleSearchHistoryClear(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearSearchHistory(r.Context()); err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("search_history_cleared", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}
