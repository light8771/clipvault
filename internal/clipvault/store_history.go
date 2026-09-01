package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"clipboard_ext/internal/clipvault/domain"
)

// 单条备份历史的错误信息长度上限，防止超长底层错误撑爆历史表。
const maxBackupHistoryErrorRunes = 500

// Save 写入一条剪贴板事件记录。created_at 使用事件发生时间
// （item.CapturedAtMS，Unix 毫秒）；<=0 时回退为当前时间。
// 数据库写入时间永远不被当作剪贴板事件时间。
func (s *Store) Save(ctx context.Context, item ClipboardItem) error {
	if !domain.ValidItemState(item.Kind, item.Format, item.UnavailableReason) {
		return fmt.Errorf("invalid clipboard state kind=%q format=%q unavailable_reason=%q",
			item.Kind, item.Format, item.UnavailableReason)
	}
	item.ByteLen = len([]byte(item.Text))
	item.CharLen = textRuneLen(item.Text)
	item.SHA256 = hashString(item.Text)
	// ContentAvailable 由 UnavailableReason 派生（单一事实来源）：
	// 有原因 = 内容不可用；无原因 = 内容可用。
	item.ContentAvailable = item.UnavailableReason == ""
	if item.MetaJSON == "" {
		item.MetaJSON = "{}"
	} else if !json.Valid([]byte(item.MetaJSON)) {
		return errors.New("meta_json must be valid JSON")
	}
	capturedAtMS := item.CapturedAtMS
	if capturedAtMS <= 0 {
		capturedAtMS = time.Now().UTC().UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO clipboard_items
		(created_at, kind, format, text, byte_len, char_len, sha256, meta_json, sensitive,
		 content_available, is_duplicate, unavailable_reason,
		 source_exe_name, source_exe_path, source_window_title, source_method)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		capturedAtMS,
		item.Kind,
		item.Format,
		item.Text,
		item.ByteLen,
		item.CharLen,
		item.SHA256,
		item.MetaJSON,
		item.Sensitive,
		item.ContentAvailable,
		item.IsDuplicate,
		item.UnavailableReason,
		item.SourceExeName,
		item.SourceExePath,
		item.SourceWindowTitle,
		item.SourceMethod,
	)
	return err
}

// AddBackupHistory 追加一条备份历史（成功、失败、跳过都记录）。append-only。
func (s *Store) AddBackupHistory(ctx context.Context, record BackupHistory) error {
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	errText := record.Error
	if utf8.RuneCountInString(errText) > maxBackupHistoryErrorRunes {
		errText = string([]rune(errText)[:maxBackupHistoryErrorRunes])
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO backup_history
		(created_at, kind, trigger, mode, ok, error, target, format, parts, items)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		createdAt.UTC().UnixMilli(), record.Kind, record.Trigger, record.Mode, record.OK,
		errText, record.Target, record.Format, record.Parts, record.Items)
	return err
}

// ListBackupHistory 返回指定通道最近的备份历史（新的在前）。
func (s *Store) ListBackupHistory(ctx context.Context, kind string, limit int) ([]BackupHistory, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT created_at, kind, trigger, mode, ok, error, target, format, parts, items
		FROM backup_history WHERE kind = ? ORDER BY created_at DESC, id DESC LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]BackupHistory, 0)
	for rows.Next() {
		var record BackupHistory
		var createdAtMS int64
		if err := rows.Scan(&createdAtMS, &record.Kind, &record.Trigger, &record.Mode, &record.OK,
			&record.Error, &record.Target, &record.Format, &record.Parts, &record.Items); err != nil {
			return nil, err
		}
		record.CreatedAt = time.UnixMilli(createdAtMS).UTC()
		history = append(history, record)
	}
	return history, rows.Err()
}
