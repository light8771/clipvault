package clipvault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ConfigChange 描述一次设置修改中单个配置项的新旧值变化。
type ConfigChange struct {
	Path  string `json:"path"`  // 点分配置路径，如 theme.mode
	Label string `json:"label"` // 中文展示名
	Old   string `json:"old"`   // 修改前的值（已格式化、已脱敏）
	New   string `json:"new"`   // 修改后的值（已格式化、已脱敏）
}

// ConfigChangeLogEntry 是一次设置保存产生的变更记录（可包含多个配置项）。
type ConfigChangeLogEntry struct {
	ID        int64          `json:"id"`
	ChangedAt time.Time      `json:"changedAt"`
	Changes   []ConfigChange `json:"changes"`
}

// SearchHistoryEntry 是一条剪贴板搜索历史。
type SearchHistoryEntry struct {
	ID          int64             `json:"id"`
	SearchedAt  time.Time         `json:"searchedAt"`
	Query       string            `json:"query"`
	Filters     map[string]string `json:"filters"`
	ResultCount int64             `json:"resultCount"`
}

// 审计表保留上限：超出后按 id 淘汰最旧记录，避免无界增长。
const (
	maxConfigChangeLogRows = 1000
	maxSearchHistoryRows   = 1000
)

// LogConfigChanges 写入一条设置修改记录；changes 为空时不落库。
func (s *Store) LogConfigChanges(ctx context.Context, changes []ConfigChange) error {
	if len(changes) == 0 {
		return nil
	}
	data, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO config_change_log (changed_at, changes) VALUES (?, ?)`,
		time.Now().UTC().UnixMilli(), string(data)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM config_change_log WHERE id NOT IN (
			SELECT id FROM config_change_log ORDER BY id DESC LIMIT ?)`, maxConfigChangeLogRows); err != nil {
		return err
	}
	return tx.Commit()
}

// ListConfigChanges 按时间倒序返回设置修改记录与总数。
func (s *Store) ListConfigChanges(ctx context.Context, limit, offset int) ([]ConfigChangeLogEntry, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM config_change_log`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, changed_at, CAST(changes AS VARCHAR) FROM config_change_log
		ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	entries := []ConfigChangeLogEntry{}
	for rows.Next() {
		var entry ConfigChangeLogEntry
		var changedAtMS int64
		var raw string
		if err := rows.Scan(&entry.ID, &changedAtMS, &raw); err != nil {
			return nil, 0, err
		}
		entry.ChangedAt = time.UnixMilli(changedAtMS).UTC()
		if err := json.Unmarshal([]byte(raw), &entry.Changes); err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	return entries, total, rows.Err()
}

// ClearConfigChanges 清空设置修改记录。
func (s *Store) ClearConfigChanges(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM config_change_log`)
	return err
}

// LogSearchQuery 记录一次剪贴板搜索。与上一条记录的关键词和筛选完全相同时，
// 仅刷新其时间与结果数（覆盖输入过程中的连续重复查询），否则插入新记录。
func (s *Store) LogSearchQuery(ctx context.Context, query string, filters map[string]string, resultCount int64) error {
	if filters == nil {
		filters = map[string]string{}
	}
	data, err := json.Marshal(filters)
	if err != nil {
		return err
	}
	nowMS := time.Now().UTC().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var lastID int64
	var lastQuery, lastFilters string
	err = tx.QueryRowContext(ctx,
		`SELECT id, query, CAST(filters AS VARCHAR) FROM search_history ORDER BY id DESC LIMIT 1`).
		Scan(&lastID, &lastQuery, &lastFilters)
	if err == nil && lastQuery == query && lastFilters == string(data) {
		_, err = tx.ExecContext(ctx,
			`UPDATE search_history SET searched_at = ?, result_count = ? WHERE id = ?`,
			nowMS, resultCount, lastID)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO search_history (searched_at, query, filters, result_count) VALUES (?, ?, ?, ?)`,
		nowMS, query, string(data), resultCount); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM search_history WHERE id NOT IN (
			SELECT id FROM search_history ORDER BY id DESC LIMIT ?)`, maxSearchHistoryRows); err != nil {
		return err
	}
	return tx.Commit()
}

// ListSearchHistory 按时间倒序返回搜索历史与总数。
func (s *Store) ListSearchHistory(ctx context.Context, limit, offset int) ([]SearchHistoryEntry, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM search_history`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, searched_at, query, CAST(filters AS VARCHAR), result_count FROM search_history
		ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	entries := []SearchHistoryEntry{}
	for rows.Next() {
		var entry SearchHistoryEntry
		var searchedAtMS int64
		var raw string
		if err := rows.Scan(&entry.ID, &searchedAtMS, &entry.Query, &raw, &entry.ResultCount); err != nil {
			return nil, 0, err
		}
		entry.SearchedAt = time.UnixMilli(searchedAtMS).UTC()
		if err := json.Unmarshal([]byte(raw), &entry.Filters); err != nil {
			return nil, 0, err
		}
		entries = append(entries, entry)
	}
	return entries, total, rows.Err()
}

// ClearSearchHistory 清空搜索历史。
func (s *Store) ClearSearchHistory(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM search_history`)
	return err
}
