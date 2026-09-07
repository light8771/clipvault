package clipvault

import (
	"encoding/json"
	"testing"
)

func TestNormalizeAppearance(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Appearance = AppearanceSettings{
		Preset:         "custom",
		Mode:           "dark",
		Primary:        "#123456",
		Accent:         "#abcdef",
		FontScale:      125,
		SurfaceOpacity: 80,
	}
	normalizeConfig(&cfg)

	if cfg.Appearance.Preset != "custom" || cfg.Appearance.Mode != "dark" {
		t.Fatalf("custom theme selection was not retained: %#v", cfg.Appearance)
	}
	if cfg.Appearance.Primary != "#123456" || cfg.Appearance.Accent != "#abcdef" {
		t.Fatalf("custom colors were not retained: %#v", cfg.Appearance)
	}
	if cfg.Appearance.FontScale != 125 {
		t.Fatalf("custom font scale was not retained: %#v", cfg.Appearance)
	}
	if cfg.Appearance.SurfaceOpacity != 80 {
		t.Fatalf("custom surface opacity was not retained: %#v", cfg.Appearance)
	}

	cfg.Appearance = AppearanceSettings{Preset: "unknown", Mode: "unknown", SurfaceOpacity: 100}
	normalizeConfig(&cfg)
	if cfg.Appearance.Preset != "mint" || cfg.Appearance.Mode != "system" {
		t.Fatalf("invalid theme did not fall back to defaults: %#v", cfg.Appearance)
	}
	if cfg.Appearance.SurfaceOpacity != 100 {
		t.Fatalf("surface opacity was not retained: %#v", cfg.Appearance)
	}

	cfg.Appearance = AppearanceSettings{Preset: "custom", Mode: "light", Primary: "invalid", Accent: "#12345", SurfaceOpacity: 100}
	normalizeConfig(&cfg)
	if cfg.Appearance.Primary != "#14B8A6" || cfg.Appearance.Accent != "#F97316" {
		t.Fatalf("invalid custom colors did not fall back to defaults: %#v", cfg.Appearance)
	}
	if cfg.Appearance.FontScale != 100 {
		t.Fatalf("missing font scale did not fall back to default: %#v", cfg.Appearance)
	}
	if cfg.Appearance.SurfaceOpacity != 100 {
		t.Fatalf("surface opacity was not retained: %#v", cfg.Appearance)
	}

	cfg.Appearance.FontScale = 200
	normalizeConfig(&cfg)
	if cfg.Appearance.FontScale != 150 {
		t.Fatalf("font scale was not clamped: %#v", cfg.Appearance)
	}

	cfg.Appearance.SurfaceOpacity = 200
	normalizeConfig(&cfg)
	if cfg.Appearance.SurfaceOpacity != 100 {
		t.Fatalf("surface opacity was not clamped to max: %#v", cfg.Appearance)
	}

	cfg.Appearance.SurfaceOpacity = -10
	normalizeConfig(&cfg)
	if cfg.Appearance.SurfaceOpacity != 0 {
		t.Fatalf("surface opacity was not clamped to min: %#v", cfg.Appearance)
	}

	cfg.Appearance.SurfaceOpacity = 0
	normalizeConfig(&cfg)
	if cfg.Appearance.SurfaceOpacity != 0 {
		t.Fatalf("explicit zero surface opacity was not retained: %#v", cfg.Appearance)
	}
}

func TestNormalizeNavigation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Navigation = NavigationSettings{Sticky: false, Blur: 40, Opacity: 140, Divider: false}
	normalizeConfig(&cfg)
	if cfg.Navigation.Blur != 32 || cfg.Navigation.Opacity != 100 {
		t.Fatalf("navigation values were not clamped: %#v", cfg.Navigation)
	}

	// -1 是合法显式值（跟随界面不透明度），不得被 clamp 改写。
	cfg.Navigation.Opacity = -1
	normalizeConfig(&cfg)
	if cfg.Navigation.Opacity != -1 {
		t.Fatalf("follow opacity (-1) must be retained: %#v", cfg.Navigation)
	}

	cfg.Navigation.Opacity = -2
	normalizeConfig(&cfg)
	if cfg.Navigation.Opacity != -1 {
		t.Fatalf("opacity below -1 must fall back to follow (-1): %#v", cfg.Navigation)
	}

	cfg.Navigation.Opacity = 0
	normalizeConfig(&cfg)
	if cfg.Navigation.Opacity != 0 {
		t.Fatalf("explicit zero opacity must be retained: %#v", cfg.Navigation)
	}
}

