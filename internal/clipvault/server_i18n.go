package clipvault

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// SetI18nService 注入运行时翻译服务（对齐 SetDataAPIService/SetMCPService
// 注入模式；服务由应用启动时构建并跨 Web 服务重启复用）。
func (s *SearchServer) SetI18nService(svc *I18nService) {
	s.i18n = svc
}

// i18nT 服务端消息翻译：错误响应的 userMsg 与动态文案统一走当前语言。
func (s *SearchServer) i18nT(key string, params map[string]any) string {
	if svc := s.i18n; svc != nil {
		return svc.T(key, params)
	}
	return templateT(key, params)
}

// handleI18nDictJS 动态生成前端字典脚本（模式同 handleThemeBootCSS）：
// 服务端按回退链合成当前语言的完整有效字典，输出为外部 JS 资源——
// 满足 CSP script-src 'self' 且在 bootstrap 模块之前同步可用，前端 t()
// 为纯查表。页面响应头为 no-cache，语言切换后整页刷新即拿到新字典。
func (s *SearchServer) handleI18nDictJS(w http.ResponseWriter, _ *http.Request) {
	var lang string
	dict := make(map[string]string)
	if svc := s.i18n; svc != nil {
		lang = svc.CurrentLanguage()
		dict = svc.EffectiveDictionary()
	} else {
		lang = I18nChinese
	}
	payload, err := json.Marshal(map[string]any{"lang": lang, "dict": dict})
	if err != nil {
		s.writeAPIError(w, http.StatusInternalServerError, "i18n_dict_invalid", "i18n dictionary encode failed", err)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte("window.__clipvaultI18n = "))
	_, _ = w.Write(payload)
	_, _ = w.Write([]byte(";\n"))
}

// ─── 语言管理 API ──────────────────────────────────────────────────────────

// handleI18nLanguagesGet 返回语言列表（含完成度）与当前语言设置。
func (s *SearchServer) handleI18nLanguagesGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	languages, err := s.store.ListI18nLanguages(ctx)
	if err != nil {
		s.writeError(w, err)
		return
	}
	completions, err := s.store.I18nCompletions(ctx)
	if err != nil {
		s.writeError(w, err)
		return
	}
	summaries := make([]I18nLanguageSummary, 0, len(languages))
	for _, lang := range languages {
		summaries = append(summaries, I18nLanguageSummary{I18nLanguage: lang, I18nCounts: completions[lang.Code]})
	}
	configured := I18nAuto
	if s.cfgMgr != nil {
		configured = s.cfgMgr.Get().UI.Language
	}
	current := configured
	if s.i18n != nil {
		current = s.i18n.CurrentLanguage()
	}
	s.writeJSON(w, map[string]any{
		"languages":       summaries,
		"currentLanguage": current,
		"configuredValue": configured,
	})
}

