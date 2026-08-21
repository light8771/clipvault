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
			resp["tokenCount"] = countChannelTokens(tokens, TokenChannelHTTP)
		}
	}
	s.writeJSON(w, resp)
}

// updateExternalServiceConfig 更新外部服务（数据接口 / MCP）启用开关与监听地址的
// 共享实现（立即生效，无需重启）。selectCfg 从配置中选出目标段；
// runtimeStatus 报告运行状态（服务未装配时 ok=false）。
func (s *SearchServer) updateExternalServiceConfig(w http.ResponseWriter, r *http.Request, feature, runtimeLogEvent string, selectCfg func(*Config) *ExternalServiceConfig, runtimeStatus func() (externalServiceStatus, bool)) {
	var req struct {
		Enabled    bool   `json:"enabled"`
		ListenAddr string `json:"listenAddr"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	host, port, err := validateListenAddr(req.ListenAddr)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_listen_addr", s.i18nT("api.error.invalid_listen_addr", map[string]any{"detail": err.Error()}), err)
		return
	}
	cfg := s.cfgMgr.Get()
	target := selectCfg(&cfg)
	addrChanged := req.ListenAddr != target.ListenAddr
	enabling := req.Enabled && !target.Enabled
	// 启用或变更地址前探测端口可用性，避免保存后绑定失败才被发现。
	if req.Enabled && (enabling || addrChanged) {
		if err := listenAddrAvailable(host, port); err != nil {
			s.writeAPIError(w, http.StatusBadRequest, "listen_addr_unavailable", s.i18nT("api.error.listen_addr_unavailable", map[string]any{"addr": req.ListenAddr}), err)
			return
		}
	}
	if req.Enabled && FreeTierRestriction(s.env) {
		s.writeAPIError(w, http.StatusForbidden, "activation_required", s.i18nT("api.error.feature_activation_required", map[string]any{"feature": feature}), nil)
		return
	}
	curCfg := cfg
	target.Enabled = req.Enabled
	target.ListenAddr = req.ListenAddr
	changes := diffConfigChanges(curCfg, cfg)
	response := map[string]any{"ok": true}
	if err := s.cfgMgr.Update(cfg); err != nil {
		var runtimeErr *ConfigRuntimeApplyError
		if !errors.As(err, &runtimeErr) {
			s.writeError(w, err)
			return
		}
		response["runtimeError"] = runtimeErr.Error()
		s.logger.Warn(runtimeLogEvent, map[string]any{"error": runtimeErr.Error()})
	}
	if s.store != nil {
		if err := s.store.LogConfigChanges(r.Context(), changes); err != nil {
			s.logger.Warn("config_change_log_failed", map[string]any{"error": err.Error()})
		}
	}
	newCfg := s.cfgMgr.Get()
	newTarget := selectCfg(&newCfg)
	response["enabled"] = newTarget.Enabled
	response["listenAddr"] = newTarget.ListenAddr
	if status, ok := runtimeStatus(); ok {
		response["running"] = status.Running
		response["url"] = status.URL
		response["bindError"] = status.BindError
	}
	s.writeJSON(w, response)
}

// handleDataAPIConfig 更新数据接口配置（立即生效，无需重启）。
func (s *SearchServer) handleDataAPIConfig(w http.ResponseWriter, r *http.Request) {
	s.updateExternalServiceConfig(w, r, s.i18nT("app.title.data_api", nil), "data_api_runtime_apply_failed",
		func(c *Config) *ExternalServiceConfig { return &c.DataAPI },
		func() (externalServiceStatus, bool) {
			if s.dataAPI == nil {
				return externalServiceStatus{}, false
			}
			return s.dataAPI.Status(), true
		})
}

// handleDataAPITokens 列出全部 HTTP 通道 Token 元数据（不含原文）。
func (s *SearchServer) handleDataAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListAPITokens(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	out := []map[string]any{}
	now := time.Now()
	for _, token := range tokens {
		if token.Channel != TokenChannelHTTP {
			continue
		}
		state := "active"
		switch {
		case token.RevokedAt != nil:
			state = "revoked"
		case token.ExpiresAt != nil && now.After(*token.ExpiresAt):
			state = "expired"
		}
		out = append(out, map[string]any{
			"id":             strconv.FormatInt(token.ID, 10),
			"name":           token.Name,
			"channel":        token.Channel,
			"allowSensitive": token.AllowSensitive,
			"createdAt":      token.CreatedAt,
			"expiresAt":      token.ExpiresAt,
			"lastUsedAt":     token.LastUsedAt,
			"revokedAt":      token.RevokedAt,
			"state":          state,
		})
	}
	s.writeJSON(w, map[string]any{"tokens": out})
}

// dataAPITokenCreated 是创建/重新生成 Token 的响应（原文只出现这一次）。
type dataAPITokenCreated struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Channel        string     `json:"channel"`
	AllowSensitive bool       `json:"allowSensitive"`
	Token          string     `json:"token"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpiresAt      *time.Time `json:"expiresAt"`
}

// createAPIToken 是创建 Token 的共享实现；要求已激活（实时从磁盘校验，AUTH 门控）。
// channel 限定令牌用途（http|mcp）；allowSensitive 由调用方决定：新令牌默认不
// 授权敏感数据（安全默认），重新生成则保持原值。
func (s *SearchServer) createAPIToken(w http.ResponseWriter, r *http.Request, name, expiryPreset, channel string, allowSensitive bool) *dataAPITokenCreated {
	if FreeTierRestriction(s.env) {
		s.writeAPIError(w, http.StatusForbidden, "activation_required", s.i18nT("api.error.token_create_activation_required", nil), nil)
		return nil
	}
	if !validTokenChannel(channel) {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_channel", s.i18nT("api.error.invalid_channel", nil), nil)
		return nil
	}
	name, err := validateAPITokenName(name)
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_name", s.i18nT("api.error.token_name_empty", nil), err)
		return nil
	}
	expiresAtMS, err := apiTokenExpiryMS(expiryPreset, time.Now().UTC())
	if err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_expiry", s.i18nT("api.error.invalid_expiry", nil), err)
		return nil
	}
	raw, hash, err := generateAPIToken()
	if err != nil {
		s.writeError(w, err)
		return nil
	}
	id, err := s.store.CreateAPIToken(r.Context(), name, hash, expiresAtMS, channel, allowSensitive)
	if err != nil {
		s.writeError(w, err)
		return nil
	}
	return &dataAPITokenCreated{
		ID:             strconv.FormatInt(id, 10),
		Name:           name,
		Channel:        channel,
		AllowSensitive: allowSensitive,
		Token:          raw,
		CreatedAt:      time.Now().UTC(),
		ExpiresAt:      msToTimePtr(expiresAtMS),
	}
}