func TestNormalizeHistoryPage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HistoryPage = HistoryPageSettings{
		Layout:            "diagonal",
		Columns:           6,
		ForceColumns:      true,
		ContentWidthMode:  "fixed",
		ContentWidth:      99,
		HorizontalPadding: 200,
		CardGap:           64,
		PreviewLines:      99,
	}
	normalizeConfig(&cfg)
	page := cfg.HistoryPage
	if page.Layout != "top-bottom" {
		t.Fatalf("invalid layout did not fall back to default: %#v", page)
	}
	if page.Columns != 0 {
		t.Fatalf("out-of-range columns must fall back to auto: %#v", page)
	}
	if page.ForceColumns {
		t.Fatalf("force columns must be false when columns fall back to auto: %#v", page)
	}
	if page.ContentWidthMode != "auto" {
		t.Fatalf("invalid content width mode did not fall back to default: %#v", page)
	}
	if page.ContentWidth != 720 || page.HorizontalPadding != 80 || page.CardGap != 32 || page.PreviewLines != 20 {
		t.Fatalf("history page values were not clamped: %#v", page)
	}

	// auto + force = 无效组合：normalize 后 force 必须为 false，不得报错。
	page.Columns = 0
	page.ForceColumns = true
	cfg.HistoryPage = page
	normalizeConfig(&cfg)
	if cfg.HistoryPage.ForceColumns {
		t.Fatalf("force columns must be cleared in auto mode: %#v", cfg.HistoryPage)
	}

	// 不限制（0 行）+ 固定占位 = 无效组合：normalize 后 fixedPreview 必须为 false。
	cfg.HistoryPage = HistoryPageSettings{PreviewLines: 0, FixedPreview: true}
	normalizeConfig(&cfg)
	if cfg.HistoryPage.FixedPreview {
		t.Fatalf("fixed preview must be cleared when lines are unlimited: %#v", cfg.HistoryPage)
	}

	// 合法固定列数 + force 原样保留；auto 模式下 contentWidth 保留为备用值。
	cfg.HistoryPage = HistoryPageSettings{
		Layout:            "left-right",
		Columns:           3,
		ForceColumns:      true,
		ContentWidthMode:  "auto",
		ContentWidth:      1800,
		HorizontalPadding: 40,
		CardGap:           16,
		PreviewLines:      5,
		FixedPreview:      true,
	}
	normalizeConfig(&cfg)
	if cfg.HistoryPage.Columns != 3 || !cfg.HistoryPage.ForceColumns || cfg.HistoryPage.ContentWidth != 1800 || !cfg.HistoryPage.FixedPreview {
		t.Fatalf("valid history page settings were altered by normalize: %#v", cfg.HistoryPage)
	}
}

// 新作用域字段仍属于严格配置契约：缺失任一必需字段必须拒绝加载。
func TestLayoutConfigRequiresStableFields(t *testing.T) {
	data, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range []struct {
		name     string
		required []string
	}{
		{"appearance", []string{"preset", "mode", "primary", "accent", "fontScale", "surfaceOpacity"}},
		{"navigation", []string{"sticky", "blur", "opacity", "divider"}},
		{"historyPage", []string{"layout", "columns", "forceColumns", "contentWidthMode", "contentWidth", "horizontalPadding", "cardGap", "previewLines", "fixedPreview"}},
		{"popup", []string{"hideConsecutiveDuplicates", "keyOpen", "keyRestore", "keyPaste"}},
	} {
		for _, key := range section.required {
			var asMap map[string]any
			if err := json.Unmarshal(data, &asMap); err != nil {
				t.Fatal(err)
			}
			sectionMap := asMap[section.name].(map[string]any)
			value, present := sectionMap[key]
			if !present {
				t.Fatalf("default config JSON missing %s.%s", section.name, key)
			}
			delete(sectionMap, key)
			trimmed, err := json.Marshal(asMap)
			if err != nil {
				t.Fatal(err)
			}
			var cfg Config
			if err := cfg.UnmarshalJSON(trimmed); err == nil {
				t.Fatalf("config without %s.%s must be rejected as corrupt", section.name, key)
			}
			sectionMap[key] = value
		}
	}
}

