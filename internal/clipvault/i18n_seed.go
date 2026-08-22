package clipvault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// I18nSchemaVersion 是官方词条集的版本。新增/修改/移除官方词条时递增；
// 它与表结构版本（duckdbstorage.SchemaVersion）解耦——词条演进不需要
// 重建数据库，只在启动时按标记增量同步。
const I18nSchemaVersion = 2

// i18nSeedVersionSettingKey 是 settings 表中记录已同步词条集版本的键。
// 已同步的库每次启动只花一次 settings 读，避免全量 upsert 的启动开销。
const i18nSeedVersionSettingKey = "i18n.seed_version"

// BuiltinTranslation 是一条官方词条的完整定义。开发者新增用户可见文案时
// 只维护这一个结构（Key + 模块 + 说明 + 中英文），启动时由 ensureI18nSeed
// 幂等入库。
type BuiltinTranslation struct {
	Key         string
	Module      string
	Description string
	ZhCN        string
	EnUS        string
}

// BuiltinTranslations 是全量官方词条。Key 是 UI 与多语言系统之间的唯一
// 稳定契约，一经发布不可变更；文案可随版本修正（Seed 覆盖官方译文）。

// BuiltinTranslations 聚合全部模块词条（顺序不影响运行，仅保证 diff 稳定）。
var BuiltinTranslations = slices.Concat(
	i18nSeedCore, i18nSeedSettings, i18nSeedStorage, i18nSeedAppearance, i18nSeedHistory, i18nSeedChannels, i18nSeedI18n, i18nSeedConfig,
)

// builtinI18nLanguages 返回官方语言定义。base 仅供编辑页参考展示；
// 运行时 fallback 链固定为 当前语言 → base → en-US → zh-CN → Key。
func builtinI18nLanguages() []I18nLanguage {
	return []I18nLanguage{
		{Code: I18nChinese, Name: "Simplified Chinese", NativeName: "简体中文", BaseLanguageCode: I18nEnglish, IsBuiltin: true, IsEnabled: true},
		{Code: I18nEnglish, Name: "English", NativeName: "English", BaseLanguageCode: I18nChinese, IsBuiltin: true, IsEnabled: true},
	}
}

// ensureI18nSeed 幂等地将官方语言数据同步到 BuiltinTranslations 当前版本：
//
//  1. upsert 官方语言定义（zh-CN / en-US）；
//  2. upsert 全部官方 Key（冲突时保留首见 source_version，退役后可回归）；
//  3. 覆盖官方 zh-CN / en-US 译文（官方维护语义；用户无权修改官方翻译）；
//  4. 退役不在源码中的 active Key（is_active=false，不删行）；
//  5. 写入 i18n.seed_version 标记——已同步的库启动只花一次 settings 读。
//
// 用户自定义语言与其译文永不触碰。整个同步在单事务内完成。
func (s *Store) ensureI18nSeed() error {
	version, err := s.readI18nSeedVersion()
	if err != nil {
		return fmt.Errorf("read i18n seed version: %w", err)
	}
	if version == I18nSchemaVersion {
		return nil
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, lang := range builtinI18nLanguages() {
		if err := upsertI18nLanguage(ctx, tx, lang); err != nil {
			return fmt.Errorf("seed builtin language %s: %w", lang.Code, err)
		}
	}
	keyDefs := make([]I18nKeyDef, 0, len(BuiltinTranslations))
	zhEntries := make([]I18nKeyText, 0, len(BuiltinTranslations))
	enEntries := make([]I18nKeyText, 0, len(BuiltinTranslations))
	keep := make(map[string]struct{}, len(BuiltinTranslations))
	for _, entry := range BuiltinTranslations {
		keyDefs = append(keyDefs, I18nKeyDef{Key: entry.Key, Module: entry.Module, Description: entry.Description, SourceVersion: I18nSchemaVersion})
		zhEntries = append(zhEntries, I18nKeyText{Key: entry.Key, Value: entry.ZhCN})
		enEntries = append(enEntries, I18nKeyText{Key: entry.Key, Value: entry.EnUS})
		keep[entry.Key] = struct{}{}
	}
	if err := upsertI18nKeys(ctx, tx, keyDefs); err != nil {
		return err
	}
	if err := upsertI18nTranslations(ctx, tx, I18nChinese, zhEntries); err != nil {
		return err
	}
	if err := upsertI18nTranslations(ctx, tx, I18nEnglish, enEntries); err != nil {
		return err
	}
	if _, err := retireI18nKeys(ctx, tx, keep); err != nil {
		return err
	}
	marker, err := json.Marshal(I18nSchemaVersion)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		i18nSeedVersionSettingKey, string(marker)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// readI18nSeedVersion 读取已同步的词条集版本；标记缺失视为 0（需要 Seed）。
func (s *Store) readI18nSeedVersion() (int, error) {
	var raw string
	err := s.db.QueryRow(`SELECT json_extract_string(value, '$') FROM settings WHERE key = ?`,
		i18nSeedVersionSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	version, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("parse i18n seed version %q: %w", raw, err)
	}
	return version, nil
}
