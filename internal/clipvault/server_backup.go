package clipvault

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleBackupStatus 返回目录备份和邮件备份的最近状态。
func (s *SearchServer) handleBackupStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	resp := map[string]any{
		"dirBackup": map[string]any{
			"enabled":     cfg.Backup.DirBackup.Enabled,
			"interval":    cfg.Backup.DirBackup.Interval,
			"dir":         cfg.Backup.DirBackup.Dir,
			"format":      cfg.Backup.DirBackup.Format,
			"hasPassword": cfg.Backup.DirBackup.Password != "",
		},
		"emailBackup": map[string]any{
			"enabled":     cfg.Backup.EmailBackup.Enabled,
			"interval":    cfg.Backup.EmailBackup.Interval,
			"smtpHost":    cfg.Backup.EmailBackup.SMTPHost,
			"smtpPort":    cfg.Backup.EmailBackup.SMTPPort,
			"smtpUser":    cfg.Backup.EmailBackup.SMTPUser,
			"from":        cfg.Backup.EmailBackup.From,
			"to":          cfg.Backup.EmailBackup.To,
			"format":      cfg.Backup.EmailBackup.Format,
			"hasSmtpPass": cfg.Backup.EmailBackup.SMTPPass != "",
			"hasPassword": cfg.Backup.EmailBackup.Password != "",
		},
	}
	if s.backup != nil {
		resp["dirStatus"] = backupStatusJSON(s.backup.DirStatus(), cfg.Backup.DirBackup.FullFallbackDays)
		resp["emailStatus"] = backupStatusJSON(s.backup.EmailStatus(), cfg.Backup.EmailBackup.FullFallbackDays)
	}
	s.writeJSON(w, resp)
}

// backupStatusJSON 把 BackupStatus 序列化为前端状态行需要的字段，
// 并附上下次自动全量兜底时间（nextFullDue），前端不做时间推算。
func backupStatusJSON(st BackupStatus, fullFallbackDays int) map[string]any {
	out := map[string]any{
		"lastTime":         st.LastTime.Format(time.RFC3339),
		"lastOK":           st.LastOK,
		"lastError":        st.LastError,
		"backupFile":       st.BackupFile,
		"actualMode":       st.ActualMode,
		"autoFallback":     st.AutoFallback,
		"lastFullAt":       st.LastFullAt,
		"trigger":          st.Trigger,
		"skipped":          st.Skipped,
		"running":          st.Running,
		"fullFallbackDays": fullFallbackDays,
	}
	if fullFallbackDays > 0 {
		if lastFull, err := time.Parse(time.RFC3339, st.LastFullAt); err == nil {
			out["nextFullDue"] = lastFull.AddDate(0, 0, fullFallbackDays).Format(time.RFC3339)
		}
	}
	return out
}

// handleBackupHistory 返回指定通道（kind=dir|email）最近的备份历史，
// 成功、失败、跳过都包含，只读。
func (s *SearchServer) handleBackupHistory(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "dir" && kind != "email" {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_backup_kind", "kind 必须是 dir 或 email", nil)
		return
	}
	history, err := s.store.ListBackupHistory(r.Context(), kind, 100)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"history": history})
}

// validateDirBackupEnabled 校验已启用的目录备份配置是否完整。
// 返回非空字符串描述缺失项，空字符串表示通过。
// 目录备份仅支持加密导出（cvexport）或明文 zip 加密打包（json/xlsx），
// 因此任何格式都必须设置加密密码，与邮件备份规则一致。
func validateDirBackupEnabled(db DirBackupConfig) string {
	var missing []string
	if db.Interval < 1 || db.Interval > maxBackupIntervalHours {
		missing = append(missing, "备份间隔")
	}
	if strings.TrimSpace(db.Dir) == "" {
		missing = append(missing, "目标目录")
	}
	if !isBackupFormat(db.Format) {
		missing = append(missing, "备份格式")
	} else if db.Password == "" {
		missing = append(missing, "加密密码（目录备份仅支持加密导出）")
	}
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, "、")
}

