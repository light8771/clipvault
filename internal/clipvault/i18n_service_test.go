package clipvault

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// newI18nTestService 打开带 Seed 的库并构建服务。cfgMgr 为 nil 时服务
// 仍可用（SetLanguage 直接切换，仅供测试）。
func newI18nTestService(t *testing.T, cfgMgr *ConfigManager) *I18nService {
	t.Helper()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc, err := NewI18nService(store, cfgMgr, nil)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestI18nServiceFallbackMatrix(t *testing.T) {
	ctx := context.Background()
	svc := newI18nTestService(t, nil)
	store := svc.store

	// 自定义语言 ja-JP（base=en-US）：有"当前语言"译文、有 base 译文、
	// 只有 en-US、只有 zh-CN、完全缺失 五种情形一次铺齐。
	if err := store.CreateI18nLanguage(ctx, I18nLanguage{
		Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nEnglish, IsEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	insertI18nKeysForTest(t, store, []I18nKeyDef{
		{Key: "fb.current", Module: "fb", SourceVersion: 1},
		{Key: "fb.base", Module: "fb", SourceVersion: 1},
		{Key: "fb.en", Module: "fb", SourceVersion: 1},
		{Key: "fb.zh", Module: "fb", SourceVersion: 1},
		{Key: "fb.missing", Module: "fb", SourceVersion: 1},
	})
	insertI18nTranslationsForTest(t, store, "ja-JP", []I18nKeyText{
		{Key: "fb.current", Value: "現在"},
		{Key: "fb.base", Value: "ベース"},
	})
	insertI18nTranslationsForTest(t, store, I18nEnglish, []I18nKeyText{
		{Key: "fb.base", Value: "BASE"},
		{Key: "fb.en", Value: "english-only"},
	})
	insertI18nTranslationsForTest(t, store, I18nChinese, []I18nKeyText{
		{Key: "fb.zh", Value: "仅中文"},
	})
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetLanguage("ja-JP"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		key  string
		want string
	}{
		{"fb.current", "現在"},      // 第 1 级：当前语言
		{"fb.base", "ベース"},        // 第 2 级：自定义语言的 base（en-US）
		{"fb.en", "english-only"}, // 第 3 级：en-US
		{"fb.zh", "仅中文"},          // 第 4 级：zh-CN
		{"fb.missing", "fb.missing"},
		{"totally.unknown", "totally.unknown"},
	} {
		if got := svc.T(tc.key, nil); got != tc.want {
			t.Errorf("T(%q) = %q, want %q", tc.key, got, tc.want)
		}
	}

	// 官方语言运行：en-US 当前 → zh-CN 回退。
	if err := svc.SetLanguage(I18nEnglish); err != nil {
		t.Fatal(err)
	}
	if got := svc.T("fb.zh", nil); got != "仅中文" {
		t.Errorf("en-US T(fb.zh) = %q, want zh-CN fallback", got)
	}
	// zh-CN 当前：fb.en 不回退到"当前语言自身之上"——en-US 是第 3 级。
	if err := svc.SetLanguage(I18nChinese); err != nil {
		t.Fatal(err)
	}
	if got := svc.T("fb.en", nil); got != "english-only" {
		t.Errorf("zh-CN T(fb.en) = %q, want en-US fallback", got)
	}
}

func TestI18nServiceParams(t *testing.T) {
	svc := newI18nTestService(t, nil)
	// tray.tip_free_tier 带 {count} 占位符。
	if !strings.Contains(svc.T("tray.tip_free_tier", map[string]any{"count": 100}), "100") {
		t.Fatalf("param render failed: %q", svc.T("tray.tip_free_tier", map[string]any{"count": 100}))
	}
	if got := svc.T("tray.tip_free_tier", map[string]any{"count": 1000}); !strings.Contains(got, "1000") {
		t.Fatalf("int64 param render failed: %q", got)
	}
	// 缺参数：保留占位符，不 panic。
	got := svc.T("tray.tip_free_tier", nil)
	if !strings.Contains(got, "{count}") {
		t.Fatalf("missing param must keep placeholder, got %q", got)
	}
	// 无参数词条不受影响。
	if got := svc.T("common.save", map[string]any{"unused": 1}); got != "保存" && got != "Save" {
		t.Fatalf("plain entry corrupted by params: %q", got)
	}
}

func TestI18nServiceLanguageSwitchAndHooks(t *testing.T) {
	svc := newI18nTestService(t, nil)

	// 启动默认：auto → detectSystemLanguage（测试环境为 zh-CN 或 en-US
	// 之一，必为官方语言）。
	current := svc.CurrentLanguage()
	if !isI18nBuiltinLanguageCode(current) {
		t.Fatalf("default current language = %q, want builtin", current)
	}

	// 切换钩子触发。
	switched := make(chan string, 4)
	svc.OnLanguageChanged(func() { switched <- svc.CurrentLanguage() })
	if err := svc.SetLanguage(I18nEnglish); err != nil {
		t.Fatal(err)
	}
	if got := <-switched; got != I18nEnglish {
		t.Fatalf("hook saw %q, want en-US", got)
	}
	if svc.CurrentLanguage() != I18nEnglish {
		t.Fatalf("current = %q, want en-US", svc.CurrentLanguage())
	}
	if got := svc.T("common.save", nil); got != "Save" {
		t.Fatalf("T after switch = %q, want Save", got)
	}

	// 切回 auto：走系统检测。
	if err := svc.SetLanguage(I18nAuto); err != nil {
		t.Fatal(err)
	}
	<-switched
	if !isI18nBuiltinLanguageCode(svc.CurrentLanguage()) {
		t.Fatalf("auto current = %q, want builtin", svc.CurrentLanguage())
	}

	// 未知/禁用语言：SetLanguage 报错；配置值不可用时回退 en-US。
	if err := svc.SetLanguage("xx-XX"); !errors.Is(err, ErrI18nLanguageNotFound) {
		t.Fatalf("SetLanguage(unknown) = %v, want %v", err, ErrI18nLanguageNotFound)
	}
	if err := disableI18nLanguageForTest(t, svc, "ja-JP"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetLanguage("ja-JP"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("SetLanguage(disabled) = %v, want disabled error", err)
	}
	svc.applyConfigLanguage("ja-JP") // 模拟配置指向禁用语言
	if svc.CurrentLanguage() != I18nEnglish {
		t.Fatalf("unavailable configured language must fall back to en-US, got %q", svc.CurrentLanguage())
	}
}

// disableI18nLanguageForTest 建一个语言并直接置为禁用。
func disableI18nLanguageForTest(t *testing.T, svc *I18nService, code string) error {
	t.Helper()
	ctx := context.Background()
	if err := svc.store.CreateI18nLanguage(ctx, I18nLanguage{Code: code, Name: "X", BaseLanguageCode: I18nEnglish, IsEnabled: true}); err != nil {
		return err
	}
	if err := svc.store.UpdateI18nLanguage(ctx, code, "X", "", I18nEnglish, false); err != nil {
		return err
	}
	return svc.Reload()
}

func TestI18nServiceEditSync(t *testing.T) {
	ctx := context.Background()
	svc := newI18nTestService(t, nil)
	if err := svc.store.CreateI18nLanguage(ctx, I18nLanguage{
		Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nEnglish, IsEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetLanguage("ja-JP"); err != nil {
		t.Fatal(err)
	}

	// 保存 → 缓存即时生效（base en-US 的 "Save" 被覆盖）。
	if got := svc.T("common.save", nil); got != "Save" {
		t.Fatalf("pre-save T = %q, want base Save", got)
	}
	if err := svc.store.SaveI18nTranslation(ctx, "ja-JP", "common.save", "保存"); err != nil {
		t.Fatal(err)
	}
	svc.ApplyTranslationSaved("ja-JP", "common.save", "保存")
	if got := svc.T("common.save", nil); got != "保存" {
		t.Fatalf("post-save T = %q, want 保存", got)
	}

	// 删除 → 回退 base。
	if err := svc.store.DeleteI18nTranslation(ctx, "ja-JP", "common.save"); err != nil {
		t.Fatal(err)
	}
	svc.ApplyTranslationDeleted("ja-JP", "common.save")
	if got := svc.T("common.save", nil); got != "Save" {
		t.Fatalf("post-delete T = %q, want fallback Save", got)
	}
}

func TestI18nServiceEffectiveDictionary(t *testing.T) {
	ctx := context.Background()
	svc := newI18nTestService(t, nil)
	if err := svc.store.CreateI18nLanguage(ctx, I18nLanguage{
		Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nChinese, IsEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	insertI18nKeysForTest(t, svc.store, []I18nKeyDef{
		{Key: "dict.custom", Module: "dict", SourceVersion: 1},
		{Key: "dict.zh_only", Module: "dict", SourceVersion: 1},
	})
	// ja-JP 只译一条，其余回退 zh-CN 基底。
	if err := svc.store.SaveI18nTranslation(ctx, "ja-JP", "dict.custom", "辞書"); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.SaveI18nTranslation(ctx, I18nChinese, "dict.zh_only", "中文基底"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetLanguage("ja-JP"); err != nil {
		t.Fatal(err)
	}

	_, dict := svc.DictionarySnapshot()
	if got := dict["dict.custom"]; got != "辞書" {
		t.Errorf("dict[dict.custom] = %q, want 辞書", got)
	}
	if got := dict["dict.zh_only"]; got != "中文基底" {
		t.Errorf("dict[dict.zh_only] = %q, want zh-CN base", got)
	}
	if got := dict["common.save"]; got != "保存" {
		// base=zh-CN：基底 zh-CN，再被 ja-JP 覆盖（common.save ja-JP 无译文）
		t.Errorf("dict[common.save] = %q, want 保存", got)
	}
}

func TestI18nServiceWithConfigManager(t *testing.T) {
	ctx := context.Background()
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfgMgr, err := NewConfigManager(store)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewI18nService(store, cfgMgr, nil)
	if err != nil {
		t.Fatal(err)
	}

	// SetLanguage 经 ConfigManager 持久化（"auto" → 具体语言）。
	if err := store.CreateI18nLanguage(ctx, I18nLanguage{
		Code: "ja-JP", Name: "Japanese", NativeName: "日本語", BaseLanguageCode: I18nEnglish, IsEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reload(); err != nil {
		t.Fatal(err)
	}
	switched := make(chan string, 4)
	svc.OnLanguageChanged(func() { switched <- svc.CurrentLanguage() })
	if err := svc.SetLanguage("ja-JP"); err != nil {
		t.Fatal(err)
	}
	if got := <-switched; got != "ja-JP" {
		t.Fatalf("hook saw %q, want ja-JP", got)
	}
	if cfgMgr.Get().UI.Language != "ja-JP" {
		t.Fatalf("persisted config language = %q, want ja-JP", cfgMgr.Get().UI.Language)
	}
	persisted, err := store.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.UI.Language != "ja-JP" {
		t.Fatalf("stored config language = %q, want ja-JP", persisted.UI.Language)
	}

	// 其他配置路径（PATCH /api/config 等价于 cfgMgr.Update）同样驱动服务。
	cfg := cfgMgr.Get()
	cfg.UI.Language = I18nChinese
	if err := cfgMgr.Update(cfg); err != nil {
		t.Fatal(err)
	}
	if got := <-switched; got != I18nChinese {
		t.Fatalf("hook saw %q, want zh-CN via config update", got)
	}
}

func TestI18nDictionarySnapshotKeepsLanguageAndTextTogether(t *testing.T) {
	svc := newI18nTestService(t, nil)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			svc.applyConfigLanguage(I18nChinese)
			svc.applyConfigLanguage(I18nEnglish)
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 200; i++ {
		code, dict := svc.DictionarySnapshot()
		want := "Save"
		if code == I18nChinese {
			want = "保存"
		}
		if dict["common.save"] != want {
			t.Fatalf("language=%s, text=%q", code, dict["common.save"])
		}
		dict["common.save"] = "mutated client copy"
	}
}
