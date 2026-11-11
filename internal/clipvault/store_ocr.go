package clipvault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"clipboard_ext/internal/clipvault/domain"
)

// updateImageOCR 使用比较并交换，避免识别结束覆盖并发修订或复活已删除的记录。
func (s *Store) updateImageOCR(ctx context.Context, id int64, update func([]domain.ImageOCR) ([]domain.ImageOCR, error)) error {
	for attempt := 0; attempt < 4; attempt++ {
		var raw string
		if err := s.db.QueryRowContext(ctx, `SELECT CAST(ocr_json AS VARCHAR) FROM clipboard_items WHERE id = ? AND record_type = 'content'`, id).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrItemNotFound
			}
			return err
		}
		var records []domain.ImageOCR
		if err := json.Unmarshal([]byte(raw), &records); err != nil {
			return err
		}
		records, err := update(records)
		if err != nil {
			return err
		}
		if err := domain.ValidateOCR(records, domain.HistoryRecordContent); err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE clipboard_items SET ocr_json = ? WHERE id = ? AND CAST(ocr_json AS VARCHAR) = ?`, domain.OCRJSON(records), id, raw)
		if err != nil {
			tx.Rollback()
			return err
		}
		if count, err := result.RowsAffected(); err != nil {
			tx.Rollback()
			return err
		} else if count == 1 {
			_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES ('ocr.revision', to_json(nextval('seq_ocr_revision'))) ON CONFLICT(key) DO UPDATE SET value=excluded.value`)
			if err != nil {
				tx.Rollback()
				return err
			}
			return tx.Commit()
		}
		tx.Rollback()
	}
	return errors.New("识别结果正在被修改，请重试")
}

func (s *Store) cachedImageOCR(ctx context.Context, key string) (domain.ImageOCR, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT CAST(o.value AS VARCHAR) FROM clipboard_items i, json_each(i.ocr_json) o WHERE json_extract_string(o.value, '$.cacheKey') = ? ORDER BY CAST(json_extract_string(o.value, '$.recognizedAt') AS BIGINT) DESC LIMIT 1`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ImageOCR{}, false, nil
	}
	if err != nil {
		return domain.ImageOCR{}, false, err
	}
	var record domain.ImageOCR
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		return record, false, err
	}
	record.Edited = nil
	return record, true, nil
}

func (s *Store) ocrRevision(ctx context.Context) (int64, error) {
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT CAST(value AS BIGINT) FROM settings WHERE key='ocr.revision'`).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, err
}
