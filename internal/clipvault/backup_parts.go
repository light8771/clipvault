package clipvault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"clipboard_ext/internal/clipvault/transfer"
)

// exportEmailParts exports data split into multiple self-contained parts (one per
// email). Each part is an independent ExportData over a row-count-sized id window,
// so it can be imported on its own. cvexport parts are individually encrypted.
//
// The returned maxID is the id of the LAST item actually included in the parts,
// computed from the same snapshot that produced the parts, so the caller can
// persist the backup watermark from the exported data (not from a later query —
// that would race with concurrent clipboard writes and silently skip rows).
// exportEmailPartFilesSized adaptively chooses the largest row count whose final
// attachment (including XLSX packaging or cvexport encryption) fits the
// configured final MIME size. A single oversized record is reported
// explicitly instead of producing a partially sent backup set.
type emailBackupPartFile struct {
	path string
	// endID 是本片覆盖的最大记录 id（含）。逐片发送成功后用它推进备份
	// 水位线：中途失败时已发出的片段不会重发，下次备份从断点续发。
	endID int64
}

func cleanupEmailPartFiles(parts []emailBackupPartFile) {
	for _, part := range parts {
		_ = os.Remove(part.path)
	}
}

// exportEmailPartFilesSized is the production email path. Attachments are
// staged on disk and later streamed one message at a time, so total backup size
// no longer multiplies heap usage.
func (bs *BackupService) exportEmailPartFilesSized(cfg EmailBackupConfig, scope ExportScope) (_ []emailBackupPartFile, ext string, maxID int64, resultErr error) {
	format := cfg.Format
	// 邮件备份永远产出加密容器：cvexport = 加密导出；json/xlsx = 明文文件
	// 打进 zip 后再用同一容器加密。附件扩展名恒为 cvexport。
	ext = "cvexport"
	if format != "json" && format != "xlsx" && format != "cvexport" {
		return nil, "", 0, fmt.Errorf("%s: %s", templateT("backup.err_unsupported_format", nil), format)
	}
	if cfg.Password == "" {
		return nil, "", 0, errors.New(templateT("backup.err_password_required", nil))
	}
	if cfg.MaxPartSize <= 0 {
		return nil, "", 0, errors.New(templateT("backup.err_max_part_size_positive", nil))
	}
	var parts []emailBackupPartFile
	defer func() {
		if resultErr != nil {
			cleanupEmailPartFiles(parts)
		}
	}()
	total, err := bs.store.CountInScope(context.Background(), scope)
	if err != nil {
		return nil, "", 0, fmt.Errorf("%s: %w", templateT("backup.err_part_rows", nil), err)
	}
	if total <= 0 {
		part, err := bs.exportEmailPartFile(format, cfg.Password, scope)
		if err != nil {
			return nil, "", 0, fmt.Errorf("%s: %w", templateT("backup.err_empty_export", nil), err)
		}
		parts = append(parts, part)
		size, err := emailBackupFileMessageSize(cfg, ext, time.Unix(0, 0), maxEmailParts-1, maxEmailParts, part.path)
		if err != nil {
			return nil, "", 0, err
		}
		if size > int64(cfg.MaxPartSize) {
			return nil, "", 0, fmt.Errorf("%s", templateT("backup.err_empty_too_large", map[string]any{"limit": cfg.MaxPartSize}))
		}
		return parts, ext, 0, nil
	}

	lastID := scope.SinceID
	if scope.Mode == "range" || scope.Mode == "" || scope.Mode == "full" {
		lastID = 0
	}
	consumed := 0
	for consumed < total {
		if len(parts) >= maxEmailParts {
			return nil, "", 0, fmt.Errorf("%s", templateT("backup.err_too_many_parts", map[string]any{"max": maxEmailParts}))
		}

		remaining := emailPartCandidateUpperBound(total, consumed)
		bestRows := 0
		bestEndID := int64(0)
		bestPart := emailBackupPartFile{}
		removeBest := func() {
			if bestPart.path != "" {
				_ = os.Remove(bestPart.path)
				bestPart.path = ""
			}
		}
		tryRows := func(rows int) (bool, error) {
			endID, err := bs.store.NthIDInScopeAfter(context.Background(), scope, lastID, rows-1)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return false, nil
				}
				return false, fmt.Errorf("%s: %w", templateT("backup.err_seek_boundary", nil), err)
			}
			partScope := scope
			partScope.SinceID = lastID
			partScope.MaxID = endID
			candidate, err := bs.exportEmailPartFile(format, cfg.Password, partScope)
			if err != nil {
				// Format limits apply before compression, independently of MIME
				// size. Shrink this candidate; a single row cannot be split further.
				if rows > 1 && (errors.Is(err, transfer.ErrJSONTooLarge) || errors.Is(err, errXLSXSizeLimit)) {
					return false, nil
				}
				return false, err
			}
			size, err := emailBackupFileMessageSize(cfg, ext, time.Unix(0, 0), maxEmailParts-1, maxEmailParts, candidate.path)
			if err != nil {
				_ = os.Remove(candidate.path)
				return false, err
			}
			if size <= int64(cfg.MaxPartSize) {
				if bestPart.path != "" {
					_ = os.Remove(bestPart.path)
				}
				bestPart, bestEndID, bestRows = candidate, endID, rows
				return true, nil
			}
			_ = os.Remove(candidate.path)
			return false, nil
		}

		probe := min(initialEmailPartProbeRows, remaining)
		failedRows := 0
		for {
			fits, err := tryRows(probe)
			if err != nil {
				removeBest()
				return nil, "", 0, err
			}
			if !fits {
				failedRows = probe
				break
			}
			if probe == remaining {
				break
			}
			probe = nextEmailPartProbe(probe, remaining)
		}
		if failedRows > 0 {
			low, high := bestRows+1, failedRows-1
			for low <= high {
				rows := low + (high-low)/2
				fits, err := tryRows(rows)
				if err != nil {
					removeBest()
					return nil, "", 0, err
				}
				if fits {
					low = rows + 1
				} else {
					high = rows - 1
				}
			}
		}
		if bestRows == 0 {
			removeBest()
			return nil, "", 0, fmt.Errorf("%s", templateT("backup.err_single_record_too_large", map[string]any{"limit": cfg.MaxPartSize}))
		}
		bestPart.endID = bestEndID
		parts = append(parts, bestPart)
		lastID = bestEndID
		consumed += bestRows
	}
	return parts, ext, lastID, nil
}

