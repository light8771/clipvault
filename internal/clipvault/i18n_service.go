package clipvault

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// I18nService 是运行时翻译服务：T() 只查内存缓存（O(1)，零 DB 访问），
// 缺失词条沿回退链解析（当前语言 → 自定义语言的 base → en-US → zh-CN →
// Key 原文）。DuckDB 是唯一真实数据源——语言编辑写库成功后才同步缓存。
type I18nService struct {
	store  *Store
	cfgMgr *ConfigManager
	logger *DailyLogger

	mu        sync.RWMutex
	current   string                  // 已解析的当前语言 code（运行期永不为 "auto"）
	languages map[string]I18nLanguage // code → 语言定义
	cache     map[string]map[string]string

	onChangeMu sync.Mutex
	onChange   []func()

	// missingOnce 去重记录缺失 Key/缺失参数的告警，避免日志刷屏。
	missingOnce sync.Map
}

// NewI18nService 构建服务并完成初始加载：
// 全量译文 → 注册 ConfigManager 监听（任何配置路径的语言变更都会同步到
// 服务，包括 PATCH /api/config 与事务性导入）→ 解析当前语言。
func NewI18nService(store *Store, cfgMgr *ConfigManager, logger *DailyLogger) (*I18nService, error) {
	s := &I18nService{store: store, cfgMgr: cfgMgr, logger: logger}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	if cfgMgr != nil {
		cfgMgr.OnChange(func(cfg Config) error {
			s.applyConfigLanguage(cfg.UI.Language)
			return nil
		})
	}
	s.applyConfigLanguage(s.configuredLanguage())
	return s, nil
}

// configuredLanguage 读取配置中的语言设置（可能为 "auto"）。
func (s *I18nService) configuredLanguage() string {
	if s.cfgMgr != nil {
		return s.cfgMgr.Get().UI.Language
	}
	return I18nAuto
}

// applyConfigLanguage 将配置值解析为具体语言并切换。
// 解析规则："auto" → 系统语言；不存在/已禁用 → en-US 兜底（告警，
// 不回写配置——避免与配置写入方形成循环）。
func (s *I18nService) applyConfigLanguage(configured string) {
	resolved := configured
	if resolved == I18nAuto || resolved == "" {
		resolved = detectSystemLanguage()
	}
	s.mu.RLock()
	lang, ok := s.languages[resolved]
	s.mu.RUnlock()
	if !ok || !lang.IsEnabled {
		s.logMissing(fmt.Sprintf("language %q unavailable, falling back to %s", resolved, I18nEnglish))
		resolved = I18nEnglish
	}
	s.mu.Lock()
	changed := s.current != resolved
	s.current = resolved
	s.mu.Unlock()
	if changed {
		s.fireLanguageChanged()
	}
}

// Reload 全量重建语言定义与译文缓存（启动、语言增删后调用）。
func (s *I18nService) Reload() error {
	ctx := context.Background()
	languages, err := s.store.ListI18nLanguages(ctx)
	if err != nil {
		return fmt.Errorf("load i18n languages: %w", err)
	}
	cache, err := s.store.LoadI18nTranslations(ctx)
	if err != nil {
		return fmt.Errorf("load i18n translations: %w", err)
	}
	byCode := make(map[string]I18nLanguage, len(languages))
	for _, lang := range languages {
		byCode[lang.Code] = lang
	}
	s.mu.Lock()
	s.languages = byCode
	s.cache = cache
	s.mu.Unlock()
	return nil
}

// CurrentLanguage 返回已解析的当前语言 code（永不为 "auto"）。
func (s *I18nService) CurrentLanguage() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// SetLanguage 切换当前语言：校验取值（"auto" 或已启用的语言）→ 持久化到
// config.ui.language。服务状态与原生 UI 钩子由已注册的 OnChange 监听器
// 统一驱动，保证与 PATCH /api/config 路径行为一致。
func (s *I18nService) SetLanguage(code string) error {
	code = strings.TrimSpace(code)
	if code != I18nAuto {
		s.mu.RLock()
		lang, ok := s.languages[code]
		s.mu.RUnlock()
		if !ok {
			return fmt.Errorf("%w: %q", ErrI18nLanguageNotFound, code)
		}
		if !lang.IsEnabled {
			return fmt.Errorf("i18n language %q is disabled", code)
		}
	}
	if s.cfgMgr != nil {
		before := s.cfgMgr.Get()
		cfg := cloneConfig(before)
		cfg.UI.Language = code
		return s.cfgMgr.UpdateFrom(before, cfg)
	}
	// 无 ConfigManager（仅测试）：直接切换。
	s.applyConfigLanguage(code)
	return nil
}

// OnLanguageChanged 注册语言切换钩子（托盘刷新、原生窗口重绘等）。
// 钩子在切换提交后同步执行；单个钩子 panic 不得影响其他钩子。
func (s *I18nService) OnLanguageChanged(fn func()) {
	s.onChangeMu.Lock()
	defer s.onChangeMu.Unlock()
	s.onChange = append(s.onChange, fn)
}

