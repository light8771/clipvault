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

// MCP 的管理端点（主 Web 服务 /api/v1/mcp/*，走会话认证 + 信封契约）。
// MCP 服务本身由 MCPService 独立监听，不经过这些端点。

// SetMCPService 注入 MCP 服务实例（由应用装配层调用）。
func (s *SearchServer) SetMCPService(svc *MCPService) {
	s.mcp = svc
}

// handleMCPStatus 返回配置、运行状态与统计信息（含可发现的工具/资源清单）。
func (s *SearchServer) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgMgr.Get()
	activated := s.license == nil || s.license.CanUsePaidFeatures()
	resp := map[string]any{
		"enabled":    cfg.MCP.Enabled,
		"listenAddr": cfg.MCP.ListenAddr,
		"activated":  activated,
		"capabilities": map[string]any{
			"tools": mcpCoreToolNames(),
			"resourceTemplates": []string{
				mcpResourceItemsTemplate,
				mcpResourceContentTemplate,
				mcpResourceGroupsTemplate,
				mcpResourceGroupItemsTemplate,
			},
			"staticResources": []string{mcpResourceRecentURI, mcpResourceStarredURI},
		},
	}
	if s.mcp != nil {
		status := s.mcp.Status()
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
			count := 0
			for _, token := range tokens {
				if token.Channel == TokenChannelMCP {
					count++
				}
			}
			resp["tokenCount"] = count
		}
	}
	s.writeJSON(w, resp)
}

// handleMCPConfig 更新启用开关与监听地址（立即生效，无需重启）。
func (s *SearchServer) handleMCPConfig(w http.ResponseWriter, r *http.Request) {
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
	addrChanged := req.ListenAddr != cfg.MCP.ListenAddr
	enabling := req.Enabled && !cfg.MCP.Enabled
	// 启用或变更地址前探测端口可用性，避免保存后绑定失败才被发现。
	if req.Enabled && (enabling || addrChanged) {
		if err := listenAddrAvailable(host, port); err != nil {
			s.writeAPIError(w, http.StatusBadRequest, "listen_addr_unavailable", "监听地址 "+req.ListenAddr+" 当前不可用（端口可能被其他程序占用），请更换端口", err)
			return
		}
	}
	if req.Enabled && FreeTierRestriction(s.env) {
		s.writeAPIError(w, http.StatusForbidden, "activation_required", "MCP 仅对已激活用户开放", nil)
		return
	}
	curCfg := cfg
	cfg.MCP.Enabled = req.Enabled
	cfg.MCP.ListenAddr = req.ListenAddr
	changes := diffConfigChanges(curCfg, cfg)
	response := map[string]any{"ok": true}
	if err := s.cfgMgr.Update(cfg); err != nil {
		var runtimeErr *ConfigRuntimeApplyError
		if !errors.As(err, &runtimeErr) {
			s.writeError(w, err)
			return
		}
		response["runtimeError"] = runtimeErr.Error()
		s.logger.Warn("mcp_runtime_apply_failed", map[string]any{"error": runtimeErr.Error()})
	}
	if s.store != nil {
		if err := s.store.LogConfigChanges(r.Context(), changes); err != nil {
			s.logger.Warn("config_change_log_failed", map[string]any{"error": err.Error()})
		}
	}
	newCfg := s.cfgMgr.Get()
	response["enabled"] = newCfg.MCP.Enabled
	response["listenAddr"] = newCfg.MCP.ListenAddr
	if s.mcp != nil {
		status := s.mcp.Status()
		response["running"] = status.Running
		response["url"] = status.URL
		response["bindError"] = status.BindError
	}
	s.writeJSON(w, response)
}