// handleDataAPITokenCreate 创建新 HTTP 数据接口 Token。
// 敏感授权初始值为关闭（安全默认），创建后可在令牌列表中单独调整。
func (s *SearchServer) handleDataAPITokenCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		ExpiresIn string `json:"expiresIn"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	created := s.createAPIToken(w, r, req.Name, req.ExpiresIn, TokenChannelHTTP, false)
	if created == nil {
		return
	}
	s.logger.Info("data_api_token_created", map[string]any{"tokenId": created.ID, "name": created.Name, "allowSensitive": created.AllowSensitive})
	s.writeJSON(w, created)
}

// parseTokenIDRequest 解析并校验 {"id":"..."} 请求体中的 Token ID。
// 失败时已写好错误响应，返回 0 表示中止。
func (s *SearchServer) parseTokenIDRequest(w http.ResponseWriter, r *http.Request) int64 {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return 0
	}
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_token_id", nil), nil)
		return 0
	}
	return id
}

// setTokenSensitive 单独调整 Token 敏感数据授权的共享实现（仅对有效 Token 生效）。
func (s *SearchServer) setTokenSensitive(w http.ResponseWriter, r *http.Request, channel, logEvent string) {
	var req struct {
		ID             string `json:"id"`
		AllowSensitive bool   `json:"allowSensitive"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", s.i18nT("api.error.invalid_json", nil), err)
		return
	}
	id, err := strconv.ParseInt(req.ID, 10, 64)
	if err != nil || id <= 0 {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_id", s.i18nT("api.error.invalid_token_id", nil), nil)
		return
	}
	if err := s.store.SetAPITokenSensitive(r.Context(), id, channel, req.AllowSensitive); err != nil {
		if errors.Is(err, errAPITokenNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.token_not_found_inactive", nil), nil)
			return
		}
		s.writeError(w, err)
		return
	}
	s.logger.Info(logEvent, map[string]any{"tokenId": req.ID, "allowSensitive": req.AllowSensitive})
	s.writeJSON(w, map[string]any{"ok": true, "id": req.ID, "allowSensitive": req.AllowSensitive})
}

