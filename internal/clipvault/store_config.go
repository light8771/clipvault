package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrConfigCorrupt 表示数据库可正常打开，但配置 JSON 已损坏。调用方必须
// fail-closed，并通过恢复密码确认后才可回退到最近一次有效配置。
var ErrConfigCorrupt = errors.New("configuration is corrupt")

// GetConfig 从 settings 表读取配置。新库初始化时已写入默认配置，
// 因此缺失或损坏都是需要恢复密码介入的完整性错误。
func (s *Store) GetConfig() (Config, error) {
	return getConfig(context.Background(), s.db)
}

func getConfig(ctx context.Context, queryer storeQueryer) (Config, error) {
	var cfg Config
	var jsonStr string
	err := queryer.QueryRowContext(ctx, `SELECT CAST(value AS VARCHAR) FROM settings WHERE key = 'config'`).Scan(&jsonStr)
	if err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrConfigCorrupt, err)
	}
	if err := decodeStrictJSON([]byte(jsonStr), &cfg); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrConfigCorrupt, err)
	}
	normalizeConfig(&cfg)
	if err := validateConfig(cfg); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrConfigCorrupt, err)
	}
	return cfg, nil
}

// SaveConfig 将配置持久化到 settings 表。
func (s *Store) SaveConfig(cfg Config) error {
	normalizeConfig(&cfg)
	if err := validateConfig(cfg); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, key := range []string{"config", "config.last_good"} {
		if _, err := tx.Exec(
			`INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RestoreLastGoodConfig 用最近一次成功保存的配置修复主配置。
// 调用方负责在调用前验证恢复密码。
func (s *Store) RestoreLastGoodConfig() (Config, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT CAST(value AS VARCHAR) FROM settings WHERE key = 'config.last_good'`).Scan(&raw); err != nil {
		return Config{}, fmt.Errorf("read last-known-good configuration: %w", err)
	}
	var cfg Config
	if err := decodeStrictJSON([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("last-known-good configuration is corrupt: %w", err)
	}
	normalizeConfig(&cfg)
	if err := validateConfig(cfg); err != nil {
		return Config{}, fmt.Errorf("last-known-good configuration is corrupt: %w", err)
	}
	if err := s.SaveConfig(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func exportAll(ctx context.Context, queryer storeQueryer) ([]StoredItem, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT `+clipboardItemColumns+`
		FROM clipboard_items ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []StoredItem
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
