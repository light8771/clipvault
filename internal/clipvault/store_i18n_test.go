package clipvault

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

// seedKeySet 返回官方词条全集（退役测试的 keep 集基底——Seed 写入的
// 官方 Key 必须始终保留）。
func seedKeySet() map[string]struct{} {
	set := make(map[string]struct{}, len(BuiltinTranslations))
	for _, entry := range BuiltinTranslations {
		set[entry.Key] = struct{}{}
	}
	return set
}

func openI18nTestStore(t *testing.T) *Store {
	t.Helper()
	// OpenStore 自动执行官方词条 Seed：zh-CN / en-US 与 BuiltinTranslations
	// 已就位，测试只需补自定义数据。
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func insertI18nKeysForTest(t *testing.T, store *Store, defs []I18nKeyDef) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := upsertI18nKeys(context.Background(), tx, defs); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func insertI18nTranslationsForTest(t *testing.T, store *Store, languageCode string, entries []I18nKeyText) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := upsertI18nTranslations(context.Background(), tx, languageCode, entries); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreI18nLanguageLifecycle(t *testing.T) {
	ctx := context.Background()
	store := openI18nTestStore(t)

	ja := I18nLanguage{Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nEnglish}
	if err := store.CreateI18nLanguage(ctx, ja); err != nil {
		t.Fatalf("create ja-JP: %v", err)
	}

	// code 唯一。
	if err := store.CreateI18nLanguage(ctx, ja); !errors.Is(err, ErrI18nLanguageExists) {
		t.Fatalf("duplicate code error = %v, want %v", err, ErrI18nLanguageExists)
	}

	langs, err := store.ListI18nLanguages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(langs) != 3 {
		t.Fatalf("expected 3 languages, got %d", len(langs))
	}
	if langs[2].Code != "ja-JP" || langs[2].BaseLanguageCode != I18nEnglish {
		t.Fatalf("unexpected language %+v", langs[2])
	}

	// 更新自定义语言。
	if err := store.UpdateI18nLanguage(ctx, "ja-JP", "Japanese", "日本語（編集）", I18nChinese, false); err != nil {
		t.Fatalf("update ja-JP: %v", err)
	}
	got, err := store.GetI18nLanguage(ctx, "ja-JP")
	if err != nil {
		t.Fatal(err)
	}
	if got.NativeName != "日本語（編集）" || got.BaseLanguageCode != I18nChinese || got.IsEnabled {
		t.Fatalf("unexpected updated language %+v", got)
	}

	// 官方语言只读：更新与删除都拒绝。
	for _, code := range []string{I18nChinese, I18nEnglish} {
		if err := store.UpdateI18nLanguage(ctx, code, "X", "X", I18nEnglish, true); !errors.Is(err, ErrI18nBuiltinReadonly) {
			t.Fatalf("update builtin %s error = %v, want %v", code, err, ErrI18nBuiltinReadonly)
		}
		if err := store.DeleteI18nLanguage(ctx, code); !errors.Is(err, ErrI18nBuiltinReadonly) {
			t.Fatalf("delete builtin %s error = %v, want %v", code, err, ErrI18nBuiltinReadonly)
		}
	}

	// 删除自定义语言时级联清理译文。
	if err := store.SaveI18nTranslation(ctx, "ja-JP", "common.save", "保存"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteI18nLanguage(ctx, "ja-JP"); err != nil {
		t.Fatalf("delete ja-JP: %v", err)
	}
	if _, err := store.GetI18nLanguage(ctx, "ja-JP"); !errors.Is(err, ErrI18nLanguageNotFound) {
		t.Fatalf("get deleted language error = %v, want %v", err, ErrI18nLanguageNotFound)
	}
	cache, err := store.LoadI18nTranslations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cache["ja-JP"]; ok {
		t.Fatal("ja-JP translations must be removed with the language")
	}
}

func TestStoreI18nKeyUpsertAndRetire(t *testing.T) {
	ctx := context.Background()
	store := openI18nTestStore(t)

	defs := []I18nKeyDef{
		{Key: "keytest.alpha", Module: "keytest", Description: "alpha", SourceVersion: 1},
		{Key: "keytest.beta", Module: "keytest", SourceVersion: 1},
		{Key: "keytest.gamma", Module: "keytest", SourceVersion: 1},
	}
	insertI18nKeysForTest(t, store, defs)

	// 再 upsert：source_version 保留首见值，module/description 更新，幂等。
	updated := []I18nKeyDef{
		{Key: "keytest.alpha", Module: "keytest", Description: "alpha（修订）", SourceVersion: 9},
		{Key: "keytest.beta", Module: "keytest", SourceVersion: 9},
	}
	insertI18nKeysForTest(t, store, updated)

	var version int
	var description string
	if err := store.db.QueryRow(`SELECT source_version, description FROM i18n_keys WHERE translation_key = 'keytest.alpha'`).
		Scan(&version, &description); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("source_version = %d, want first-seen 1", version)
	}
	if description != "alpha（修订）" {
		t.Fatalf("description = %q, want updated", description)
	}

	// 退役：keep 之外的全部 active 置 false（官方 Seed Key 全部保留）。
	keep := seedKeySet()
	keep["keytest.alpha"] = struct{}{}
	keep["keytest.beta"] = struct{}{}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	retired, err := retireI18nKeys(ctx, tx, keep)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if retired != 1 {
		t.Fatalf("retired = %d, want 1", retired)
	}
	var active bool
	if err := store.db.QueryRow(`SELECT is_active FROM i18n_keys WHERE translation_key = 'keytest.gamma'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("retired key must be inactive")
	}

	// 退役后回归：重新 upsert 即重新激活，source_version 仍保留首见值。
	insertI18nKeysForTest(t, store, []I18nKeyDef{{Key: "keytest.gamma", Module: "keytest", SourceVersion: 5}})
	if err := store.db.QueryRow(`SELECT is_active, source_version FROM i18n_keys WHERE translation_key = 'keytest.gamma'`).
		Scan(&active, &version); err != nil {
		t.Fatal(err)
	}
	if !active || version != 1 {
		t.Fatalf("reactivated key active=%v source_version=%d, want true/1", active, version)
	}
}

func TestStoreI18nTranslations(t *testing.T) {
	ctx := context.Background()
	store := openI18nTestStore(t)

	if err := store.SaveI18nTranslation(ctx, "ja-JP", "common.save", "保存"); err != nil {
		t.Fatal(err)
	}
	// 空值禁止入库（空翻译语义 = 删除）。
	if err := store.SaveI18nTranslation(ctx, "ja-JP", "common.cancel", ""); !errors.Is(err, ErrI18nEmptyTranslation) {
		t.Fatalf("empty value error = %v, want %v", err, ErrI18nEmptyTranslation)
	}
	// 超限拒绝。
	if err := store.SaveI18nTranslation(ctx, "ja-JP", "common.ok", string(make([]byte, MaxI18nTranslationBytes+1))); !errors.Is(err, ErrI18nTranslationTooLarge) {
		t.Fatalf("oversize error = %v, want %v", err, ErrI18nTranslationTooLarge)
	}

	// upsert 更新已有值。
	if err := store.SaveI18nTranslation(ctx, "ja-JP", "common.save", "保存する"); err != nil {
		t.Fatal(err)
	}
	cache, err := store.LoadI18nTranslations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := cache["ja-JP"]["common.save"]; got != "保存する" {
		t.Fatalf("common.save = %q, want upserted value", got)
	}

	// 删除后自然缺省。
	if err := store.DeleteI18nTranslation(ctx, "ja-JP", "common.save"); err != nil {
		t.Fatal(err)
	}
	cache, err = store.LoadI18nTranslations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cache["ja-JP"]["common.save"]; ok {
		t.Fatal("deleted translation must be absent")
	}
}

func TestStoreI18nCompletions(t *testing.T) {
	ctx := context.Background()
	store := openI18nTestStore(t)

	// Seed 已写入 len(BuiltinTranslations) 个官方 Key 与双语译文，
	// 完成度期望值以此为基线叠加测试数据。
	base := len(BuiltinTranslations)
	insertI18nKeysForTest(t, store, []I18nKeyDef{
		{Key: "completion.one", Module: "completion", SourceVersion: 1},
		{Key: "completion.two", Module: "completion", SourceVersion: 1},
		{Key: "completion.three", Module: "completion", SourceVersion: 1},
	})
	insertI18nTranslationsForTest(t, store, I18nEnglish, []I18nKeyText{
		{Key: "completion.one", Value: "One"},
		{Key: "completion.two", Value: "Two"},
		{Key: "completion.three", Value: "Three"},
	})
	if err := store.CreateI18nLanguage(ctx, I18nLanguage{Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nEnglish}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveI18nTranslation(ctx, "ja-JP", "completion.one", "壱"); err != nil {
		t.Fatal(err)
	}
	// 一个不在 i18n_keys 中的词条不应计入完成度。
	if err := store.SaveI18nTranslation(ctx, "ja-JP", "completion.unknown", "？"); err != nil {
		t.Fatal(err)
	}

	completions, err := store.I18nCompletions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]I18nCounts{
		I18nChinese: {Total: base + 3, Translated: base},
		I18nEnglish: {Total: base + 3, Translated: base + 3},
		"ja-JP":     {Total: base + 3, Translated: 1},
	}
	for code, counts := range want {
		got, ok := completions[code]
		if !ok {
			t.Fatalf("completion for %s missing", code)
		}
		if got != counts {
			t.Fatalf("completion[%s] = %+v, want %+v", code, got, counts)
		}
	}
}

func TestStoreI18nEditorQuery(t *testing.T) {
	ctx := context.Background()
	store := openI18nTestStore(t)

	// 测试数据收敛在独立模块 editor（另一模块 othermodule 用于验证模块
	// 筛选的区分度），与官方 Seed 词条互不干扰。
	insertI18nKeysForTest(t, store, []I18nKeyDef{
		{Key: "editor.alfa", Module: "editor", SourceVersion: 1},
		{Key: "editor.beta", Module: "editor", SourceVersion: 1},
		{Key: "editor.gamma.title", Module: "editor", SourceVersion: 2},
		{Key: "editor.gamma.create", Module: "editor", SourceVersion: 2},
		{Key: "othermodule.solo", Module: "othermodule", SourceVersion: 1},
	})
	insertI18nTranslationsForTest(t, store, I18nEnglish, []I18nKeyText{
		{Key: "editor.alfa", Value: "Alfa"},
		{Key: "editor.beta", Value: "Beta"},
		{Key: "editor.gamma.title", Value: "Gamma Title"},
		{Key: "editor.gamma.create", Value: "Gamma Create"},
		{Key: "othermodule.solo", Value: "Solo"},
	})
	if err := store.CreateI18nLanguage(ctx, I18nLanguage{Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nEnglish}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range []I18nKeyText{
		{Key: "editor.alfa", Value: "ア"},
		{Key: "editor.beta", Value: "ベ"},
	} {
		if err := store.SaveI18nTranslation(ctx, "ja-JP", kv.Key, kv.Value); err != nil {
			t.Fatal(err)
		}
	}

	// 所有查询限定 editor 模块，基线即 4 条测试词条。
	newEditorFilter := func(mutate func(*I18nEditorFilter)) I18nEditorFilter {
		filter := I18nEditorFilter{LanguageCode: "ja-JP", BaseLanguageCode: I18nEnglish, Module: "editor"}
		if mutate != nil {
			mutate(&filter)
		}
		return filter
	}

	// 全量 + 总数。
	entries, total, err := store.ListI18nEditorEntries(ctx, newEditorFilter(nil))
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(entries) != 4 {
		t.Fatalf("total=%d len=%d, want 4/4", total, len(entries))
	}
	// ORDER BY module, translation_key：editor.alfa < editor.beta < editor.gamma.create < editor.gamma.title。
	if entries[0].Key != "editor.alfa" || entries[0].SourceText != "Alfa" || entries[0].CustomText != "ア" {
		t.Fatalf("unexpected first entry %+v", entries[0])
	}
	if entries[2].Module != "editor" || entries[2].CustomText != "" {
		t.Fatalf("untranslated entry must have empty custom text: %+v", entries[2])
	}

	// 状态筛选。
	untranslated, total, err := store.ListI18nEditorEntries(ctx, newEditorFilter(func(f *I18nEditorFilter) { f.Status = I18nEditorStatusUntranslated }))
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(untranslated) != 2 {
		t.Fatalf("untranslated total=%d len=%d, want 2/2", total, len(untranslated))
	}
	translated, total, err := store.ListI18nEditorEntries(ctx, newEditorFilter(func(f *I18nEditorFilter) { f.Status = I18nEditorStatusTranslated }))
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(translated) != 2 {
		t.Fatalf("translated total=%d len=%d, want 2/2", total, len(translated))
	}

	// 最近新增（source_version >= 2）。
	recent, total, err := store.ListI18nEditorEntries(ctx, newEditorFilter(func(f *I18nEditorFilter) {
		f.Status = I18nEditorStatusRecent
		f.RecentFrom = 2
	}))
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(recent) != 2 || recent[0].Key != "editor.gamma.create" {
		t.Fatalf("recent total=%d first=%+v, want 2/editor.gamma.create", total, recent[0])
	}

	// 搜索：匹配 Key（不区分大小写）、基准文本、自定义译文。
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"gamma.title", "editor.gamma.title"},
		{"GAMMA.CREATE", "editor.gamma.create"},
		{"Alfa", "editor.alfa"},
		{"editor.alfa", "editor.alfa"},
		{"ベ", "editor.beta"},
	} {
		entries, total, err := store.ListI18nEditorEntries(ctx, newEditorFilter(func(f *I18nEditorFilter) { f.Query = tc.query }))
		if err != nil {
			t.Fatalf("search %q: %v", tc.query, err)
		}
		if total != 1 || len(entries) != 1 || entries[0].Key != tc.want {
			t.Fatalf("search %q total=%d entries=%+v, want single %s", tc.query, total, entries, tc.want)
		}
	}

	// 搜索词中的通配符按字面匹配：editor 模块没有含 % 的词条，命中 0 条。
	if _, total, err := store.ListI18nEditorEntries(ctx, newEditorFilter(func(f *I18nEditorFilter) { f.Query = "%" })); err != nil || total != 0 {
		t.Fatalf("literal %% search total=%d err=%v, want 0", total, err)
	}

	// 分页。
	page1, total, err := store.ListI18nEditorEntries(ctx, newEditorFilter(func(f *I18nEditorFilter) { f.PageSize, f.Page = 3, 1 }))
	if err != nil {
		t.Fatal(err)
	}
	page2, _, err := store.ListI18nEditorEntries(ctx, newEditorFilter(func(f *I18nEditorFilter) { f.PageSize, f.Page = 3, 2 }))
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(page1) != 3 || len(page2) != 1 {
		t.Fatalf("pagination total=%d page1=%d page2=%d, want 4/3/1", total, len(page1), len(page2))
	}
	if page1[0].Key == page2[0].Key {
		t.Fatal("pages must not overlap")
	}

	// 非法状态。
	if _, _, err := store.ListI18nEditorEntries(ctx, newEditorFilter(func(f *I18nEditorFilter) { f.Status = "bogus" })); err == nil {
		t.Fatal("expected invalid status error")
	}

	// 模块筛选排除 othermodule。
	_, total, err = store.ListI18nEditorEntries(ctx, I18nEditorFilter{LanguageCode: "ja-JP", BaseLanguageCode: I18nEnglish, Module: "othermodule"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("othermodule total=%d, want 1", total)
	}

	// 模块列表包含测试模块与官方模块。
	modules, err := store.ListI18nModules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"editor", "othermodule", "common", "tray"} {
		if !slices.Contains(modules, module) {
			t.Fatalf("modules = %v, missing %q", modules, module)
		}
	}
}

// TestStoreI18nUpsertChunking 验证超过单条 SQL 行数上限的批量 upsert 分块正确。
func TestStoreI18nUpsertChunking(t *testing.T) {
	ctx := context.Background()
	store := openI18nTestStore(t)

	const count = i18nUpsertChunk + 50
	defs := make([]I18nKeyDef, 0, count)
	entries := make([]I18nKeyText, 0, count)
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("chunk.key.%04d", i)
		defs = append(defs, I18nKeyDef{Key: key, Module: "chunk", SourceVersion: 1})
		entries = append(entries, I18nKeyText{Key: key, Value: fmt.Sprintf("value-%04d", i)})
	}
	insertI18nKeysForTest(t, store, defs)
	insertI18nTranslationsForTest(t, store, I18nEnglish, entries)

	_, total, err := store.ListI18nEditorEntries(ctx, I18nEditorFilter{LanguageCode: "ja-JP", BaseLanguageCode: I18nEnglish, Module: "chunk"})
	if err != nil {
		t.Fatal(err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}

	// 幂等重跑（模拟再次启动 Seed）。
	insertI18nKeysForTest(t, store, defs)
	insertI18nTranslationsForTest(t, store, I18nEnglish, entries)
	completions, err := store.I18nCompletions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := len(BuiltinTranslations) + count
	if got := completions[I18nEnglish]; got.Total != want || got.Translated != want {
		t.Fatalf("after re-seed english = %+v, want %d/%d", got, want, want)
	}
}