// validateEmailBackupEnabled 校验已启用的邮件备份配置是否完整。
// 返回非空字符串描述缺失项，空字符串表示通过。
// 邮件备份仅支持加密导出（cvexport）或明文 zip 加密打包（json/xlsx），
// 因此任何格式都必须设置加密密码。
func validateEmailBackupEnabled(eb EmailBackupConfig) string {
	var missing []string
	if eb.Interval < 1 || eb.Interval > maxBackupIntervalHours {
		missing = append(missing, "备份间隔")
	}
	missing = append(missing, smtpDeliveryErrors(eb)...)
	if !isBackupFormat(eb.Format) {
		missing = append(missing, "备份格式")
	} else if eb.Password == "" {
		missing = append(missing, "加密密码（邮件备份仅支持加密导出）")
	}
	if eb.MaxPartSize < 1<<20 || eb.MaxPartSize > 50<<20 {
		missing = append(missing, "单封邮件上限")
	}
	missing = append(missing, emailTemplateErrors(eb)...)
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, "、")
}

// writeBackupRunError 把手动触发的同步错误映射为 HTTP 响应。
// 备份执行错误（SMTP、IO 等）的具体原因直接透传给用户——这些都是
// 用户自己的配置/环境问题，需要看到原因才能修，而不是"请查看日志"。
func (s *SearchServer) writeBackupRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBackupLicenseRequired):
		s.writeAPIError(w, http.StatusPaymentRequired, "license_required", err.Error(), nil)
	case errors.Is(err, errBackupBusy):
		s.writeAPIError(w, http.StatusConflict, "backup_busy", err.Error(), nil)
	default:
		// 配置不完整等同步错误：err.Error() 已包含具体缺失项
		s.writeAPIError(w, http.StatusConflict, "backup_not_ready", err.Error(), err)
	}
}

// handleBackupDirNow 异步启动一次目录备份。配置校验、占用检测在同步阶段
// 完成；启动后返回 started=true，执行结果通过 GET /api/v1/backup 状态观察。
func (s *SearchServer) handleBackupDirNow(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		s.writeAPIError(w, http.StatusServiceUnavailable, "backup_unavailable", "备份服务未启动", nil)
		return
	}
	forceFull := r.URL.Query().Get("forceFull") == "1"
	if err := s.backup.RunDirBackup(forceFull); err != nil {
		s.writeBackupRunError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "started": true})
}

// handleBackupEmailNow 异步启动一次邮件备份。语义同 handleBackupDirNow。
func (s *SearchServer) handleBackupEmailNow(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		s.writeAPIError(w, http.StatusServiceUnavailable, "backup_unavailable", "备份服务未启动", nil)
		return
	}
	forceFull := r.URL.Query().Get("forceFull") == "1"
	if err := s.backup.RunEmailBackup(forceFull); err != nil {
		s.writeBackupRunError(w, err)
		return
	}
	s.writeJSON(w, map[string]any{"ok": true, "started": true})
}

// handleBackupEmailTest 发送一封测试邮件。
func (s *SearchServer) handleBackupEmailTest(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		s.writeAPIError(w, http.StatusServiceUnavailable, "backup_unavailable", "备份服务未启动", nil)
		return
	}
	if err := s.backup.SendTestEmail(); err != nil {
		s.logger.Error("email_test_failed", err, nil)
		s.writeAPIError(w, http.StatusInternalServerError, "email_test_failed", "测试邮件发送失败: "+err.Error(), err)
		return
	}
	s.logger.Info("email_test_ok", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// writeFileRefError 统一 resolveRecordedFilePath 三类失败的 HTTP 分层响应（#2b 错误码分层）：
//   - ErrFreeWindowOutside → 402 window_outside（免费版可见窗口之外，与 enforceFreeWindow / handleItemPosition 一致）；
//   - ErrFileRefLookup     → 5xx internal_error（存储层/IO 查询故障，复用本文件既有的 writeError 风格，
//     不把 DuckDB 内部细节回传客户端，#19）；
//   - 其余（无效 item_id / file_index、越界、非文件记录等）→ 400 invalid_file_ref（用户入参非法）。
func (s *SearchServer) writeFileRefError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrFreeWindowOutside):
		s.writeAPIError(w, http.StatusPaymentRequired, "window_outside",
			"未激活仅可查看最近 "+strconv.Itoa(FreeTierVisibleLimit)+" 条记录，该文件不在可查看范围内", nil)
	case errors.Is(err, ErrFileRefLookup):
		s.writeError(w, err)
	default:
		s.writeAPIError(w, http.StatusBadRequest, "invalid_file_ref", "无效的文件引用", nil)
	}
}
