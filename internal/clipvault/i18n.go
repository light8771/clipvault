package clipvault

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 官方内置语言与配置取值。自定义语言的基准语言只能在这两种官方语言中
// 选择，因此 fallback 环在构造上不可能，无需运行时循环检测。
const (
	I18nChinese = "zh-CN"
	I18nEnglish = "en-US"
	// I18nAuto 是 config.ui.language 的“跟随系统”取值：启动时按 Windows
	// 系统 UI 语言映射为 zh-CN 或 en-US；用户显式选择后写入具体 code。
	I18nAuto = "auto"
)

// I18nEditorStatus 是翻译编辑页的状态筛选。
const (
	I18nEditorStatusAll          = ""
	I18nEditorStatusTranslated   = "translated"
	I18nEditorStatusUntranslated = "untranslated"
	I18nEditorStatusRecent       = "recent"
)

const (
	maxI18nLanguageCodeRunes = 16
	maxI18nLanguageNameRunes = 64
	// MaxI18nTranslationBytes 单条译文上限（字节）。UI 文案远用不满，
	// 主要是防止异常输入把词条表当成大文本存储。
	MaxI18nTranslationBytes = 4096
	// DefaultI18nEditorPageSize 翻译编辑页每页条数。词条可达 2000+，
	// 不做一次性渲染。
	DefaultI18nEditorPageSize = 100
	maxI18nEditorPageSize     = 500
	// i18nUpsertChunk 批量 upsert 的单条 SQL 行数上限（Key/译文 Seed 与
	// 编辑保存共用），避免超长 SQL。
	i18nUpsertChunk = 400
)

var (
	ErrI18nLanguageNotFound    = errors.New("i18n language not found")
	ErrI18nLanguageExists      = errors.New("i18n language code already exists")
	ErrI18nBuiltinReadonly     = errors.New("builtin i18n language is readonly")
	ErrI18nInvalidLanguageCode = errors.New("invalid i18n language code")
	ErrI18nInvalidLanguageName = errors.New("invalid i18n language name")
	ErrI18nInvalidBaseLanguage = errors.New("i18n base language must be a builtin language")
	ErrI18nTranslationTooLarge = errors.New("i18n translation exceeds size limit")
	ErrI18nEmptyTranslation    = errors.New("i18n translation must not be empty")
)

// i18nLanguageCodePattern BCP 47 风格基础限制：字母/数字段以 - 连接，
// 如 en、zh-CN、ja-JP、zh-Hans-CN。
var i18nLanguageCodePattern = regexp.MustCompile(`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`)

// I18nLanguage 是一种 UI 语言的定义。官方语言（zh-CN/en-US）只读；
// 自定义语言可编辑/删除，code 创建后锁定。
type I18nLanguage struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	NativeName       string `json:"nativeName"`
	BaseLanguageCode string `json:"baseLanguageCode"`
	IsBuiltin        bool   `json:"isBuiltin"`
	IsEnabled        bool   `json:"isEnabled"`
}

// I18nCounts 是某语言的翻译完成度：已翻译数 / 全部 active Key 数。
type I18nCounts struct {
	Total      int `json:"total"`
	Translated int `json:"translated"`
}

// I18nLanguageSummary 语言列表项：定义 + 完成度。
type I18nLanguageSummary struct {
	I18nLanguage
	I18nCounts
}

// I18nKeyDef 官方词条 Key 的定义（Seed 与存储层共用的最小结构）。
type I18nKeyDef struct {
	Key           string
	Module        string
	Description   string
	SourceVersion int
}

// I18nEditorEntry 翻译编辑页的一行：Key 元数据 + 基准文本 + 自定义译文。
type I18nEditorEntry struct {
	Key         string `json:"key"`
	Module      string `json:"module"`
	Description string `json:"description"`
	SourceText  string `json:"sourceText"`
	CustomText  string `json:"customText"`
}

// I18nEditorFilter 编辑页查询条件。BaseLanguageCode 由服务层按目标语言的
// base 解析后传入（自定义语言取其 base，官方语言取另一官方语言）。
type I18nEditorFilter struct {
	LanguageCode     string
	BaseLanguageCode string
	Query            string // 匹配 Key / 基准文本 / 自定义译文（不区分大小写）
	Module           string // 为空表示全部模块
	Status           string // "" | translated | untranslated | recent
	RecentFrom       int    // Status=recent 时：source_version >= RecentFrom
	Page             int    // 1-based
	PageSize         int    // 默认 DefaultI18nEditorPageSize
}

func (f *I18nEditorFilter) normalize() {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = DefaultI18nEditorPageSize
	}
	if f.PageSize > maxI18nEditorPageSize {
		f.PageSize = maxI18nEditorPageSize
	}
	f.Query = strings.TrimSpace(f.Query)
	f.Module = strings.TrimSpace(f.Module)
	f.Status = strings.TrimSpace(f.Status)
}

// isI18nBuiltinLanguageCode 报告 code 是否为官方语言。
func isI18nBuiltinLanguageCode(code string) bool {
	return code == I18nChinese || code == I18nEnglish
}

// validateI18nLanguageCode 校验语言代码（BCP 47 风格基础限制）。
func validateI18nLanguageCode(code string) error {
	if code == "" {
		return fmt.Errorf("%w: code must not be empty", ErrI18nInvalidLanguageCode)
	}
	if utf8.RuneCountInString(code) > maxI18nLanguageCodeRunes {
		return fmt.Errorf("%w: code exceeds %d characters", ErrI18nInvalidLanguageCode, maxI18nLanguageCodeRunes)
	}
	if !i18nLanguageCodePattern.MatchString(code) {
		return fmt.Errorf("%w: %q", ErrI18nInvalidLanguageCode, code)
	}
	return nil
}

// validateI18nLanguageName 校验语言名称（英文名与本地名共用）。
func validateI18nLanguageName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: name must not be empty", ErrI18nInvalidLanguageName)
	}
	if utf8.RuneCountInString(name) > maxI18nLanguageNameRunes {
		return fmt.Errorf("%w: name exceeds %d characters", ErrI18nInvalidLanguageName, maxI18nLanguageNameRunes)
	}
	return nil
}

// validateI18nBaseLanguage 基准语言只能是官方语言（防 fallback 环）。
func validateI18nBaseLanguage(code string) error {
	if !isI18nBuiltinLanguageCode(code) {
		return fmt.Errorf("%w: %q", ErrI18nInvalidBaseLanguage, code)
	}
	return nil
}

// validateI18nTranslationValue 译文必须非空且不超限。空白即删除的语义由
// 服务层处理（TrimSpace 后为空 → DeleteI18nTranslation）。
func validateI18nTranslationValue(value string) error {
	if value == "" {
		return ErrI18nEmptyTranslation
	}
	if len(value) > MaxI18nTranslationBytes {
		return fmt.Errorf("%w: %d bytes > %d", ErrI18nTranslationTooLarge, len(value), MaxI18nTranslationBytes)
	}
	return nil
}
