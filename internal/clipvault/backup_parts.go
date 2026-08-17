package clipvault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
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
		return nil, "", 0, fmt.Errorf("不支持的备份格式: %s", format)
	}
	if cfg.Password == "" {
		return nil, "", 0, errors.New("邮件备份必须设置加密密码（仅支持加密导出）")
	}
	if cfg.MaxPartSize <= 0 {
		return nil, "", 0, errors.New("单封邮件大小上限必须大于 0")
	}
	var parts []emailBackupPartFile
	defer func() {
		if resultErr != nil {
			cleanupEmailPartFiles(parts)
		}
	}()
	total, err := bs.store.CountInScope(context.Background(), scope)
	if err != nil {
		return nil, "", 0, fmt.Errorf("统计分片行数失败: %w", err)
	}
	if total <= 0 {
		part, err := bs.exportEmailPartFile(format, cfg.Password, scope)
		if err != nil {
			return nil, "", 0, fmt.Errorf("导出空备份失败: %w", err)
		}
		parts = append(parts, part)
		size, err := emailBackupFileMessageSize(cfg, ext, time.Unix(0, 0), maxEmailParts-1, maxEmailParts, part.path)
		if err != nil {
			return nil, "", 0, err
		}
		if size > int64(cfg.MaxPartSize) {
			return nil, "", 0, fmt.Errorf("空备份邮件仍超过上限 %d 字节", cfg.MaxPartSize)
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
			return nil, "", 0, fmt.Errorf("备份需要超过 %d 封邮件，请提高单封大小上限、改用目录备份或缩小范围", maxEmailParts)
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
				return false, fmt.Errorf("定位分片边界失败: %w", err)
			}
			partScope := scope
			partScope.SinceID = lastID
			partScope.MaxID = endID
			candidate, err := bs.exportEmailPartFile(format, cfg.Password, partScope)
			if err != nil {
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
			return nil, "", 0, fmt.Errorf("至少一条记录单独导出后邮件仍超过上限 %d 字节；请提高上限或改用目录备份", cfg.MaxPartSize)
		}
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
		return emailBackupPartFile{}, fmt.Errorf("创建邮件分片文件失败: %w", err)
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
		return emailBackupPartFile{}, fmt.Errorf("同步邮件分片失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return emailBackupPartFile{}, fmt.Errorf("关闭邮件分片失败: %w", err)
	}
	success = true
	return emailBackupPartFile{path: path}, nil
}

// sendBackupFailureNotice sends a notification email when a multi-part backup
// failed partway, so the recipient knows the partial parts cannot be restored
// alone and a full retry is needed. Failure to send the notice is only logged
// and does not mask the original backup error.
func (bs *BackupService) sendBackupFailureNotice(eb EmailBackupConfig, startedAt time.Time, sentParts, totalParts int, ext string, cause error) {
	subject := fmt.Sprintf("⚠️ ClipVault 备份未完成（已发 %d/%d 片）", sentParts, totalParts)
	body := fmt.Sprintf(`ClipVault 邮件备份未完成

备份发起时间：%s
备份格式：%s
计划分片总数：%d
已成功发送：%d 片
失败位置：第 %d 片
失败原因：%v

重要说明：
- 已发送的 %d 片已无法撤回，但数据不完整，无法单独还原。
- 请勿仅凭已收到的部分片进行还原，否则会丢失数据。
- 建议在 ClipVault 设置页查看备份状态（LastError），修复问题后重新触发一次完整备份。
- 若频繁失败，可尝试：改用增量备份减少单次数据量、检查 SMTP 配置与网络、或降低单封附件上限。

—— ClipVault`, startedAt.Format("2006-01-02 15:04:05"), ext, totalParts, sentParts, sentParts+1, cause, sentParts)
	htmlBody := renderEmailHTML(subject, strings.ReplaceAll(body, "\n", "<br>"), eb.Style, eb.AccentColor)
	msg := buildMIME(eb.From, eb.To, subject, body, htmlBody, nil)
	if err := sendSMTPMessage(eb, msg); err != nil {
		bs.logger.Error("backup_failure_notice_failed", err, map[string]any{"sentParts": sentParts, "totalParts": totalParts})
	}
}
