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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_search_parameters", "搜索参数无效", err)
		return
	}
	groupID, err := parseOptionalPositiveInt64(r.URL.Query().Get("group_id"), "group_id")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_id", "分组 id 无效", err)
		return
	}
	cfg := s.cfgMgr.Get()
	token := GetSessionToken(r)
	secondaryReadable := !s.auth.HasSecondaryPassword(cfg) || s.auth.HasSecondaryAuth(token)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("kind")
	starredOnly, err := parseOptionalFlag(r.URL.Query().Get("starred"), "starred")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_starred_filter", "收藏筛选参数无效", err)
		return
	}
	dateFrom := r.URL.Query().Get("date_from")
	dateTo := r.URL.Query().Get("date_to")
	createdFrom, createdBefore, err := localDateFilterBounds(r.URL.Query().Get("date_from"), r.URL.Query().Get("date_to"), s.configuredTimeZone())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_date_range", "日期范围无效", err)
		return
	}
	hasContentOracleFilter := query != "" || kind != "" || starredOnly || dateFrom != "" || dateTo != "" || groupID != 0 || len(sourceExeNames) != 0
	opts := SearchOptions{
		Query:         query,
		Limit:         limit,
		Offset:        offset,
		StarredOnly:   starredOnly,
		Kind:          kind,
		CreatedFrom:   createdFrom,
		CreatedBefore: createdBefore,

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
			if items[i].Sensitive {
				items[i].Text = "【敏感内容，点击查看】"

				items[i].MetaJSON = ""
				items[i].SHA256 = ""
				items[i].Format = ""
				items[i].ByteLen = 0
				items[i].SourceWindowTitle = ""
				items[i].SourceExePath = ""
			}
		}
	}
	resp["items"] = items
	resp["total"] = total
	s.writeJSON(w, resp)
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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_search_parameters", "搜索参数无效", err)
		return
	}
	cfg := s.cfgMgr.Get()
	groupID, err := parseOptionalPositiveInt64(r.URL.Query().Get("group_id"), "group_id")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_group_id", "分组 id 无效", err)
		return
	}
	starredOnly, err := parseOptionalFlag(r.URL.Query().Get("starred"), "starred")
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_starred_filter", "收藏筛选参数无效", err)
		return
	}
	createdFrom, createdBefore, err := localDateFilterBounds(r.URL.Query().Get("date_from"), r.URL.Query().Get("date_to"), s.configuredTimeZone())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_date_range", "日期范围无效", err)
		return
	}
	opts := SearchOptions{
		Query:         r.URL.Query().Get("q"),
		Limit:         limit,
		Offset:        offset,
		Kind:          r.URL.Query().Get("kind"),
		StarredOnly:   starredOnly,
		CreatedFrom:   createdFrom,
		CreatedBefore: createdBefore,
		SensitiveOnly: true,
		GroupID:       groupID,

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
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 id", nil)
		return
	}
	item, err := s.store.GetByID(r.Context(), id)
	if err != nil {
		s.writeItemLookupError(w, err)
		return
	}
	if !item.Sensitive {
		s.writeAPIError(w, http.StatusBadRequest, "not_sensitive", "该记录不是敏感内容", nil)
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
				"未激活仅可查看最近 "+strconv.Itoa(FreeTierVisibleLimit)+" 条记录，该记录不在可查看范围内", nil)
			return
		}
	}
	s.writeJSON(w, map[string]any{"item": item})
}

func (s *SearchServer) handleItemPosition(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 id", nil)
		return
	}
	pos, err := s.store.GetPositionByID(r.Context(), id)
	if err != nil {
		s.writeItemLookupError(w, err)
		return
	}

	if s.license != nil && !s.license.CanUsePaidFeatures() && pos >= FreeTierVisibleLimit {
		s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
			"未激活仅可查看最近 "+strconv.Itoa(FreeTierVisibleLimit)+" 条记录，该记录不在可查看范围内", nil)
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
				"未激活仅可操作最近 "+strconv.Itoa(FreeTierVisibleLimit)+" 条记录，该记录不在可操作范围内", nil)
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
