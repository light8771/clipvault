package clipvault

import (
	"bytes"
	"encoding/csv"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 数据接口的管理端点（主 Web 服务 /api/v1/data-api/*，走会话认证 + 信封契约）。
// 外部数据 API 本身由 DataAPIService 独立监听，不经过这些端点。

// SetDataAPIService 注入数据接口服务实例（由应用装配层调用）。
func (s *SearchServer) SetDataAPIService(svc *DataAPIService) {
	s.dataAPI = svc
}

// handleDataAPIStatus 返回配置、运行状态与统计信息。
func (s *SearchServer) handleDataAPIStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	activated := s.license == nil || s.license.CanUsePaidFeatures()
	resp := map[string]any{
		"enabled":    cfg.DataAPI.Enabled,
		"listenAddr": cfg.DataAPI.ListenAddr,
		"activated":  activated,
	}
	if s.dataAPI != nil {
		status := s.dataAPI.Status()
		resp["running"] = status.Running
		resp["url"] = status.URL
		resp["bindError"] = status.BindError
		resp["activeQueries"] = status.ActiveQueries
		resp["requestsTotal"] = status.RequestsTotal
		resp["busyRejections"] = status.BusyRejections
		resp["auditDropped"] = status.AuditDropped
	}
	if s.store != nil {
		if tokens, err := s.store.ListAPITokens(r.Context()); err == nil {
			resp["tokenCount"] = len(tokens)
		}
	}
	s.writeJSON(w, resp)
}

// handleDataAPIConfig 更新启用开关与监听地址（立即生效，无需重启）。
func (s *SearchServer) handleDataAPIConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled    bool   `json:"enabled"`
		ListenAddr string `json:"listenAddr"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	host, port, err := validateListenAddr(req.ListenAddr)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_listen_addr", "监听地址无效: "+err.Error(), err)
		return
	}
	cfg := s.cfgMgr.Get()
	addrChanged := req.ListenAddr != cfg.DataAPI.ListenAddr
	enabling := req.Enabled && !cfg.DataAPI.Enabled
	// 启用或变更地址前探测端口可用性，避免保存后绑定失败才被发现。
	if req.Enabled && (enabling || addrChanged) {
		if err := listenAddrAvailable(host, port); err != nil {
			s.writeAPIError(w, http.StatusBadRequest, "listen_addr_unavailable", "监听地址 "+req.ListenAddr+" 当前不可用（端口可能被其他程序占用），请更换端口", err)
			return
		}
	}
	if req.Enabled && FreeTierRestriction(s.env) {
		s.writeAPIError(w, http.StatusForbidden, "activation_required", "数据接口仅对已激活用户开放", nil)
		return
	}
	curCfg := cfg
	cfg.DataAPI.Enabled = req.Enabled
	cfg.DataAPI.ListenAddr = req.ListenAddr
	changes := diffConfigChanges(curCfg, cfg)
	response := map[string]any{"ok": true}
	if err := s.cfgMgr.Update(cfg); err != nil {
		var runtimeErr *ConfigRuntimeApplyError
		if !errors.As(err, &runtimeErr) {
			s.writeError(w, err)
			return
		}
		response["runtimeError"] = runtimeErr.Error()
		s.logger.Warn("data_api_runtime_apply_failed", map[string]any{"error": runtimeErr.Error()})
	}
	if s.store != nil {
		if err := s.store.LogConfigChanges(r.Context(), changes); err != nil {
			s.logger.Warn("config_change_log_failed", map[string]any{"error": err.Error()})
		}
	}
	newCfg := s.cfgMgr.Get()
	response["enabled"] = newCfg.DataAPI.Enabled
	response["listenAddr"] = newCfg.DataAPI.ListenAddr
	if s.dataAPI != nil {
		status := s.dataAPI.Status()
		response["running"] = status.Running
		response["url"] = status.URL
		response["bindError"] = status.BindError
	}
	s.writeJSON(w, response)
}

// handleDataAPITokens 列出全部 Token 元数据（不含原文）。
func (s *SearchServer) handleDataAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListAPITokens(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	out := make([]map[string]any, len(tokens))
	now := time.Now()
	for i, token := range tokens {
		state := "active"
		switch {
		case token.RevokedAt != nil:
			state = "revoked"
		case token.ExpiresAt != nil && now.After(*token.ExpiresAt):
			state = "expired"
		}
		out[i] = map[string]any{
			"id":         strconv.FormatInt(token.ID, 10),
			"name":       token.Name,
			"createdAt":  token.CreatedAt,
			"expiresAt":  token.ExpiresAt,
			"lastUsedAt": token.LastUsedAt,
			"revokedAt":  token.RevokedAt,
			"state":      state,
		}
	}
	s.writeJSON(w, map[string]any{"tokens": out})
}

// dataAPITokenCreated 是创建/重新生成 Token 的响应（原文只出现这一次）。
type dataAPITokenCreated struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Token     string     `json:"token"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// createAPIToken 是创建 Token 的共享实现；要求已激活（实时从磁盘校验，AUTH 门控）。
func (s *SearchServer) createAPIToken(w http.ResponseWriter, r *http.Request, name, expiryPreset string) *dataAPITokenCreated {
	if FreeTierRestriction(s.env) {
		s.writeAPIError(w, http.StatusForbidden, "activation_required", "数据接口仅对已激活用户开放", nil)
		return nil
	}
	name, err := validateAPITokenName(name)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_name", "Token 名称不能为空", err)
		return nil
	}
	expiresAtMS, err := apiTokenExpiryMS(expiryPreset, time.Now().UTC())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_expiry", "无效的有效期", err)
		return nil
	}
	raw, hash, err := generateAPIToken()
	if err != nil {
		s.writeError(w, err)
		return nil
	}
	id, err := s.store.CreateAPIToken(r.Context(), name, hash, expiresAtMS)
	if err != nil {
		s.writeError(w, err)
		return nil
	}
	return &dataAPITokenCreated{
		ID:        strconv.FormatInt(id, 10),
		Name:      name,
		Token:     raw,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: msToTimePtr(expiresAtMS),
	}
}

// handleDataAPITokenCreate 创建新 Token。
func (s *SearchServer) handleDataAPITokenCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		ExpiresIn string `json:"expiresIn"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	created := s.createAPIToken(w, r, req.Name, req.ExpiresIn)
	if created == nil {
		return
	}
	s.logger.Info("data_api_token_created", map[string]any{"tokenId": created.ID, "name": created.Name})
	s.writeJSON(w, created)
}

// handleDataAPITokenRegenerate 撤销旧 Token 并按原名称/有效期签发新 Token。
func (s *SearchServer) handleDataAPITokenRegenerate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 Token ID", nil)
		return
	}
	var old *APIToken
	tokens, err := s.store.ListAPITokens(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	for i := range tokens {
		if tokens[i].ID == id {
			old = &tokens[i]
			break
		}
	}
	if old == nil {
		s.writeAPIError(w, http.StatusNotFound, "not_found", "Token 不存在", nil)
		return
	}
	// 有效期语义：保持原来的总时长（从签发时刻重新计算）；永不过期保持不变。
	preset := "never"
	if old.ExpiresAt != nil {
		switch old.ExpiresAt.Sub(old.CreatedAt) {
		case 24 * time.Hour:
			preset = "1d"
		case 7 * 24 * time.Hour:
			preset = "7d"
		case 30 * 24 * time.Hour:
			preset = "30d"
		case 90 * 24 * time.Hour:
			preset = "90d"
		default:
			preset = "1y"
		}
	}
	if err := s.store.RevokeAPIToken(r.Context(), id); err != nil {
		if errors.Is(err, errAPITokenNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "not_found", "Token 不存在", nil)
			return
		}
		s.writeError(w, err)
		return
	}
	created := s.createAPIToken(w, r, old.Name, preset)
	if created == nil {
		return
	}
	s.logger.Info("data_api_token_regenerated", map[string]any{"oldTokenId": req.ID, "newTokenId": created.ID})
	s.writeJSON(w, created)
}

// handleDataAPITokenRevoke 撤销 Token（立即生效，不删除记录）。
func (s *SearchServer) handleDataAPITokenRevoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", "无效的 Token ID", nil)
		return
	}
	if err := s.store.RevokeAPIToken(r.Context(), id); err != nil {
		if errors.Is(err, errAPITokenNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "not_found", "Token 不存在", nil)
			return
		}
		s.writeError(w, err)
		return
	}
	s.logger.Info("data_api_token_revoked", map[string]any{"tokenId": req.ID})
	s.writeJSON(w, map[string]any{"ok": true})
}

// dataAPIAuditFilterFromRequest 把查询参数编译为审计过滤条件。
// operation 支持逗号分隔多值；limit 上限 200。
func dataAPIAuditFilterFromRequest(r *http.Request) APIAuditFilter {
	query := r.URL.Query()
	filter := APIAuditFilter{
		Result:  query.Get("result"),
		Query:   query.Get("q"),
		TokenID: parseInt64OrZero(query.Get("tokenId")),
	}
	for _, op := range strings.Split(query.Get("operation"), ",") {
		if op = strings.TrimSpace(op); op != "" {
			filter.Operations = append(filter.Operations, op)
		}
	}
	filter.Limit, _ = strconv.Atoi(query.Get("limit"))
	filter.Offset, _ = strconv.Atoi(query.Get("offset"))
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return filter
}

func parseInt64OrZero(text string) int64 {
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// handleDataAPIAudit 分页返回外部 API 访问记录（支持操作/结果/令牌/关键词过滤），
// 并附带全量统计概览。
func (s *SearchServer) handleDataAPIAudit(w http.ResponseWriter, r *http.Request) {
	filter := dataAPIAuditFilterFromRequest(r)
	events, total, err := s.store.ListAPIAuditEvents(r.Context(), filter)
	if err != nil {
		s.writeError(w, err)
		return
	}
	resp := map[string]any{"entries": events, "total": total}
	if stats, err := s.store.APIAuditStats(r.Context()); err == nil {
		resp["stats"] = stats
	}
	s.writeJSON(w, resp)
}

// handleDataAPIAuditExport 按当前过滤条件导出全部审计记录为 CSV（UTF-8 BOM，
// Excel 可直接打开）。
func (s *SearchServer) handleDataAPIAuditExport(w http.ResponseWriter, r *http.Request) {
	filter := dataAPIAuditFilterFromRequest(r)
	filter.Limit = maxAPIAuditLogRows
	filter.Offset = 0
	events, _, err := s.store.ListAPIAuditEvents(r.Context(), filter)
	if err != nil {
		s.writeError(w, err)
		return
	}
	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM
	writer := csv.NewWriter(&buf)
	_ = writer.Write([]string{"时间(UTC)", "令牌", "来源IP", "方法", "端点", "操作", "记录ID", "返回条数", "耗时(ms)", "状态码", "错误码", "请求摘要", "请求体"})
	for _, event := range events {
		itemID := ""
		if event.ItemID > 0 {
			itemID = strconv.FormatInt(event.ItemID, 10)
		}
		_ = writer.Write([]string{
			event.Timestamp.UTC().Format("2006-01-02 15:04:05"),
			event.TokenName,
			event.RemoteIP,
			event.Method,
			event.Endpoint,
			event.Operation,
			itemID,
			strconv.Itoa(event.ItemCount),
			strconv.FormatInt(event.DurationMs, 10),
			strconv.Itoa(event.StatusCode),
			event.ErrorCode,
			event.RequestSummary,
			event.RequestBody,
		})
	}
	writer.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="clipvault-audit.csv"`)
	_, _ = w.Write(buf.Bytes())
}

// handleDataAPIAuditClear 清空外部 API 访问记录。
func (s *SearchServer) handleDataAPIAuditClear(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearAPIAuditEvents(r.Context()); err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("data_api_audit_cleared", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}
