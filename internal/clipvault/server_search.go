package clipvault

import (
	"clipboard_ext/internal/licensing"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *SearchServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	limit, offset, sourceExeNames, err := parseSearchParameters(r)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_search_parameters", s.i18nT("api.error.invalid_search_parameters", nil), err)
		return
	}
	groupID, err := parseOptionalPositiveInt64(r.URL.Query().Get("group_id"), "group_id")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_id", s.i18nT("api.error.invalid_group_id", nil), err)
		return
	}
	cfg := s.cfgMgr.Get()
	token := GetSessionToken(r)
	secondaryReadable := !s.auth.HasSecondaryPassword(cfg) || s.auth.HasSecondaryAuth(token)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("kind")
	starredOnly, err := parseOptionalFlag(r.URL.Query().Get("starred"), "starred")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_starred_filter", s.i18nT("api.error.invalid_starred_filter", nil), err)
		return
	}
	dateFrom := r.URL.Query().Get("date_from")
	dateTo := r.URL.Query().Get("date_to")
	createdFromMS, createdBeforeMS, err := localDateFilterBounds(dateFrom, dateTo, s.configuredTimeZone())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_date_range", s.i18nT("api.error.invalid_date_range", nil), err)
		return
	}
	hasContentOracleFilter := query != "" || kind != "" || starredOnly || dateFrom != "" || dateTo != "" || groupID != 0 || len(sourceExeNames) != 0
	opts := SearchOptions{
		Query:           query,
		Limit:           limit,
		Offset:          offset,
		StarredOnly:     starredOnly,
		Kind:            kind,
		CreatedFromMS:   createdFromMS,
		CreatedBeforeMS: createdBeforeMS,

		IncludeSensitive: secondaryReadable || !hasContentOracleFilter,
		GroupID:          groupID,
		SourceExeNames:   sourceExeNames,
		ExeAliases:       cfg.Source.ExeAliases,
	}
	resp := map[string]any{}

	if s.license != nil && !s.license.CanUsePaidFeatures() {
		opts.RestrictToLatestN = FreeTierVisibleLimit
		if ft, err := s.store.CountAll(r.Context()); err == nil {
			resp["fullTotal"] = ft
		}
	}
	items, total, err := s.store.Search(r.Context(), opts)
	if err != nil {
		s.writeError(w, err)
		return
	}

	if s.auth.HasSecondaryPassword(cfg) && !s.auth.HasSecondaryAuth(token) {
		for i := range items {
			if !items[i].Sensitive {
				continue
			}
			// 未过二次认证时，敏感条目只保留渲染占位卡片所必需的字段
			// （ID、时间、类型、收藏、敏感标志）。其余元数据——来源应用、
			// 窗口、路径、字数、哈希、重复标记——全部清除：它们是
			// 旁信道（例如"来自密码管理器的 24 字符记录"本身即泄露）。
			items[i].Text = s.i18nT("api.error.sensitive_placeholder", nil)
			items[i].MetaJSON = ""
			items[i].SHA256 = ""
			items[i].Format = ""
			items[i].ByteLen = 0
			items[i].CharLen = 0
			items[i].SourceExeName = ""
			items[i].SourceExePath = ""
			items[i].SourceWindowTitle = ""
			items[i].SourceMethod = ""
			items[i].IsDuplicate = false
			items[i].UnavailableReason = ""
			items[i].ContentAvailable = true
		}
	}
	resp["items"] = items
	resp["total"] = total
	if query != "" && s.store != nil {
		if err := s.store.LogSearchQuery(r.Context(), query,
			searchHistoryFilters(kind, starredOnly, groupID, dateFrom, dateTo, sourceExeNames), int64(total)); err != nil {
			s.logger.Warn("search_history_log_failed", map[string]any{"error": err.Error()})
		}
	}
	s.writeJSON(w, resp)
}

