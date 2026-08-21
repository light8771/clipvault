package clipvault

import (
	"context"
	"testing"
)

func TestConfigChangeLogRoundTrip(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	entries, total, err := store.ListConfigChanges(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(entries) != 0 {
		t.Fatalf("expected empty log, got total=%d entries=%d", total, len(entries))
	}

	// 空变更不落库。
	if err := store.LogConfigChanges(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := store.ListConfigChanges(ctx, 20, 0); total != 0 {
		t.Fatalf("empty changes must not be persisted, total=%d", total)
	}

	changes := []ConfigChange{
		{Path: "theme.mode", Label: "显示模式", Old: "system", New: "dark"},
		{Path: "maxTextChars", Label: "单条文本最大长度", Old: "10000", New: "20000"},
	}
	if err := store.LogConfigChanges(ctx, changes); err != nil {
		t.Fatal(err)
	}
	if err := store.LogConfigChanges(ctx, []ConfigChange{{Path: "autostart.enabled", Label: "开机自启", Old: "关闭", New: "开启"}}); err != nil {
		t.Fatal(err)
	}

	entries, total, err = store.ListConfigChanges(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(entries) != 2 {
		t.Fatalf("expected 2 entries, got total=%d entries=%d", total, len(entries))
	}
	// 倒序：最新一条在最前。
	if len(entries[0].Changes) != 1 || entries[0].Changes[0].Path != "autostart.enabled" {
		t.Fatalf("unexpected newest entry: %+v", entries[0])
	}
	if len(entries[1].Changes) != 2 || entries[1].Changes[0].Old != "system" || entries[1].Changes[1].New != "20000" {
		t.Fatalf("unexpected first entry changes: %+v", entries[1].Changes)
	}
	if entries[0].ChangedAt.IsZero() {
		t.Fatal("ChangedAt must be populated")
	}

	if err := store.ClearConfigChanges(ctx); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := store.ListConfigChanges(ctx, 20, 0); total != 0 {
		t.Fatalf("clear failed, total=%d", total)
	}
}

func TestSearchHistoryDedupeAndClear(t *testing.T) {
	store, err := OpenStore(StoreOptions{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.LogSearchQuery(ctx, "alpha", map[string]string{}, 3); err != nil {
		t.Fatal(err)
	}
	// 相同关键词 + 相同筛选：只刷新上一条，不新增。
	if err := store.LogSearchQuery(ctx, "alpha", map[string]string{}, 5); err != nil {
		t.Fatal(err)
	}
	// 筛选不同：新增一条。
	if err := store.LogSearchQuery(ctx, "alpha", map[string]string{"类型": "文本"}, 1); err != nil {
		t.Fatal(err)
	}
	// 空筛选 map 与 nil 等价（nil 归一为 {}），再次去重需按相同 filters 才算重复。
	if err := store.LogSearchQuery(ctx, "beta", nil, 0); err != nil {
		t.Fatal(err)
	}

	entries, total, err := store.ListSearchHistory(ctx, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(entries) != 3 {
		t.Fatalf("expected 3 entries, got total=%d entries=%d", total, len(entries))
	}
	if entries[0].Query != "beta" {
		t.Fatalf("newest entry should be beta, got %q", entries[0].Query)
	}
	if entries[1].Filters["类型"] != "文本" {
		t.Fatalf("filters lost: %+v", entries[1].Filters)
	}
	if entries[2].ResultCount != 5 {
		t.Fatalf("dedupe must refresh result count, got %d", entries[2].ResultCount)
	}

	if err := store.ClearSearchHistory(ctx); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := store.ListSearchHistory(ctx, 20, 0); total != 0 {
		t.Fatalf("clear failed, total=%d", total)
	}
}

func TestDiffConfigChanges(t *testing.T) {
	oldCfg := DefaultConfig()
	newCfg := oldCfg
	newCfg.Theme.Mode = "dark"
	newCfg.MaxTextChars = oldCfg.MaxTextChars + 1
	newCfg.Autostart.Enabled = !oldCfg.Autostart.Enabled
	newCfg.Backup.DirBackup.Password = "new-secret"
	newCfg.Brand.LogoVersion = oldCfg.Brand.LogoVersion + 1 // 资产元数据应被跳过

	changes := diffConfigChanges(oldCfg, newCfg)
	byPath := map[string]ConfigChange{}
	for _, c := range changes {
		byPath[c.Path] = c
	}
	// 操作记录持久化词条 key（config_field.* / config_value.*），
	// 由前端展示时翻译。
	if c, ok := byPath["theme.mode"]; !ok || c.Label != "config_field.theme_mode" || c.New != "dark" {
		t.Fatalf("theme.mode change missing or wrong: %+v", c)
	}
	if c, ok := byPath["maxTextChars"]; !ok || c.Label != "config_field.max_text_chars" {
		t.Fatalf("maxTextChars change missing or wrong: %+v", c)
	}
	if c, ok := byPath["autostart.enabled"]; !ok || c.Old != "config_value.off" || c.New != "config_value.on" {
		t.Fatalf("autostart.enabled bool formatting wrong: %+v", c)
	}
	if c, ok := byPath["backup.dirBackup.password"]; !ok || c.Old != "config_value.not_set" || c.New != "config_value.set" {
		t.Fatalf("password change must be masked: %+v", c)
	}
	if _, ok := byPath["brand.logoVersion"]; ok {
		t.Fatal("asset metadata must be skipped")
	}
	if len(changes) != 4 {
		t.Fatalf("expected 4 changes, got %d: %+v", len(changes), changes)
	}
}

func TestDiffConfigChangesIdentical(t *testing.T) {
	cfg := DefaultConfig()
	normalizeConfig(&cfg)
	if changes := diffConfigChanges(cfg, cfg); len(changes) != 0 {
		t.Fatalf("identical configs must produce no changes: %+v", changes)
	}
}
