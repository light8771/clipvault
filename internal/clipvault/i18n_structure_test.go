package clipvault

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// TestI18nKeysAreDefined 硬门禁：web 源码中所有词条引用（HTML 的
// {{T "..."}}、JS 的 t('...')）必须存在于官方词条表 BuiltinTranslations，
// 防止悬空 Key 导致 UI 显示 Key 原文。无白名单。
func TestI18nKeysAreDefined(t *testing.T) {
	defined := make(map[string]struct{}, len(BuiltinTranslations))
	for _, entry := range BuiltinTranslations {
		defined[entry.Key] = struct{}{}
	}
	templateKeyPattern := regexp.MustCompile(`\{\{-?\s*T\s+"([^"]+)"`)
	jsKeyPattern := regexp.MustCompile(`\bt\(\s*['"]([a-z0-9_.]+)['"]`)
	// recovery.js 使用自带的本地字典（恢复服务器场景无 i18n 服务），
	// 其 key 不在官方词条表内，跳过词条覆盖校验。
	skipKeyCoverage := map[string]struct{}{"web/recovery.js": {}}
	err := fs.WalkDir(webFS, "web", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := path.Ext(name)
		if ext != ".html" && ext != ".js" {
			return nil
		}
		data, readErr := fs.ReadFile(webFS, name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		text := string(data)
		for _, pattern := range []*regexp.Regexp{templateKeyPattern, jsKeyPattern} {
			for _, match := range pattern.FindAllStringSubmatch(text, -1) {
				key := match[1]
				// 以下划线结尾的是动态 key 前缀（运行时拼接状态后缀，
				// 如 channel.token_state_ + state），由 Key 级联词条覆盖。
				if strings.HasSuffix(key, "_") {
					continue
				}
				if _, ok := defined[key]; !ok {
					if _, skipped := skipKeyCoverage[name]; skipped {
						continue
					}
					t.Errorf("%s references undefined i18n key %q", strings.TrimPrefix(name, "web/"), key)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// i18nWebChineseAllowlist 是仅有的允许出现中文的 web 源文件：
// recovery.js 内嵌恢复页双语字典（恢复服务器无 i18n 服务，无法走词条）。
var i18nWebChineseAllowlist = map[string]struct{}{
	"web/recovery.js": {},
}

// TestWebSourcesHaveNoHardcodedChinese 硬门禁：web 源码的用户可见文案
// 不得出现硬编码中文（注释除外——代码注释是开发者可见内容；允许清单
// 中的文件除外，见 i18nWebChineseAllowlist 注释）。
func TestWebSourcesHaveNoHardcodedChinese(t *testing.T) {
	chinesePattern := regexp.MustCompile(`[\p{Han}]`)
	htmlCommentPattern := regexp.MustCompile(`(?s)<!--.*?-->`)
	// 行首或空白后的 // 到行尾均视为注释（URL 中紧贴冒号的 // 不受影响）。
	jsLineCommentPattern := regexp.MustCompile(`(?m)(^|[\s\p{Zs}])//[^\n]*$`)
	jsBlockCommentPattern := regexp.MustCompile(`(?s)/\*.*?\*/`)
	err := fs.WalkDir(webFS, "web", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := path.Ext(name)
		if ext != ".html" && ext != ".js" {
			return nil
		}
		if strings.Contains(name, "_test") || name == "web/__layout_check.html" {
			return nil
		}
		data, readErr := fs.ReadFile(webFS, name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		text := string(data)
		switch ext {
		case ".html":
			text = htmlCommentPattern.ReplaceAllString(text, "")
		case ".js":
			text = jsBlockCommentPattern.ReplaceAllString(text, "")
			text = jsLineCommentPattern.ReplaceAllString(text, "")
		}
		if chinesePattern.MatchString(text) {
			if _, allowed := i18nWebChineseAllowlist[name]; allowed {
				return nil
			}
			t.Errorf("%s contains hardcoded Chinese outside comments; extract to i18n keys", strings.TrimPrefix(name, "web/"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestI18nBuiltinTranslationsAreWellFormed 词条表自身的质量门禁：
// Key 命名规范（module.path 全小写）、双语完整、{name} 占位符中英成对。
func TestI18nBuiltinTranslationsAreWellFormed(t *testing.T) {
	seen := make(map[string]struct{}, len(BuiltinTranslations))
	keyPattern := regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)
	for _, entry := range BuiltinTranslations {
		if !keyPattern.MatchString(entry.Key) {
			t.Errorf("key %q violates module.path naming (lowercase)", entry.Key)
		}
		if entry.ZhCN == "" || entry.EnUS == "" {
			t.Errorf("key %q must define both zh-CN and en-US", entry.Key)
		}
		if _, duplicate := seen[entry.Key]; duplicate {
			t.Errorf("duplicate key %q", entry.Key)
		}
		seen[entry.Key] = struct{}{}
		zhParams := placeholderNames(entry.ZhCN)
		enParams := placeholderNames(entry.EnUS)
		// 占位符集合一致即可：中英语序不同是合法翻译形态。
		if !sameElements(zhParams, enParams) {
			t.Errorf("key %q placeholder mismatch: zh=%v en=%v", entry.Key, zhParams, enParams)
		}
		if !unicode.IsLetter(rune(entry.Key[0])) {
			t.Errorf("key %q must start with a letter", entry.Key)
		}
	}
}

func placeholderNames(text string) []string {
	pattern := regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)
	var names []string
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		names = append(names, match[1])
	}
	return names
}

// sameElements 报告两个切片是否含相同元素（含重复计数，忽略顺序）。
func sameElements(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
		if counts[v] < 0 {
			return false
		}
	}
	return true
}