// handleDataAPITokenSensitive 单独调整某个 Token 的敏感数据授权（仅对有效 Token 生效）。
func (s *SearchServer) handleDataAPITokenSensitive(w http.ResponseWriter, r *http.Request) {
	s.setTokenSensitive(w, r, TokenChannelHTTP, "data_api_token_sensitive_updated")
}

// regenerateToken 撤销旧 Token 并按原名称/有效期/通道/敏感授权签发同通道新 Token 的
// 共享实现。激活门控先于撤销执行：撤销不可回滚，必须先确保新 Token 可签发，
// 避免未激活用户在重新生成时丢失旧 Token 又拿不到新 Token。
// 已失效（撤销/过期）Token 拒绝重新生成（404）：重新生成是轮换在用 Token，
// 不是复活已废止的 Token——与敏感授权端点的失效语义一致。
func (s *SearchServer) regenerateToken(w http.ResponseWriter, r *http.Request, channel, logEvent string) {
	if FreeTierRestriction(s.env) {
		s.writeAPIError(w, http.StatusForbidden, "activation_required", s.i18nT("api.error.token_regen_activation_required", nil), nil)
		return
	}
	id := s.parseTokenIDRequest(w, r)
	if id == 0 {
		return
	}
	old, err := s.store.GetAPITokenByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, errAPITokenNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.token_not_found", nil), nil)
			return
		}
		s.writeError(w, err)
		return
	}
	if old.Channel != channel {
		s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.token_not_found", nil), nil)
		return
	}
	if old.RevokedAt != nil || (old.ExpiresAt != nil && time.Now().After(*old.ExpiresAt)) {
		s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.token_not_found_inactive", nil), nil)
		return
	}
	// 有效期语义：保持原来的总时长（从签发时刻重新计算）；永不过期保持不变。
	preset := tokenExpiryPreset(old.CreatedAt, old.ExpiresAt)
	if err := s.store.RevokeAPIToken(r.Context(), id, channel); err != nil {
		if errors.Is(err, errAPITokenNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.token_not_found", nil), nil)
			return
		}
		s.writeError(w, err)
		return
	}
	// 通道与敏感授权保持不变（重新生成不扩大权限）。
	created := s.createAPIToken(w, r, old.Name, preset, channel, old.AllowSensitive)
	if created == nil {
		return
	}
	s.logger.Info(logEvent, map[string]any{"oldTokenId": id, "newTokenId": created.ID})
	s.writeJSON(w, created)
}

// handleDataAPITokenRegenerate 撤销旧 Token 并按原名称/有效期/敏感授权签发新 Token。
func (s *SearchServer) handleDataAPITokenRegenerate(w http.ResponseWriter, r *http.Request) {
	s.regenerateToken(w, r, TokenChannelHTTP, "data_api_token_regenerated")
}

// revokeToken 撤销 Token 的共享实现（立即生效，不删除记录）。通道必须匹配：
// 跨通道撤销与不存在一样返回 404（SEC-07 通道隔离）。
func (s *SearchServer) revokeToken(w http.ResponseWriter, r *http.Request, channel, logEvent string) {
	id := s.parseTokenIDRequest(w, r)
	if id == 0 {
		return
	}
	if err := s.store.RevokeAPIToken(r.Context(), id, channel); err != nil {
		if errors.Is(err, errAPITokenNotFound) {
			s.writeAPIError(w, http.StatusNotFound, "not_found", s.i18nT("api.error.token_not_found", nil), nil)
			return
		}
		s.writeError(w, err)
		return
	}
	s.logger.Info(logEvent, map[string]any{"tokenId": id})
	s.writeJSON(w, map[string]any{"ok": true})
}

// handleDataAPITokenRevoke 撤销 Token（立即生效，不删除记录）。
func (s *SearchServer) handleDataAPITokenRevoke(w http.ResponseWriter, r *http.Request) {
	s.revokeToken(w, r, TokenChannelHTTP, "data_api_token_revoked")
}