// searchHistoryFilters 把搜索条件汇总为「标签 key → 值 key/原始值」的
// 展示用 map，与搜索历史记录一起落库。key 是 shlog.filter.* 词条 key，
// 由前端展示时翻译——与操作记录同理由：持久化数据不落语言相关文案。
func searchHistoryFilters(kind string, starredOnly bool, groupID int64, dateFrom, dateTo string, sourceExeNames []string) map[string]string {
	filters := map[string]string{}
	switch kind {
	case "text":
		filters["shlog.filter.kind"] = "history.kind_text"
	case "image":
		filters["shlog.filter.kind"] = "history.kind_image"
	case "":
	default:
		filters["shlog.filter.kind"] = kind
	}
	if starredOnly {
		filters["shlog.filter.starred"] = "shlog.filter.starred_only"
	}
	if groupID != 0 {
		filters["shlog.filter.group"] = strconv.FormatInt(groupID, 10)
	}
	if dateFrom != "" || dateTo != "" {
		filters["shlog.filter.date"] = dateFrom + " ~ " + dateTo
	}
	if len(sourceExeNames) > 0 {
		filters["shlog.filter.source_apps"] = strings.Join(sourceExeNames, ", ")
	}
	return filters
}

// handleSourceExes 返回所有来源应用（原始 exe 名）及其记录数，供搜索下拉与别名/白名单编辑使用。
func (s *SearchServer) handleSourceExes(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	includeSensitive := !s.auth.HasSecondaryPassword(cfg) || s.auth.HasSecondaryAuth(GetSessionToken(r))
	restrictToLatestN := 0
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		restrictToLatestN = FreeTierVisibleLimit
	}
	stats, err := s.store.SourceExeStatsFiltered(r.Context(), includeSensitive, restrictToLatestN)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if stats == nil {
		stats = []ExeStat{}
	}
	s.writeJSON(w, stats)
}

// maxSourceExeFilters 限制来源应用多选过滤参数的条数，防止构造超长 IN 列表。
const maxSourceExeFilters = 50

const (
	maxSearchQueryBytes    = 4096
	maxSourceExeFilterSize = 260
)

func parseSearchParameters(r *http.Request) (limit, offset int, sourceExeNames []string, err error) {
	query := r.URL.Query().Get("q")
	if len(query) > maxSearchQueryBytes {
		return 0, 0, nil, fmt.Errorf("q exceeds %d bytes", maxSearchQueryBytes)
	}
	if _, err := splitSearchTerms(query); err != nil {
		return 0, 0, nil, err
	}
	limit, err = parseOptionalInt(r.URL.Query().Get("limit"), "limit", 1, 500)
	if err != nil {
		return 0, 0, nil, err
	}
	offset, err = parseOptionalInt(r.URL.Query().Get("offset"), "offset", 0, 0)
	if err != nil {
		return 0, 0, nil, err
	}
	sourceExeNames = r.URL.Query()["source_exe"]
	if len(sourceExeNames) > maxSourceExeFilters {
		return 0, 0, nil, fmt.Errorf("source_exe exceeds %d values", maxSourceExeFilters)
	}
	for _, name := range sourceExeNames {
		if name == "" || len(name) > maxSourceExeFilterSize {
			return 0, 0, nil, fmt.Errorf("invalid source_exe value")
		}
	}
	return limit, offset, sourceExeNames, nil
}

func parseOptionalInt(raw, name string, minValue, maxValue int) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || (maxValue > 0 && value > maxValue) {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func parseOptionalPositiveInt64(raw, name string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func parseOptionalFlag(raw, name string) (bool, error) {
	switch raw {
	case "", "0":
		return false, nil
	case "1":
		return true, nil
	default:
		return false, fmt.Errorf("invalid %s", name)
	}
}

// handleSensitiveSearch 仅返回敏感记录，需要二次认证。
func (s *SearchServer) handleSensitiveSearch(w http.ResponseWriter, r *http.Request) {
	limit, offset, sourceExeNames, err := parseSearchParameters(r)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_search_parameters", s.i18nT("api.error.invalid_search_parameters", nil), err)
		return
	}
	cfg := s.cfgMgr.Get()
	groupID, err := parseOptionalPositiveInt64(r.URL.Query().Get("group_id"), "group_id")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_id", s.i18nT("api.error.invalid_group_id", nil), err)
		return
	}
	starredOnly, err := parseOptionalFlag(r.URL.Query().Get("starred"), "starred")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_starred_filter", s.i18nT("api.error.invalid_starred_filter", nil), err)
		return
	}
	createdFromMS, createdBeforeMS, err := localDateFilterBounds(r.URL.Query().Get("date_from"), r.URL.Query().Get("date_to"), s.configuredTimeZone())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_date_range", s.i18nT("api.error.invalid_date_range", nil), err)
		return
	}
	opts := SearchOptions{
		Query:           r.URL.Query().Get("q"),
		Limit:           limit,
		Offset:          offset,
		Kind:            r.URL.Query().Get("kind"),
		StarredOnly:     starredOnly,
		CreatedFromMS:   createdFromMS,
		CreatedBeforeMS: createdBeforeMS,
		SensitiveOnly:   true,
		GroupID:         groupID,

		SourceExeNames: sourceExeNames,
		ExeAliases:     cfg.Source.ExeAliases,
	}
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		opts.RestrictToLatestN = FreeTierVisibleLimit
	}
	items, total, err := s.store.Search(r.Context(), opts)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"items": items, "total": total})
}