// handleI18nLanguagesCreate 创建自定义语言。
func (s *SearchServer) handleI18nLanguagesCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code             string `json:"code"`
		Name             string `json:"name"`
		NativeName       string `json:"nativeName"`
		BaseLanguageCode string `json:"baseLanguageCode"`
	}
	if err := decodeJSONRequest(w, r, &req, 8<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("common.error", nil), err)
		return
	}
	lang := I18nLanguage{
		Code:             strings.TrimSpace(req.Code),
		Name:             strings.TrimSpace(req.Name),
		NativeName:       strings.TrimSpace(req.NativeName),
		BaseLanguageCode: strings.TrimSpace(req.BaseLanguageCode),
		IsEnabled:        true,
	}
	switch {
	case validateI18nLanguageCode(lang.Code) != nil:
		s.writeAPIError(w, http.StatusBadRequest, "i18n_code_invalid", s.i18nT("i18n.error.code_invalid", nil), nil)
		return
	case validateI18nLanguageName(lang.Name) != nil:
		s.writeAPIError(w, http.StatusBadRequest, "i18n_name_invalid", s.i18nT("i18n.error.name_invalid", nil), nil)
		return
	case isI18nBuiltinLanguageCode(lang.Code):
		// 官方 code 不可被用户重建（与唯一性分开提示）。
		s.writeAPIError(w, http.StatusBadRequest, "i18n_code_exists", s.i18nT("i18n.error.code_exists", nil), nil)
		return
	case validateI18nBaseLanguage(lang.BaseLanguageCode) != nil:
		s.writeAPIError(w, http.StatusBadRequest, "i18n_base_invalid", s.i18nT("i18n.error.base_invalid", nil), nil)
		return
	}
	if lang.NativeName == "" {
		lang.NativeName = lang.Name
	}
	if err := s.store.CreateI18nLanguage(r.Context(), lang); err != nil {
		if errors.Is(err, ErrI18nLanguageExists) {
			s.writeAPIError(w, http.StatusBadRequest, "i18n_code_exists", s.i18nT("i18n.error.code_exists", nil), err)
			return
		}
		s.writeError(w, err)
		return
	}
	if s.i18n != nil {
		if err := s.i18n.Reload(); err != nil {
			s.writeError(w, err)
			return
		}
	}
	if s.logger != nil {
		s.logger.Info("i18n_language_created", map[string]any{"code": lang.Code, "base": lang.BaseLanguageCode})
	}
	s.writeJSON(w, map[string]any{"ok": true, "language": lang})
}

// handleI18nLanguagesUpdate 更新自定义语言（名称/本地名/基准语言/启用态）。
func (s *SearchServer) handleI18nLanguagesUpdate(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		s.writeAPIError(w, http.StatusBadRequest, "i18n_code_missing", s.i18nT("i18n.error.invalid_code_param", nil), nil)
		return
	}
	var req struct {
		Name             string `json:"name"`
		NativeName       string `json:"nativeName"`
		BaseLanguageCode string `json:"baseLanguageCode"`
		IsEnabled        *bool  `json:"isEnabled"`
	}
	if err := decodeJSONRequest(w, r, &req, 8<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("common.error", nil), err)
		return
	}
	current, err := s.store.GetI18nLanguage(r.Context(), code)
	if err != nil {
		if errors.Is(err, ErrI18nLanguageNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "i18n_not_found", s.i18nT("i18n.error.not_found", nil), err)
			return
		}
		s.writeError(w, err)
		return
	}
	if current.IsBuiltin {
		s.writeAPIError(w, http.StatusForbidden, "i18n_builtin_readonly", s.i18nT("i18n.error.builtin_readonly", nil), nil)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = current.Name
	}
	if validateI18nLanguageName(name) != nil {
		s.writeAPIError(w, http.StatusBadRequest, "i18n_name_invalid", s.i18nT("i18n.error.name_invalid", nil), nil)
		return
	}
	nativeName := strings.TrimSpace(req.NativeName)
	if nativeName == "" {
		nativeName = current.NativeName
	}
	base := strings.TrimSpace(req.BaseLanguageCode)
	if base == "" {
		base = current.BaseLanguageCode
	}
	if validateI18nBaseLanguage(base) != nil {
		s.writeAPIError(w, http.StatusBadRequest, "i18n_base_invalid", s.i18nT("i18n.error.base_invalid", nil), nil)
		return
	}
	enabled := current.IsEnabled
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}
	if err := s.store.UpdateI18nLanguage(r.Context(), code, name, nativeName, base, enabled); err != nil {
		s.writeError(w, err)
		return
	}
	if s.i18n != nil {
		if err := s.i18n.Reload(); err != nil {
			s.writeError(w, err)
			return
		}
	}
	if s.logger != nil {
		s.logger.Info("i18n_language_updated", map[string]any{"code": code, "enabled": enabled})
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleI18nLanguagesDelete 删除自定义语言；删除的是当前语言时先切到
// en-US（统一国际 fallback），再执行级联删除。
func (s *SearchServer) handleI18nLanguagesDelete(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		s.writeAPIError(w, http.StatusBadRequest, "i18n_code_missing", s.i18nT("i18n.error.invalid_code_param", nil), nil)
		return
	}
	lang, err := s.store.GetI18nLanguage(r.Context(), code)
	if err != nil {
		if errors.Is(err, ErrI18nLanguageNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "i18n_not_found", s.i18nT("i18n.error.not_found", nil), err)
			return
		}
		s.writeError(w, err)
		return
	}
	if lang.IsBuiltin {
		s.writeAPIError(w, http.StatusForbidden, "i18n_builtin_readonly", s.i18nT("i18n.error.builtin_readonly", nil), nil)
		return
	}
	if s.i18n != nil && s.i18n.CurrentLanguage() == code && s.cfgMgr != nil {
		if err := s.i18n.SetLanguage(I18nEnglish); err != nil {
			s.writeError(w, err)
			return
		}
	}
	if err := s.store.DeleteI18nLanguage(r.Context(), code); err != nil {
		s.writeError(w, err)
		return
	}
	if s.i18n != nil {
		if err := s.i18n.Reload(); err != nil {
			s.writeError(w, err)
			return
		}
	}
	if s.logger != nil {
		s.logger.Info("i18n_language_deleted", map[string]any{"code": code})
	}
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleI18nCurrentLanguagePut 切换当前界面语言（显示偏好，不需二次认证）。
func (s *SearchServer) handleI18nCurrentLanguagePut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSONRequest(w, r, &req, 1<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("common.error", nil), err)
		return
	}
	if s.i18n == nil {
		s.writeAPIError(w, http.StatusServiceUnavailable, "i18n_unavailable", s.i18nT("common.error", nil), nil)
		return
	}
	code := strings.TrimSpace(req.Code)
	if err := s.i18n.SetLanguage(code); err != nil {
		switch {
		case errors.Is(err, ErrI18nLanguageNotFound):
			s.writeAPIError(w, http.StatusNotFound, "i18n_not_found", s.i18nT("i18n.error.not_found", nil), err)
		case strings.Contains(err.Error(), "disabled"):
			s.writeAPIError(w, http.StatusBadRequest, "i18n_language_disabled", s.i18nT("i18n.error.language_disabled", nil), err)
		default:
			s.writeError(w, err)
		}
		return
	}
	if s.logger != nil {
		s.logger.Info("i18n_language_switched", map[string]any{"code": code})
	}
	s.writeJSON(w, map[string]any{"ok": true, "currentLanguage": s.i18n.CurrentLanguage()})
}