// apiAuditFilterFromRequest 把查询参数编译为审计过滤条件（两通道共享骨架）。
// operation 支持逗号分隔多值；limit 上限 200；channel 限定本页只看哪个通道。
func apiAuditFilterFromRequest(r *http.Request, channel string) APIAuditFilter {
	query := r.URL.Query()
	filter := APIAuditFilter{
		Channel: channel,
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

// countChannelTokens 统计指定通道的 Token 数量（含已撤销，供管理界面展示）。
func countChannelTokens(tokens []APIToken, channel string) int {
	count := 0
	for _, token := range tokens {
		if token.Channel == channel {
			count++
		}
	}
	return count
}

func parseInt64OrZero(text string) int64 {
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// handleDataAPIAudit 分页返回外部 API 访问记录（支持操作/结果/令牌/关键词过滤），
// 并附带该通道的统计概览。
func (s *SearchServer) handleDataAPIAudit(w http.ResponseWriter, r *http.Request) {
	filter := apiAuditFilterFromRequest(r, TokenChannelHTTP)
	events, total, err := s.store.ListAPIAuditEvents(r.Context(), filter)
	if err != nil {
		s.writeError(w, err)
		return
	}
	resp := map[string]any{"entries": events, "total": total}
	if stats, err := s.store.APIAuditStats(r.Context(), TokenChannelHTTP); err == nil {
		resp["stats"] = stats
	}
	s.writeJSON(w, resp)
}

// handleDataAPIAuditExport 按当前过滤条件导出全部审计记录为 CSV（UTF-8 BOM，
// Excel 可直接打开）。
func (s *SearchServer) handleDataAPIAuditExport(w http.ResponseWriter, r *http.Request) {
	filter := apiAuditFilterFromRequest(r, TokenChannelHTTP)
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
	// 表头与布尔值按当前语言生成（导出物给用户阅读，不是机器契约）。
	_ = writer.Write([]string{
		s.i18nT("audit_csv.time_utc", nil),
		s.i18nT("audit_csv.channel", nil),
		s.i18nT("audit_csv.token", nil),
		s.i18nT("audit_csv.source_ip", nil),
		s.i18nT("audit_csv.method", nil),
		s.i18nT("audit_csv.endpoint", nil),
		s.i18nT("audit_csv.operation", nil),
		s.i18nT("audit_csv.resource_uri", nil),
		s.i18nT("audit_csv.record_id", nil),
		s.i18nT("audit_csv.item_count", nil),
		s.i18nT("audit_csv.duration_ms", nil),
		s.i18nT("audit_csv.status_code", nil),
		s.i18nT("audit_csv.error_code", nil),
		s.i18nT("audit_csv.request_summary", nil),
		s.i18nT("audit_csv.request_body", nil),
		s.i18nT("audit_csv.sensitive_access", nil),
	})
	noText := s.i18nT("audit_csv.no", nil)
	yesText := s.i18nT("audit_csv.yes", nil)
	for _, event := range events {
		itemID := ""
		if event.ItemID > 0 {
			itemID = strconv.FormatInt(event.ItemID, 10)
		}
		sensitive := noText
		if event.SensitiveAccessed {
			sensitive = yesText
		}
		_ = writer.Write([]string{
			event.Timestamp.UTC().Format("2006-01-02 15:04:05"),
			event.Channel,
			event.TokenName,
			event.RemoteIP,
			event.Method,
			event.Endpoint,
			event.Operation,
			event.ResourceURI,
			itemID,
			strconv.Itoa(event.ItemCount),
			strconv.FormatInt(event.DurationMs, 10),
			strconv.Itoa(event.StatusCode),
			event.ErrorCode,
			event.RequestSummary,
			event.RequestBody,
			sensitive,
		})
	}
	writer.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="clipvault-audit.csv"`)
	_, _ = w.Write(buf.Bytes())
}

// handleDataAPIAuditClear 清空数据接口（http 通道）访问记录，保留 MCP 审计。
func (s *SearchServer) handleDataAPIAuditClear(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearAPIAuditEvents(r.Context(), TokenChannelHTTP); err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("data_api_audit_cleared", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}
