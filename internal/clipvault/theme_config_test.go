package clipvault

import (
	"encoding/json"
	"testing"
)

func TestNormalizeTheme(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Theme = ThemeConfig{
		Preset:         "custom",
		Mode:           "dark",
		Primary:        "#123456",
		Accent:         "#abcdef",
		FontScale:      125,
		SurfaceOpacity: 80,
	}
	normalizeConfig(&cfg)

	if cfg.Theme.Preset != "custom" || cfg.Theme.Mode != "dark" {
		t.Fatalf("custom theme selection was not retained: %#v", cfg.Theme)
	}
	if cfg.Theme.Primary != "#123456" || cfg.Theme.Accent != "#abcdef" {
		t.Fatalf("custom colors were not retained: %#v", cfg.Theme)
	}
	if cfg.Theme.FontScale != 125 {
		t.Fatalf("custom font scale was not retained: %#v", cfg.Theme)
	}
	if cfg.Theme.SurfaceOpacity != 80 {
		t.Fatalf("custom surface opacity was not retained: %#v", cfg.Theme)
	}

	cfg.Theme = ThemeConfig{Preset: "unknown", Mode: "unknown", SurfaceOpacity: 100}
	normalizeConfig(&cfg)
	if cfg.Theme.Preset != "mint" || cfg.Theme.Mode != "system" {
		t.Fatalf("invalid theme did not fall back to defaults: %#v", cfg.Theme)
	}
	if cfg.Theme.SurfaceOpacity != 100 {
		t.Fatalf("surface opacity was not retained: %#v", cfg.Theme)
	}

	cfg.Theme = ThemeConfig{Preset: "custom", Mode: "light", Primary: "invalid", Accent: "#12345", SurfaceOpacity: 100}
	normalizeConfig(&cfg)
	if cfg.Theme.Primary != "#14B8A6" || cfg.Theme.Accent != "#F97316" {
		t.Fatalf("invalid custom colors did not fall back to defaults: %#v", cfg.Theme)
	}
	if cfg.Theme.FontScale != 100 {
		t.Fatalf("missing font scale did not fall back to default: %#v", cfg.Theme)
	}
	if cfg.Theme.SurfaceOpacity != 100 {
		t.Fatalf("surface opacity was not retained: %#v", cfg.Theme)
	}

	cfg.Theme.FontScale = 200
	normalizeConfig(&cfg)
	if cfg.Theme.FontScale != 150 {
		t.Fatalf("font scale was not clamped: %#v", cfg.Theme)
	}

	cfg.Theme.SurfaceOpacity = 200
	normalizeConfig(&cfg)
	if cfg.Theme.SurfaceOpacity != 100 {
		t.Fatalf("surface opacity was not clamped to max: %#v", cfg.Theme)
	}

	cfg.Theme.SurfaceOpacity = -10
	normalizeConfig(&cfg)
	if cfg.Theme.SurfaceOpacity != 0 {
		t.Fatalf("surface opacity was not clamped to min: %#v", cfg.Theme)
	}

	cfg.Theme.SurfaceOpacity = 0
	normalizeConfig(&cfg)
	if cfg.Theme.SurfaceOpacity != 0 {
		t.Fatalf("explicit zero surface opacity was not retained: %#v", cfg.Theme)
	}

	cfg.Theme = ThemeConfig{Preset: "mint", Mode: "light", SurfaceOpacity: 100,
		Layout: "diagonal", PreviewLines: 99, NavOpacity: 140, NavBlur: 99,
		HistoryColumns: "6", HistoryForceColumns: true}
	normalizeConfig(&cfg)
	if cfg.Theme.Layout != "vertical" {
		t.Fatalf("invalid layout did not fall back to default: %#v", cfg.Theme)
	}
	if cfg.Theme.PreviewLines != 20 || cfg.Theme.NavOpacity != 100 || cfg.Theme.NavBlur != 32 {
		t.Fatalf("new theme fields were not clamped: %#v", cfg.Theme)
	}
	if cfg.Theme.HistoryColumns != "auto" {
		t.Fatalf("invalid history columns did not fall back to auto: %#v", cfg.Theme)
	}
	if cfg.Theme.HistoryForceColumns {
		t.Fatalf("force columns must be false when columns fall back to auto: %#v", cfg.Theme)
	}

	// auto + force = 无效组合：normalize 后 force 必须为 false，不得报错（spec §2.2）
	cfg.Theme.HistoryColumns = "auto"
	cfg.Theme.HistoryForceColumns = true
	normalizeConfig(&cfg)
	if cfg.Theme.HistoryForceColumns {
		t.Fatalf("force columns must be cleared in auto mode: %#v", cfg.Theme)
	}

	// 合法固定列数 + force 原样保留
	cfg.Theme.HistoryColumns = "3"
	cfg.Theme.HistoryForceColumns = true
	normalizeConfig(&cfg)
	if cfg.Theme.HistoryColumns != "3" || !cfg.Theme.HistoryForceColumns {
		t.Fatalf("valid forced columns were altered by normalize: %#v", cfg.Theme)
	}
}