// ─── 翻译编辑 API ──────────────────────────────────────────────────────────

// handleI18nEntriesGet 返回编辑页词条数据（搜索/筛选/分页 + 完成度）。
func (s *SearchServer) handleI18nEntriesGet(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	langCode := strings.TrimSpace(query.Get("lang"))
	lang, err := s.store.GetI18nLanguage(r.Context(), langCode)
	if err != nil {
		if errors.Is(err, ErrI18nLanguageNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "i18n_not_found", s.i18nT("i18n.error.not_found", nil), err)
			return
		}
		s.writeError(w, err)
		return
	}
	// 基准语言：自定义语言取其 base；官方语言互为参考（另一官方语言）。
	base := lang.BaseLanguageCode
	if lang.IsBuiltin {
		if lang.Code == I18nChinese {
			base = I18nEnglish
		} else {
			base = I18nChinese
		}
	}
	baseName := base
	if languages, err := s.store.ListI18nLanguages(r.Context()); err == nil {
		for _, candidate := range languages {
			if candidate.Code == base {
				baseName = candidate.Name
				if candidate.NativeName != "" && candidate.NativeName != candidate.Name {
					baseName += " / " + candidate.NativeName
				}
				break
			}
		}
	}
	status := strings.TrimSpace(query.Get("status"))
	switch status {
	case I18nEditorStatusAll, I18nEditorStatusTranslated, I18nEditorStatusUntranslated, I18nEditorStatusRecent:
	default:
		s.writeAPIError(w, http.StatusBadRequest, "i18n_invalid_status", s.i18nT("i18n.error.invalid_status", nil), nil)
		return
	}
	page, _ := strconv.Atoi(strings.TrimSpace(query.Get("page")))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(strings.TrimSpace(query.Get("pageSize")))
	filter := I18nEditorFilter{
		LanguageCode:     langCode,
		BaseLanguageCode: base,
		Query:            query.Get("q"),
		Module:           strings.TrimSpace(query.Get("module")),
		Status:           status,
		RecentFrom:       I18nSchemaVersion,
		Page:             page,
		PageSize:         pageSize,
	}
	filter.normalize()
	entries, total, err := s.store.ListI18nEditorEntries(r.Context(), filter)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if entries == nil {
		entries = []I18nEditorEntry{}
	}
	completions, err := s.store.I18nCompletions(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	counts := completions[langCode]
	s.writeJSON(w, map[string]any{
		"entries":    entries,
		"total":      total,
		"page":       filter.Page,
		"pageSize":   filter.PageSize,
		"language":   lang,
		"baseCode":   base,
		"baseName":   baseName,
		"counts":     counts,
		"modules":    mustListI18nModules(r, s.store),
		"canEdit":    !lang.IsBuiltin,
		"recentFrom": I18nSchemaVersion,
	})
}

