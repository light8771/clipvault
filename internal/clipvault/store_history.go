package clipvault

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"clipboard_ext/internal/clipvault/domain"
)

// 单条备份历史的错误信息长度上限，防止超长底层错误撑爆历史表。
const maxBackupHistoryErrorRunes = 500

// Save 写入一条剪贴板历史记录。created_at 使用事件发生时间
// （item.CapturedAtMS，Unix 毫秒）；<=0 时回退为当前时间。
// 数据库写入时间永远不被当作剪贴板事件时间。
func (s *Store) Save(ctx context.Context, item ClipboardItem) error {
	switch item.SourceAttribution {
	case "":
		item.SourceAttribution = "unknown"
	default:
		if !validSourceAttribution(item.SourceAttribution) {
			return fmt.Errorf("invalid source attribution %q", item.SourceAttribution)
		}
	}
	var contentHash any
	var contentHashValue string
	var byteLen, charLen int
	var eventCode any
	var eventFingerprint any
	switch item.RecordType {
	case domain.HistoryRecordContent:
		if strings.IndexByte(item.Text, 0) >= 0 {
			return errors.New("clipboard content must not contain an embedded NUL")
		}
		if !domain.ValidContentKindFormat(item.Kind, item.Format) || item.EventCode != "" ||
			item.CaptureSequence != 0 || item.CaptureStage != "" || item.CaptureAttempts != 0 ||
			len(item.EventFormats) != 0 || item.ErrorCategory != "" {
			return fmt.Errorf("invalid content record kind=%q format=%q", item.Kind, item.Format)
		}
		if item.Format == domain.FormatComposite {
			text, size, err := validateComposite(item.Content)
			if err != nil {
				return err
			}
			if text != item.Text {
				return errors.New("composite search text mismatch")
			}
			byteLen, charLen = size, textRuneLen(text)
			contentHashValue = compositeFingerprint(item.Content)
		} else if item.Content != nil {
			return errors.New("atomic content cannot contain a composite package")
		} else if item.Format == domain.FormatImageFile {
			if item.ByteLen <= 0 || item.CharLen != 0 {
				return errors.New("managed image requires a positive byte length and zero character length")
			}
			if err := validateManagedImageStoredPath(item.Text); err != nil {
				return fmt.Errorf("invalid managed image path: %w", err)
			}
			byteLen = item.ByteLen
			charLen = 0
			contentHashValue = item.ContentSHA256
			if contentHashValue == "" {
				return errors.New("managed image requires content_sha256")
			}
		} else {
			byteLen = len([]byte(item.Text))
			charLen = textRuneLen(item.Text)
			contentHashValue = hashString(item.Text)
		}
		if item.ContentSHA256 != "" && item.ContentSHA256 != contentHashValue {
			return errors.New("content_sha256 does not match content")
		}
		if !validContentSHA256(contentHashValue) {
			return errors.New("content_sha256 must be a lowercase SHA-256 digest")
		}
		contentHash = contentHashValue
	case domain.HistoryRecordEvent:
		if item.Content != nil {
			return errors.New("event cannot contain a composite package")
		}
		if !domain.ValidEventCode(item.EventCode) {
			return fmt.Errorf("invalid event record code=%q", item.EventCode)
		}
		if item.Kind != "" || item.Format != "" || item.Text != "" || item.ContentSHA256 != "" ||
			item.Starred || item.Sensitive || item.CaptureAttempts < 0 {
			return errors.New("event record has invalid content state or capture attempts")
		}
		contentHash = nil
		eventCode = string(item.EventCode)
		eventFingerprint = buildEventFingerprintFromInfo(item.EventCode, item.EventFormats,
			item.CaptureStage, item.ErrorCategory, item.SourceExeName)
	default:
		return fmt.Errorf("invalid record type %q", item.RecordType)
	}
	if item.MetaJSON == "" {
		item.MetaJSON = "{}"
	} else if !json.Valid([]byte(item.MetaJSON)) {
		return errors.New("meta_json must be valid JSON")
	}
	eventFormatsJSON := encodeClipboardFormats(item.EventFormats)
	capturedAtMS := item.CapturedAtMS
	if capturedAtMS <= 0 {
		capturedAtMS = time.Now().UTC().UnixMilli()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	consecutiveDuplicate := false
	if item.RecordType == domain.HistoryRecordContent {
		var previousKind, previousFormat, previousHash string
		err = tx.QueryRowContext(ctx, `SELECT kind, format, content_sha256 FROM clipboard_items
			WHERE record_type = 'content' ORDER BY id DESC LIMIT 1`).Scan(&previousKind, &previousFormat, &previousHash)
		switch {
		case err == nil:
			consecutiveDuplicate = previousKind == item.Kind && previousFormat == item.Format && previousHash == contentHashValue
		case errors.Is(err, sql.ErrNoRows):
			err = nil
		default:
			return err
		}
	}

	contentTypes, err := historyContentTypesJSON(item.Format, item.Text, item.Content)
	if err != nil {
		return err
	}
	contentJSON, err := encodeComposite(item.Content)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO clipboard_items
		(created_at, record_type, kind, format, text, byte_len, char_len, content_sha256, consecutive_duplicate,
		 meta_json, starred, sensitive, event_code, event_fingerprint, capture_sequence,
		 capture_stage, capture_attempts, event_formats, error_category,
		 source_exe_name, source_exe_path, source_window_title, source_attribution, content_json, content_types)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		capturedAtMS,
		item.RecordType,
		item.Kind,
		item.Format,
		item.Text,
		byteLen,
		charLen,
		contentHash,
		consecutiveDuplicate,
		item.MetaJSON,
		item.Starred,
		item.Sensitive,
		eventCode,
		eventFingerprint,
		item.CaptureSequence,
		item.CaptureStage,
		item.CaptureAttempts,
		eventFormatsJSON,
		item.ErrorCategory,
		item.SourceExeName,
		item.SourceExePath,
		item.SourceWindowTitle,
		item.SourceAttribution,
		contentJSON,
		contentTypes,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func validContentSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
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