// theme 的全部字段（含 layout/historyColumns/historyForceColumns/previewLines 等）都是
// 严格配置契约的一部分：缺失任意一个都视为配置损坏，绝不静默回退。
func TestThemeConfigRequiresAllFields(t *testing.T) {
	data, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(data, &asMap); err != nil {
		t.Fatal(err)
	}
	themeMap, ok := asMap["theme"].(map[string]any)
	if !ok {
		t.Fatalf("theme missing in marshaled config: %#v", asMap)
	}
	for _, key := range []string{"preset", "mode", "primary", "accent", "fontScale", "surfaceOpacity",
		"layout", "historyColumns", "historyForceColumns", "previewLines", "navOpacity", "navBlur", "navBorder", "navSticky"} {
		value, present := themeMap[key]
		if !present {
			t.Fatalf("default config JSON missing theme.%s", key)
		}
		delete(themeMap, key)
		trimmed, err := json.Marshal(asMap)
		if err != nil {
			t.Fatal(err)
		}
		var cfg Config
		if err := cfg.UnmarshalJSON(trimmed); err == nil {
			t.Fatalf("config without theme.%s must be rejected as corrupt", key)
		}
		themeMap[key] = value
	}
}

func TestThemeConfigPersists(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	cfg := DefaultConfig()
	cfg.Theme = ThemeConfig{
		Preset:              "custom",
		Mode:                "light",
		Primary:             "#2563EB",
		Accent:              "#F59E0B",
		FontScale:           135,
		SurfaceOpacity:      72,
		Layout:              "sidebar",
		HistoryColumns:      "3",
		HistoryForceColumns: true,
		PreviewLines:        5,
		NavOpacity:          40,
		NavBlur:             8,
		NavBorder:           false,
	}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	got, err := store.GetConfig()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got.Theme != cfg.Theme {
		t.Fatalf("persisted theme mismatch: got %#v, want %#v", got.Theme, cfg.Theme)
	}
}

func TestThemeConfigManagerUpdate(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	manager, err := NewConfigManager(store)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}

	want := ThemeConfig{Preset: "custom", Mode: "dark", Primary: "#2563EB", Accent: "#F59E0B", FontScale: 135, SurfaceOpacity: 72,
		Layout: "sidebar", HistoryColumns: "4", HistoryForceColumns: true, PreviewLines: 3, NavOpacity: 55, NavBlur: 24, NavBorder: false}
	var notified ThemeConfig
	manager.OnChange(func(cfg Config) error {
		notified = cfg.Theme
		return nil
	})

	cfg := manager.Get()
	cfg.Theme = want
	if err := manager.Update(cfg); err != nil {
		t.Fatalf("update config: %v", err)
	}
	if got := manager.Get().Theme; got != want {
		t.Fatalf("in-memory theme = %#v, want %#v", got, want)
	}
	if notified != want {
		t.Fatalf("listener theme = %#v, want %#v", notified, want)
	}
	persisted, err := store.GetConfig()
	if err != nil {
		t.Fatalf("get persisted config: %v", err)
	}
	if persisted.Theme != want {
		t.Fatalf("persisted theme = %#v, want %#v", persisted.Theme, want)
	}
}