// handleSensitiveItem 返回单条敏感记录的明文，需要二次认证。
func (s *SearchServer) handleSensitiveItem(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
		return
	}
	item, err := s.store.GetByID(r.Context(), id)
	if err != nil {
		s.writeItemLookupError(w, err)
		return
	}
	if !item.Sensitive {
		s.writeAPIError(w, http.StatusBadRequest, "not_sensitive", s.i18nT("api.error.not_sensitive", nil), nil)
		return
	}

	if s.license != nil && !s.license.CanUsePaidFeatures() {
		pos, perr := s.store.GetPositionByID(r.Context(), id)
		if perr != nil {
			s.writeError(w, perr)
			return
		}
		if pos >= FreeTierVisibleLimit {
			s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
				s.i18nT("api.error.free_window_read", map[string]any{"n": FreeTierVisibleLimit}), nil)
			return
		}
	}
	s.writeJSON(w, map[string]any{"item": item})
}

func (s *SearchServer) handleItemPosition(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_id", nil), nil)
		return
	}
	item, err := s.store.GetByID(r.Context(), id)
	if err != nil {
		s.writeItemLookupError(w, err)
		return
	}
	if item.Sensitive && !s.authorizeSecondaryRequest(w, r) {
		return
	}
	pos, err := s.store.GetPositionByID(r.Context(), id)
	if err != nil {
		s.writeItemLookupError(w, err)
		return
	}

	if s.license != nil && !s.license.CanUsePaidFeatures() && pos >= FreeTierVisibleLimit {
		s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
			s.i18nT("api.error.free_window_read", map[string]any{"n": FreeTierVisibleLimit}), nil)
		return
	}
	s.writeJSON(w, map[string]any{"position": pos})
}

// enforceFreeWindow 免费版（未激活）写操作门控：与读窗口语义一致，
// 窗口外（最近 FreeTierVisibleLimit 条之外）的记录不允许删除/收藏等写操作。
// 返回 true 表示已拦截并写出 402 响应，调用方应直接 return。
// 与 handleSensitiveItem/handleItemPosition 的读窗口门控共用 GetPositionByID 判定。
func (s *SearchServer) enforceFreeWindow(w http.ResponseWriter, r *http.Request, id int64) bool {
	if s.license != nil && !s.license.CanUsePaidFeatures() {
		pos, perr := s.store.GetPositionByID(r.Context(), id)
		if perr != nil {
			s.writeItemLookupError(w, perr)
			return true
		}
		if pos >= FreeTierVisibleLimit {
			s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
				s.i18nT("api.error.free_window_write", map[string]any{"n": FreeTierVisibleLimit}), nil)
			return true
		}
	}
	return false
}

// FreeTierRestriction 返回免费版（未激活）可见窗口限制是否生效：未激活用户只能
// 浏览/检索最近 FreeTierVisibleLimit 条记录。CLI（cmd/clipvault/main.go 的 search）
// 通过本函数判定，与 Web handler 基于 license.CanUsePaidFeatures 的判定等价，
// 避免 CLI 各自硬编码而绕过窗口。receipt 路径与 app_windows.go 的 license 加载一致：
// <DataDir>/license/clipvault.lic。注意：本函数会从磁盘重新加载 license 状态。
func FreeTierRestriction(env Environment) bool {
	receiptPath := filepath.Join(env.DataDir, "license", "clipvault.lic")
	lm := licensing.NewManager(receiptPath, licensing.BuiltinTrustedKeys())
	lm.Load()
	return !lm.CanUsePaidFeatures()
}
