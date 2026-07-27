package clipvault

import "testing"

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

	cfg.Theme = ThemeConfig{Preset: "unknown", Mode: "unknown"}
	normalizeConfig(&cfg)
	if cfg.Theme.Preset != "mint" || cfg.Theme.Mode != "system" {
		t.Fatalf("invalid theme did not fall back to defaults: %#v", cfg.Theme)
	}
	if cfg.Theme.SurfaceOpacity != 100 {
		t.Fatalf("missing surface opacity did not fall back to default: %#v", cfg.Theme)
	}

	cfg.Theme = ThemeConfig{Preset: "custom", Mode: "light", Primary: "invalid", Accent: "#12345"}
	normalizeConfig(&cfg)
	if cfg.Theme.Primary != "#14B8A6" || cfg.Theme.Accent != "#F97316" {
		t.Fatalf("invalid custom colors did not fall back to defaults: %#v", cfg.Theme)
	}
	if cfg.Theme.FontScale != 100 {
		t.Fatalf("missing font scale did not fall back to default: %#v", cfg.Theme)
	}
	if cfg.Theme.SurfaceOpacity != 100 {
		t.Fatalf("missing surface opacity did not fall back to default: %#v", cfg.Theme)
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
}

func TestThemeConfigPersists(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	cfg := DefaultConfig()
	cfg.Theme = ThemeConfig{
		Preset:         "custom",
		Mode:           "light",
		Primary:        "#2563EB",
		Accent:         "#F59E0B",
		FontScale:      135,
		SurfaceOpacity: 72,
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

	want := ThemeConfig{Preset: "custom", Mode: "dark", Primary: "#2563EB", Accent: "#F59E0B", FontScale: 135, SurfaceOpacity: 72}
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
