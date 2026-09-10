package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestBuiltinTranslationResourceHasCompleteRecords(t *testing.T) {
	decoder := json.NewDecoder(bytes.NewReader(builtinTranslationsJSON))
	decoder.DisallowUnknownFields()
	var entries []BuiltinTranslation
	if err := decoder.Decode(&entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("embedded translation resource is empty")
	}
	for _, entry := range entries {
		for field, value := range map[string]string{"Key": entry.Key, "Module": entry.Module, "Description": entry.Description, "ZhCN": entry.ZhCN, "EnUS": entry.EnUS} {
			if strings.TrimSpace(value) == "" {
				t.Errorf("translation %q has empty %s", entry.Key, field)
			}
		}
	}
}

func TestI18nSeedInitializesOfficialData(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 官方语言就位且只读标记正确。
	langs, err := store.ListI18nLanguages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(langs) != 2 {
		t.Fatalf("expected 2 builtin languages, got %d", len(langs))
	}
	for _, lang := range langs {
		if !lang.IsBuiltin || !lang.IsEnabled {
			t.Fatalf("builtin language must be builtin+enabled: %+v", lang)
		}
	}

	// 全部官方词条 active 且双语完整。
	cache, err := store.LoadI18nTranslations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	zh, en := cache[I18nChinese], cache[I18nEnglish]
	if zh == nil || en == nil {
		t.Fatal("builtin translations missing")
	}
	for _, entry := range BuiltinTranslations {
		if zh[entry.Key] != entry.ZhCN {
			t.Fatalf("zh-CN %q = %q, want %q", entry.Key, zh[entry.Key], entry.ZhCN)
		}
		if en[entry.Key] != entry.EnUS {
			t.Fatalf("en-US %q = %q, want %q", entry.Key, en[entry.Key], entry.EnUS)
		}
	}

	// 标记已写入，二次打开不再执行 Seed（等价于 readI18nSeedVersion 命中）。
	version, err := store.readI18nSeedVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != I18nSchemaVersion {
		t.Fatalf("seed version = %d, want %d", version, I18nSchemaVersion)
	}

	// 每条词条的 {name} 占位符必须合法（正则限定单行标识符；JSON 示例等
	// 文本中的花括号不构成占位符）。成对一致性由结构测试覆盖。
	for _, entry := range BuiltinTranslations {
		for _, text := range []string{entry.ZhCN, entry.EnUS} {
			for _, placeholder := range extractI18nPlaceholders(text) {
				if !i18nPlaceholderPattern.MatchString(placeholder) {
					t.Fatalf("entry %q has malformed placeholder %q in %q", entry.Key, placeholder, text)
				}
			}
		}
	}
}

// i18nPlaceholderPattern 合法占位符名：字母数字下划线（不跨行）。
var i18nPlaceholderPattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// extractI18nPlaceholders 提取 {name} 形式的占位符（只匹配单行标识符，
// JSON/代码示例文本中的花括号会被自然忽略）。
func extractI18nPlaceholders(text string) []string {
	pattern := regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)
	var names []string
	for _, match := range pattern.FindAllStringSubmatch(text, -1) {
		names = append(names, match[1])
	}
	return names
}

func TestI18nSeedReappliesAndProtectsUserData(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	// 用户数据：自定义语言 + 译文。
	if err := store.CreateI18nLanguage(ctx, I18nLanguage{
		Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nEnglish,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveI18nTranslation(ctx, "ja-JP", "common.save", "保存"); err != nil {
		t.Fatal(err)
	}

	// 模拟官方译文被直接写库篡改 + 一个 seed 外的野 Key 存在。
	if err := store.SaveI18nTranslation(ctx, I18nChinese, "common.save", "篡改值"); err != nil {
		t.Fatal(err)
	}
	insertI18nKeysForTest(t, store, []I18nKeyDef{{Key: "stale.removed_key", Module: "stale", SourceVersion: 1}})
	if err := store.SaveI18nTranslation(ctx, I18nEnglish, "stale.removed_key", "stale"); err != nil {
		t.Fatal(err)
	}

	// 重置标记后强制重跑 Seed（等价于新版本词条集）。
	if _, err := store.db.Exec(`DELETE FROM settings WHERE key = ?`, i18nSeedVersionSettingKey); err != nil {
		t.Fatal(err)
	}
	if err := store.ensureI18nSeed(); err != nil {
		t.Fatal(err)
	}

	cache, err := store.LoadI18nTranslations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 官方译文被覆盖回源码值。
	if got := cache[I18nChinese]["common.save"]; got != "保存" {
		t.Fatalf("tampered official translation = %q, want overwritten by seed", got)
	}
	// seed 外的野 Key 退役，但其历史译文保留（不物理删除）。
	var active bool
	if err := store.db.QueryRow(`SELECT is_active FROM i18n_keys WHERE translation_key = 'stale.removed_key'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("stale key must be retired")
	}
	if got := cache[I18nEnglish]["stale.removed_key"]; got != "stale" {
		t.Fatalf("retired key must keep its historical translation, got %q", got)
	}
	// 用户语言与译文不受影响。
	if got := cache["ja-JP"]["common.save"]; got != "保存" {
		t.Fatalf("user translation must survive re-seed, got %q", got)
	}
	langs, err := store.ListI18nLanguages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(langs) != 3 {
		t.Fatalf("expected 3 languages after re-seed, got %d", len(langs))
	}

	// 退役 Key 不计入完成度分母。
	completions, err := store.I18nCompletions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := completions[I18nEnglish]; got.Total != len(BuiltinTranslations) {
		t.Fatalf("english completion total = %d, want %d (retired keys excluded)", got.Total, len(BuiltinTranslations))
	}
}

func TestI18nSeedSkipsWhenMarkerCurrent(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	// 标记命中时二次打开必须成功（不重复 Seed 也不报错）。
	store2, err := OpenStore(StoreOptions{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	version, err := store2.readI18nSeedVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != I18nSchemaVersion {
		t.Fatalf("seed version = %d, want %d", version, I18nSchemaVersion)
	}
}