func TestLegacyThemeConfigIsRejected(t *testing.T) {
	data, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatal(err)
	}
	asMap["theme"] = map[string]any{"preset": "ocean", "layout": "sidebar"}
	legacyData, err := json.Marshal(asMap)
	if err != nil {
		t.Fatal(err)
	}

	var cfg Config
	if err := cfg.UnmarshalJSON(legacyData); err == nil {
		t.Fatal("obsolete theme config was accepted")
	}
}

func TestHistoryPageConfigPersists(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	cfg := DefaultConfig()
	cfg.Appearance = AppearanceSettings{
		Preset:         "custom",
		Mode:           "light",
		Primary:        "#2563EB",
		Accent:         "#F59E0B",
		FontScale:      135,
		SurfaceOpacity: 72,
	}
	cfg.Navigation = NavigationSettings{Sticky: false, Blur: 8, Opacity: 40, Divider: false}
	cfg.HistoryPage = HistoryPageSettings{
		Layout:            "left-right",
		Columns:           3,
		ForceColumns:      true,
		ContentWidthMode:  "custom",
		ContentWidth:      1800,
		HorizontalPadding: 40,
		CardGap:           8,
		PreviewLines:      5,
	}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, err := store.GetConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got.Appearance != cfg.Appearance || got.Navigation != cfg.Navigation || got.HistoryPage != cfg.HistoryPage {
		t.Fatalf("persisted layout config mismatch: got %#v / %#v / %#v", got.Appearance, got.Navigation, got.HistoryPage)
	}
}

func TestLayoutConfigManagerUpdate(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}

	wantAppearance := AppearanceSettings{Preset: "custom", Mode: "dark", Primary: "#2563EB", Accent: "#F59E0B", FontScale: 135, SurfaceOpacity: 72}
	wantNavigation := NavigationSettings{Sticky: false, Blur: 24, Opacity: 55, Divider: false}
	wantHistory := HistoryPageSettings{Layout: "left-right", Columns: 4, ForceColumns: true, ContentWidthMode: "custom", ContentWidth: 2400, HorizontalPadding: 60, CardGap: 20, PreviewLines: 3}
	var notifiedAppearance AppearanceSettings
	var notifiedNavigation NavigationSettings
	var notifiedHistory HistoryPageSettings
	manager.OnChange(func(cfg Config) error {
		notifiedAppearance = cfg.Appearance
		notifiedNavigation = cfg.Navigation
		notifiedHistory = cfg.HistoryPage
		return nil
	})

	cfg := manager.Get()
	cfg.Appearance = wantAppearance
	cfg.Navigation = wantNavigation
	cfg.HistoryPage = wantHistory
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("update config: %v", err)
	}
	if got := manager.Get(); got.Appearance != wantAppearance || got.Navigation != wantNavigation || got.HistoryPage != wantHistory {
		t.Fatalf("in-memory layout config = %#v / %#v / %#v", got.Appearance, got.Navigation, got.HistoryPage)
	}
	if notifiedAppearance != wantAppearance || notifiedNavigation != wantNavigation || notifiedHistory != wantHistory {
		t.Fatalf("listener layout config = %#v / %#v / %#v", notifiedAppearance, notifiedNavigation, notifiedHistory)
	}
	persisted, err := store.GetConfig()
	if err != nil {
		t.Fatalf("get persisted config: %v", err)
	}
	if persisted.Appearance != wantAppearance || persisted.Navigation != wantNavigation || persisted.HistoryPage != wantHistory {
		t.Fatalf("persisted layout config = %#v / %#v / %#v", persisted.Appearance, persisted.Navigation, persisted.HistoryPage)
	}
}
