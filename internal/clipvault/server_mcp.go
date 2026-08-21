package clipvault

import (
	"bytes"
	"encoding/csv"
	"net/http"
	"strconv"
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
			resp["tokenCount"] = countChannelTokens(tokens, TokenChannelMCP)
		}
	}
	s.writeJSON(w, resp)
}

// handleMCPConfig 更新 MCP 服务配置（立即生效，无需重启）。
func (s *SearchServer) handleMCPConfig(w http.ResponseWriter, r *http.Request) {
	s.updateExternalServiceConfig(w, r, "MCP ", "mcp_runtime_apply_failed",
		func(c *Config) *ExternalServiceConfig { return &c.MCP },
		func() (externalServiceStatus, bool) {
			if s.mcp == nil {
				return externalServiceStatus{}, false
			}
			return s.mcp.Status(), true
		})
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
// 敏感授权初始值为关闭（安全默认），创建后可在令牌列表中单独调整。
func (s *SearchServer) handleMCPTokenCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		ExpiresIn string `json:"expiresIn"`
	}
	if err := decodeJSONRequest(w, r, &req, 64<<10); err != nil {
		s.writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式错误", err)
		return
	}
	created := s.createAPIToken(w, r, req.Name, req.ExpiresIn, TokenChannelMCP, false)
	if created == nil {
		return
	}
	s.logger.Info("mcp_token_created", map[string]any{"tokenId": created.ID, "name": created.Name, "allowSensitive": created.AllowSensitive})
	s.writeJSON(w, created)
}

// handleMCPTokenSensitive 单独调整某个 MCP Token 的敏感数据授权（仅对有效 Token 生效）。
func (s *SearchServer) handleMCPTokenSensitive(w http.ResponseWriter, r *http.Request) {
	s.setTokenSensitive(w, r, TokenChannelMCP, "mcp_token_sensitive_updated")
}

// handleMCPTokenRegenerate 撤销旧 Token 并按原名称/有效期/敏感授权签发新 MCP Token。
func (s *SearchServer) handleMCPTokenRegenerate(w http.ResponseWriter, r *http.Request) {
	s.regenerateToken(w, r, TokenChannelMCP, "mcp_token_regenerated")
}

// handleMCPTokenRevoke 撤销 MCP Token（立即生效，不删除记录）。
func (s *SearchServer) handleMCPTokenRevoke(w http.ResponseWriter, r *http.Request) {
	s.revokeToken(w, r, TokenChannelMCP, "mcp_token_revoked")
}

// handleMCPAudit 分页返回 MCP 访问记录（支持操作/结果/令牌/关键词过滤），
// 并附带 MCP 通道统计概览。
func (s *SearchServer) handleMCPAudit(w http.ResponseWriter, r *http.Request) {
	filter := apiAuditFilterFromRequest(r, TokenChannelMCP)
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
	filter := apiAuditFilterFromRequest(r, TokenChannelMCP)
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