// handleMCPTokens 列出全部 MCP 通道 Token 元数据（不含原文）。
func (s *SearchServer) handleMCPTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListAPITokens(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	out := []map[string]any{}
	now := time.Now()
	for _, token := range tokens {
		if token.Channel != TokenChannelMCP {
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

// handleMCPTokenCreate 创建新 MCP 通道 Token。
func (s *SearchServer) handleMCPTokenCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string `json:"name"`
		Expiry         string `json:"expiry"`
		AllowSensitive bool   `json:"allowSensitive"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	created := s.createAPIToken(w, r, req.Name, req.Expiry, TokenChannelMCP, req.AllowSensitive)
	if created == nil {
		return
	}
	s.logger.Info("mcp_token_created", map[string]any{"tokenId": created.ID, "name": created.Name, "allowSensitive": created.AllowSensitive})
	s.writeJSON(w, created)
}

// handleMCPTokenRegenerate 撤销旧 Token 并按原名称/有效期/敏感授权签发新 MCP Token。
func (s *SearchServer) handleMCPTokenRegenerate(w http.ResponseWriter, r *http.Request) {
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
	if old == nil || old.Channel != TokenChannelMCP {
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
	created := s.createAPIToken(w, r, old.Name, preset, TokenChannelMCP, old.AllowSensitive)
	if created == nil {
		return
	}
	s.logger.Info("mcp_token_regenerated", map[string]any{"oldTokenId": req.ID, "newTokenId": created.ID})
	s.writeJSON(w, created)
}

// handleMCPTokenRevoke 撤销 MCP Token（立即生效，不删除记录）。
func (s *SearchServer) handleMCPTokenRevoke(w http.ResponseWriter, r *http.Request) {
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
	s.logger.Info("mcp_token_revoked", map[string]any{"tokenId": req.ID})
	s.writeJSON(w, map[string]any{"ok": true})
}

// mcpAuditFilterFromRequest 编译 MCP 审计过滤条件（只看 mcp 通道）。
func mcpAuditFilterFromRequest(r *http.Request) APIAuditFilter {
	query := r.URL.Query()
	filter := APIAuditFilter{
		Channel: TokenChannelMCP,
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

// handleMCPAudit 分页返回 MCP 访问记录（支持操作/结果/令牌/关键词过滤），
// 并附带 MCP 通道统计概览。
func (s *SearchServer) handleMCPAudit(w http.ResponseWriter, r *http.Request) {
	filter := mcpAuditFilterFromRequest(r)
	events, total, err := s.store.ListAPIAuditEvents(r.Context(), filter)
	if err != nil {
		s.writeError(w, err)
		return
	}
	resp := map[string]any{"entries": events, "total": total}
	if stats, err := s.store.APIAuditStats(r.Context(), TokenChannelMCP); err == nil {
		resp["stats"] = stats
	}
	s.writeJSON(w, resp)
}

// handleMCPAuditExport 按当前过滤条件导出全部 MCP 审计记录为 CSV。
func (s *SearchServer) handleMCPAuditExport(w http.ResponseWriter, r *http.Request) {
	filter := mcpAuditFilterFromRequest(r)
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
	_ = writer.Write([]string{"时间(UTC)", "令牌", "来源IP", "操作", "资源URI", "记录ID", "返回条数", "耗时(ms)", "状态码", "错误码", "请求摘要", "敏感访问"})
	for _, event := range events {
		itemID := ""
		if event.ItemID > 0 {
			itemID = strconv.FormatInt(event.ItemID, 10)
		}
		sensitive := "否"
		if event.SensitiveAccessed {
			sensitive = "是"
		}
		_ = writer.Write([]string{
			event.Timestamp.UTC().Format("2006-01-02 15:04:05"),
			event.TokenName,
			event.RemoteIP,
			event.Operation,
			event.ResourceURI,
			itemID,
			strconv.Itoa(event.ItemCount),
			strconv.FormatInt(event.DurationMs, 10),
			strconv.Itoa(event.StatusCode),
			event.ErrorCode,
			event.RequestSummary,
			sensitive,
		})
	}
	writer.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="clipvault-mcp-audit.csv"`)
	_, _ = w.Write(buf.Bytes())
}

// handleMCPAuditClear 清空 MCP 访问记录，保留数据接口（http 通道）审计。
func (s *SearchServer) handleMCPAuditClear(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearAPIAuditEvents(r.Context(), TokenChannelMCP); err != nil {
		s.writeError(w, err)
		return
	}
	s.logger.Info("mcp_audit_cleared", nil)
	s.writeJSON(w, map[string]any{"ok": true})
}

// mcpCoreToolNames 返回 v1 核心 MCP 工具名清单（管理界面能力展示用）。
func mcpCoreToolNames() []string {
	return []string{
		mcpToolSearch,
		mcpToolGetItem,
		mcpToolGetItems,
		mcpToolGetContent,
		mcpToolCount,
		mcpToolStats,
		mcpToolGroups,
	}
}