func emailPartCandidateUpperBound(total, consumed int) int {
	if total <= consumed {
		return 0
	}
	return total - consumed
}

func nextEmailPartProbe(current, remaining int) int {
	if current >= remaining {
		return remaining
	}
	if current > remaining/2 {
		return remaining
	}
	return current * 2
}

func (bs *BackupService) exportEmailPartFile(format, password string, scope ExportScope) (_ emailBackupPartFile, resultErr error) {
	file, err := os.CreateTemp("", "clipvault-email-part-*.cvexport")
	if err != nil {
		return emailBackupPartFile{}, fmt.Errorf("%s: %w", templateT("backup.err_create_part", nil), err)
	}
	path := file.Name()
	success := false
	defer func() {
		if !success {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err := bs.exportEmailBackupTo(file, format, password, scope); err != nil {
		return emailBackupPartFile{}, err
	}
	if err := file.Sync(); err != nil {
		return emailBackupPartFile{}, fmt.Errorf("%s: %w", templateT("backup.err_sync_part", nil), err)
	}
	if err := file.Close(); err != nil {
		return emailBackupPartFile{}, fmt.Errorf("%s: %w", templateT("backup.err_close_part", nil), err)
	}
	success = true
	return emailBackupPartFile{path: path}, nil
}

// sendBackupFailureNotice 在多片备份中途失败时发送通知邮件。
// 已发送的片段已计入备份进度（不会重发），剩余数据将在下次备份时
// 自动从断点续发。通知发送失败只记日志，不掩盖原始备份错误。
func (bs *BackupService) sendBackupFailureNotice(eb EmailBackupConfig, startedAt time.Time, sentParts, totalParts int, ext string, cause error) {
	subject := templateT("backup.partial_subject", map[string]any{"sent": sentParts, "total": totalParts})
	body := templateT("backup.partial_body", map[string]any{
		"started": startedAt.Format("2006-01-02 15:04:05"),
		"format":  ext,
		"total":   totalParts,
		"sent":    sentParts,
		"failed":  sentParts + 1,
		"cause":   cause.Error(),
	})
	htmlBody := renderEmailHTML(subject, strings.ReplaceAll(body, "\n", "<br>"), eb.Style, eb.AccentColor)
	msg := buildMIME(eb.From, eb.To, subject, body, htmlBody, nil)
	if err := sendSMTPMessage(eb, msg); err != nil {
		bs.logger.Error("backup_failure_notice_failed", err, map[string]any{"sentParts": sentParts, "totalParts": totalParts})
	}
}