func mustListI18nModules(r *http.Request, store *Store) []string {
	modules, err := store.ListI18nModules(r.Context())
	if err != nil || modules == nil {
		return []string{}
	}
	return modules
}

// handleI18nEntriesPut 保存单条译文：空白（TrimSpace 后）即删除（空翻译
// 语义 → 运行时 fallback）；官方语言只读；写库成功后同步内存缓存。
func (s *SearchServer) handleI18nEntriesPut(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	langCode := strings.TrimSpace(query.Get("lang"))
	key := strings.TrimSpace(query.Get("key"))
	if langCode == "" || key == "" {
		s.writeAPIError(w, http.StatusBadRequest, "i18n_params_missing", s.i18nT("i18n.error.invalid_code_param", nil), nil)
		return
	}
	lang, err := s.store.GetI18nLanguage(r.Context(), langCode)
	if err != nil {
		if errors.Is(err, ErrI18nLanguageNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "i18n_not_found", s.i18nT("i18n.error.not_found", nil), err)
			return
		}
		s.writeError(w, err)
		return
	}
	if lang.IsBuiltin {
		s.writeAPIError(w, http.StatusForbidden, "i18n_builtin_readonly", s.i18nT("i18n.error.builtin_readonly", nil), nil)
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := decodeJSONRequest(w, r, &req, 16<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("common.error", nil), err)
		return
	}
	value := strings.TrimSpace(req.Value)
	if value == "" {
		if err := s.store.DeleteI18nTranslation(r.Context(), langCode, key); err != nil {
			s.writeError(w, err)
			return
		}
		if s.i18n != nil {
			s.i18n.ApplyTranslationDeleted(langCode, key)
		}
		s.writeJSON(w, map[string]any{"ok": true, "deleted": true})
		return
	}
	if err := validateI18nTranslationValue(value); err != nil {
		if errors.Is(err, ErrI18nTranslationTooLarge) {
			s.writeAPIError(w, http.StatusBadRequest, "i18n_value_too_large", s.i18nT("i18n.error.value_too_large", nil), err)
			return
		}
		s.writeAPIError(w, http.StatusBadRequest, "i18n_value_invalid", s.i18nT("common.error", nil), err)
		return
	}
	if err := s.store.SaveI18nTranslation(r.Context(), langCode, key, value); err != nil {
		s.writeError(w, err)
		return
	}
	if s.i18n != nil {
		s.i18n.ApplyTranslationSaved(langCode, key, value)
	}
	s.writeJSON(w, map[string]any{"ok": true})
}
