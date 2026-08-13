package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Store) Save(ctx context.Context, item ClipboardItem) error {
	if !validItemKindFormat(item.Kind, item.Format) {
		return fmt.Errorf("invalid clipboard kind/format %q/%q", item.Kind, item.Format)
	}
	item.ByteLen = len([]byte(item.Text))
	item.CharLen = textRuneLen(item.Text)
	item.SHA256 = hashString(item.Text)
	if item.MetaJSON == "" {
		item.MetaJSON = "{}"
	} else if !json.Valid([]byte(item.MetaJSON)) {
		return errors.New("meta_json must be valid JSON")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO clipboard_items
		(created_at, kind, format, text, byte_len, char_len, sha256, meta_json, sensitive,
		 source_exe_name, source_exe_path, source_window_title, source_method)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano),
		item.Kind,
		item.Format,
		item.Text,
		item.ByteLen,
		item.CharLen,
		item.SHA256,
		item.MetaJSON,
		item.Sensitive,
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
		record.SentAt.UTC().Format(emailBackupHistoryTimeLayout), record.Mode, record.From, record.To, record.Format)
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
		var sentAt string
		if err := rows.Scan(&sentAt, &record.Mode, &record.From, &record.To, &record.Format); err != nil {
			return nil, err
		}
		var err error
		record.SentAt, err = time.Parse(emailBackupHistoryTimeLayout, sentAt)
		if err != nil {
			return nil, fmt.Errorf("parse email backup history timestamp: %w", err)
		}
		history = append(history, record)
	}
	return history, rows.Err()
}
