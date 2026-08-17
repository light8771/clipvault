package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Save 写入一条剪贴板事件记录。created_at 使用事件发生时间
// （item.CapturedAtMS，Unix 毫秒）；<=0 时回退为当前时间。
// 数据库写入时间永远不被当作剪贴板事件时间。
func (s *Store) Save(ctx context.Context, item ClipboardItem) error {
	if !validItemKindFormat(item.Kind, item.Format) {
		return fmt.Errorf("invalid clipboard kind/format %q/%q", item.Kind, item.Format)
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

// AddEmailBackupHistory records a successful backup email. History is append-only.
func (s *Store) AddEmailBackupHistory(ctx context.Context, record EmailBackupHistory) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO email_backup_history
		(sent_at, mode, from_address, to_address, format) VALUES (?, ?, ?, ?, ?)`,
		record.SentAt.UTC().UnixMilli(), record.Mode, record.From, record.To, record.Format)
	return err
}

// ListEmailBackupHistory returns the most recent successful backup emails.
func (s *Store) ListEmailBackupHistory(ctx context.Context, limit int) ([]EmailBackupHistory, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sent_at, mode, from_address, to_address, format
		FROM email_backup_history ORDER BY sent_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	history := make([]EmailBackupHistory, 0)
	for rows.Next() {
		var record EmailBackupHistory
		var sentAtMS int64
		if err := rows.Scan(&sentAtMS, &record.Mode, &record.From, &record.To, &record.Format); err != nil {
			return nil, err
		}
		record.SentAt = time.UnixMilli(sentAtMS).UTC()
		history = append(history, record)
	}
	return history, rows.Err()
}