func (s *I18nService) fireLanguageChanged() {
	s.onChangeMu.Lock()
	hooks := append([]func(){}, s.onChange...)
	s.onChangeMu.Unlock()
	for _, fn := range hooks {
		func() {
			defer func() { _ = recover() }()
			fn()
		}()
	}
}

// T 翻译 Key 并渲染 {name} 参数。缺失词条回退到 Key 原文（Debug 告警）；
// 缺失参数保留占位符（Debug 告警）。永不 panic、永不返回空串。
func (s *I18nService) T(key string, params map[string]any) string {
	s.mu.RLock()
	text, ok := s.resolveLocked(key)
	s.mu.RUnlock()
	if !ok {
		s.logMissing("i18n key missing: " + key)
		return key
	}
	return renderI18nParams(key, text, params, func(name string) {
		s.logMissing(fmt.Sprintf("i18n param %q missing for key %q", name, key))
	})
}

// resolveLocked 沿回退链解析 Key；调用方须持有读锁或写锁。
func (s *I18nService) resolveLocked(key string) (string, bool) {
	if dict, ok := s.cache[s.current]; ok {
		if v, ok := dict[key]; ok {
			return v, true
		}
	}
	// 自定义语言的基准语言（官方语言跳过此级——其 base 仅供编辑页参考）。
	if lang, ok := s.languages[s.current]; ok && !lang.IsBuiltin {
		if dict, ok := s.cache[lang.BaseLanguageCode]; ok {
			if v, ok := dict[key]; ok {
				return v, true
			}
		}
	}
	if s.current != I18nEnglish {
		if dict, ok := s.cache[I18nEnglish]; ok {
			if v, ok := dict[key]; ok {
				return v, true
			}
		}
	}
	if dict, ok := s.cache[I18nChinese]; ok {
		if v, ok := dict[key]; ok {
			return v, true
		}
	}
	return "", false
}

// renderI18nParams 将 {name} 占位符替换为参数值。缺失参数保留占位符原文
// 并经 onMissing 回调（可为 nil）。key 仅用于回调语义。
func renderI18nParams(key, text string, params map[string]any, onMissing func(name string)) string {
	if !strings.ContainsRune(text, '{') || len(params) == 0 {
		return text
	}
	var sb strings.Builder
	rest := text
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			sb.WriteString(rest)
			return sb.String()
		}
		sb.WriteString(rest[:open])
		closeIdx := strings.IndexByte(rest[open:], '}')
		if closeIdx < 0 {
			sb.WriteString(rest[open:])
			return sb.String()
		}
		name := rest[open+1 : open+closeIdx]
		if value, ok := params[name]; ok {
			sb.WriteString(formatI18nParam(value))
		} else {
			sb.WriteString(rest[open : open+closeIdx+1])
			if onMissing != nil {
				onMissing(name)
			}
		}
		rest = rest[open+closeIdx+1:]
	}
}

// formatI18nParam 参数值转字符串：整数直书（避免 strconv 科学计数），
// 其余走 fmt.Sprint。
func formatI18nParam(value any) string {
	switch v := value.(type) {
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// ─── 编辑同步（写库成功后由 TranslationService 调用）────────────────────

// ApplyTranslationSaved 将单条已保存译文同步进缓存（DB 写成功后调用）。
func (s *I18nService) ApplyTranslationSaved(languageCode, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache[languageCode] == nil {
		s.cache[languageCode] = make(map[string]string)
	}
	s.cache[languageCode][key] = value
}

// ApplyTranslationDeleted 将单条已删除译文移出缓存（DB 删除成功后调用）。
func (s *I18nService) ApplyTranslationDeleted(languageCode, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dict := s.cache[languageCode]; dict != nil {
		delete(dict, key)
	}
}

// EffectiveDictionary 返回前端字典：按回退链合成当前语言的完整有效
// 字典（zh-CN 基底 ← en-US ← base ← 当前语言逐层覆盖），前端 t() 为
// 纯查表，无需理解回退逻辑。
func (s *I18nService) EffectiveDictionary() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	effective := make(map[string]string, len(s.cache[I18nChinese]))
	for _, code := range []string{I18nChinese, I18nEnglish} {
		for k, v := range s.cache[code] {
			effective[k] = v
		}
	}
	if lang, ok := s.languages[s.current]; ok && !lang.IsBuiltin {
		for k, v := range s.cache[lang.BaseLanguageCode] {
			effective[k] = v
		}
	}
	for k, v := range s.cache[s.current] {
		effective[k] = v
	}
	return effective
}

// Languages 返回语言定义快照（语言管理页数据源）。
func (s *I18nService) Languages() map[string]I18nLanguage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	languages := make(map[string]I18nLanguage, len(s.languages))
	for code, lang := range s.languages {
		languages[code] = lang
	}
	return languages
}

// logMissing 记录缺失告警（每个 Key 只记一次，防日志刷屏）。
func (s *I18nService) logMissing(message string) {
	if _, loaded := s.missingOnce.LoadOrStore(message, struct{}{}); loaded {
		return
	}
	if s.logger != nil {
		s.logger.Warn("i18n_missing", map[string]any{"detail": message})
	}
}
