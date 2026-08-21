package clipvault

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// maxAPIAuditLogRows 是数据接口审计表的保留上限，超出后按 id 淘汰最旧记录。
const maxAPIAuditLogRows = 1000

// APIAuditEvent 是一次外部访问（HTTP 数据接口或 MCP）的审计记录。绝不包含
// 剪贴板正文、Token 原文或 Authorization 头（AUDIT-02）。RequestSummary 是
// 查询条件的紧凑摘要（字段.操作符=值，逐段截断）；RequestBody 是调用方提交
// 的原始请求体（带上限截断），供本机用户审计"谁问了什么"。
// Channel 区分 http/mcp；ResourceURI 记录 MCP 资源 URI（HTTP 为空）；
// SensitiveAccessed 仅在实际返回了敏感行/敏感内容时为 true。
type APIAuditEvent struct {
	ID                int64     `json:"id"`
	Timestamp         time.Time `json:"timestamp"`
	Channel           string    `json:"channel"`   // "http" | "mcp"
	TokenID           int64     `json:"tokenId"`   // 0 = 无关联（如认证失败）
	TokenName         string    `json:"tokenName"` // 认证失败时为空
	RemoteIP          string    `json:"remoteIP"`
	Method            string    `json:"method"`
	Endpoint          string    `json:"endpoint"`
	Operation         string    `json:"operation"`
	ResourceURI       string    `json:"resourceUri,omitempty"`
	SensitiveAccessed bool      `json:"sensitiveAccessed"`
	ItemID            int64     `json:"itemId"`    // 0 = 不适用
	ItemCount         int       `json:"itemCount"` // 返回条数，不适用为 0
	DurationMs        int64     `json:"durationMs"`
	StatusCode        int       `json:"statusCode"`
	ErrorCode         string    `json:"errorCode,omitempty"`
	RequestSummary    string    `json:"requestSummary,omitempty"`
	RequestBody       string    `json:"requestBody,omitempty"`
}

// APIAuditFilter 是审计列表的过滤条件；零值字段表示不过滤。
type APIAuditFilter struct {
	Channel    string   // "" 全部 / "http" / "mcp"
	Operations []string // 操作类型白名单
	TokenID    int64    // 0 = 全部令牌
	Result     string   // "" 全部 / "success" <400 / "error" >=400
	Query      string   // 关键词，匹配令牌名/端点/操作/错误码/请求摘要
	Limit      int
	Offset     int
}

// APIAuditStats 是审计记录的全量统计概览。
type APIAuditStats struct {
	Total         int64 `json:"total"`
	Success       int64 `json:"success"`
	Error         int64 `json:"error"`
	AvgDurationMs int64 `json:"avgDurationMs"`
	Last24h       int64 `json:"last24h"`
}

func msToTimePtr(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

// scanAPIToken 扫描 api_tokens 行（列顺序固定）。
func scanAPIToken(row rowScanner, token *APIToken) error {
	var createdAtMS, expiresAtMS, lastUsedAtMS, revokedAtMS int64
	if err := row.Scan(&token.ID, &token.Name, &token.Channel, &token.AllowSensitive,
		&createdAtMS, &expiresAtMS, &lastUsedAtMS, &revokedAtMS); err != nil {
		return err
	}
	token.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	token.ExpiresAt = msToTimePtr(expiresAtMS)
	token.LastUsedAt = msToTimePtr(lastUsedAtMS)
	token.RevokedAt = msToTimePtr(revokedAtMS)
	return nil
}

const apiTokenColumns = `id, name, channel, allow_sensitive, created_at, expires_at, last_used_at, revoked_at`

// CreateAPIToken 持久化一个 Token 的哈希与元数据，返回自增 ID。
// channel 必须是 http|mcp；allowSensitive 由调用方决定（新令牌默认关闭，
// 重新生成保持原值）。
func (s *Store) CreateAPIToken(ctx context.Context, name string, tokenHash []byte, expiresAtMS int64, channel string, allowSensitive bool) (int64, error) {
	if !validTokenChannel(channel) {
		return 0, fmt.Errorf("invalid token channel %q", channel)
	}
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (name, token_hash, channel, allow_sensitive, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		name, hex.EncodeToString(tokenHash), channel, allowSensitive, time.Now().UTC().UnixMilli(), expiresAtMS).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// AuthenticateAPIToken 按哈希查找 Token 并校验撤销/过期状态与通道匹配
// （AUTH-03 / SEC-07：HTTP 与 MCP 令牌互相隔离）。哈希比较使用恒定时间比较；
// 过期/撤销/通道不匹配与不存在区分错误，供上层映射 401。
func (s *Store) AuthenticateAPIToken(ctx context.Context, raw string, channel string) (APIToken, error) {
	var token APIToken
	var storedHash string
	row := s.db.QueryRowContext(ctx,
		`SELECT `+apiTokenColumns+`, token_hash FROM api_tokens WHERE token_hash = ?`,
		hex.EncodeToString(hashAPIToken(raw)))
	var createdAtMS, expiresAtMS, lastUsedAtMS, revokedAtMS int64
	err := row.Scan(&token.ID, &token.Name, &token.Channel, &token.AllowSensitive,
		&createdAtMS, &expiresAtMS, &lastUsedAtMS, &revokedAtMS, &storedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return token, errAPITokenNotFound
	}
	if err != nil {
		return token, err
	}
	if !constantTimeHashEqual(storedHash, hashAPIToken(raw)) {
		return token, errAPITokenNotFound
	}
	token.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	token.ExpiresAt = msToTimePtr(expiresAtMS)
	token.LastUsedAt = msToTimePtr(lastUsedAtMS)
	token.RevokedAt = msToTimePtr(revokedAtMS)
	if token.RevokedAt != nil {
		return token, errAPITokenRevoked
	}
	if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
		return token, errAPITokenExpired
	}
	if token.Channel != channel {
		return token, errAPITokenChannel
	}
	return token, nil
}

// ListAPITokens 返回全部 Token 元数据（含已撤销，供管理界面展示）。
func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+apiTokenColumns+` FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tokens := []APIToken{}
	for rows.Next() {
		var token APIToken
		if err := scanAPIToken(rows, &token); err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, rows.Err()
}

// RevokeAPIToken 撤销一个 Token（幂等：同通道已撤销视为成功，不改动撤销时间）。
// 通道必须匹配（SEC-07 通道隔离在管理面同样成立）：跨通道与不存在一样
// 返回 errAPITokenNotFound。
func (s *Store) RevokeAPIToken(ctx context.Context, id int64, channel string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND channel = ? AND revoked_at = 0`,
		time.Now().UTC().UnixMilli(), id, channel)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		// 同通道已撤销 → 幂等成功；不存在或通道不匹配 → not found。
		var revokedAtMS int64
		row := s.db.QueryRowContext(ctx, `SELECT revoked_at FROM api_tokens WHERE id = ? AND channel = ?`, id, channel)
		if scanErr := row.Scan(&revokedAtMS); errors.Is(scanErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: %d", errAPITokenNotFound, id)
		} else if scanErr != nil {
			return scanErr
		}
	}
	return nil
}

// GetAPITokenByID 按主键返回 Token 元数据；不存在返回 errAPITokenNotFound。
func (s *Store) GetAPITokenByID(ctx context.Context, id int64) (APIToken, error) {
	var token APIToken
	row := s.db.QueryRowContext(ctx, `SELECT `+apiTokenColumns+` FROM api_tokens WHERE id = ?`, id)
	if err := scanAPIToken(row, &token); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return token, fmt.Errorf("%w: %d", errAPITokenNotFound, id)
		}
		return token, err
	}
	return token, nil
}

// SetAPITokenSensitive 修改 Token 的敏感数据授权（仅对未撤销且未过期的 Token 生效，
// 通道必须匹配）。幂等：已是目标值时直接成功。Token 不存在或已失效返回 errAPITokenNotFound。
func (s *Store) SetAPITokenSensitive(ctx context.Context, id int64, channel string, allow bool) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET allow_sensitive = ? WHERE id = ? AND channel = ? AND revoked_at = 0 AND (expires_at = 0 OR expires_at > ?)`,
		allow, id, channel, time.Now().UTC().UnixMilli())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %d", errAPITokenNotFound, id)
	}
	return nil
}

// TouchAPITokenLastUsed 异步更新 Token 的最后使用时间（单调递增，失败静默）。
func (s *Store) TouchAPITokenLastUsed(ctx context.Context, id int64, tsMS int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ? WHERE id = ? AND last_used_at < ?`, tsMS, id, tsMS)
	return err
}

// InsertAPIAuditEvents 批量写入审计记录并淘汰最旧行；由异步 writer 调用。
func (s *Store) InsertAPIAuditEvents(ctx context.Context, events []APIAuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, event := range events {
		// 防御性归一化：通道缺失时归入 HTTP 通道，避免空通道行逃过按通道的过滤与清理。
		channel := event.Channel
		if channel == "" {
			channel = TokenChannelHTTP
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO api_audit_log (ts, token_id, token_name, remote_ip, method, endpoint, operation, item_id, item_count, duration_ms, status_code, error_code, request_summary, request_body, channel, resource_uri, sensitive_accessed)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.Timestamp.UTC().UnixMilli(), event.TokenID, event.TokenName, event.RemoteIP,
			event.Method, event.Endpoint, event.Operation, event.ItemID, event.ItemCount,
			event.DurationMs, event.StatusCode, event.ErrorCode, event.RequestSummary, event.RequestBody,
			channel, event.ResourceURI, event.SensitiveAccessed); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM api_audit_log WHERE id NOT IN (
			SELECT id FROM api_audit_log ORDER BY id DESC LIMIT ?)`, maxAPIAuditLogRows); err != nil {
		return err
	}
	return tx.Commit()
}

// auditFilterClauses 把过滤条件编译为 WHERE 片段与参数（全参数化，无原文拼接）。
func auditFilterClauses(filter APIAuditFilter) (string, []any) {
	var conds []string
	var args []any
	if filter.Channel != "" {
		conds = append(conds, "channel = ?")
		args = append(args, filter.Channel)
	}
	if len(filter.Operations) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(filter.Operations)), ",")
		conds = append(conds, "operation IN ("+placeholders+")")
		for _, op := range filter.Operations {
			args = append(args, op)
		}
	}
	if filter.TokenID > 0 {
		conds = append(conds, "token_id = ?")
		args = append(args, filter.TokenID)
	}
	switch filter.Result {
	case "success":
		conds = append(conds, "status_code < 400")
	case "error":
		conds = append(conds, "status_code >= 400")
	}
	if q := strings.TrimSpace(filter.Query); q != "" {
		pattern := "%" + q + "%"
		conds = append(conds, `(token_name ILIKE ? OR endpoint ILIKE ? OR operation ILIKE ? OR error_code ILIKE ? OR request_summary ILIKE ?)`)
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListAPIAuditEvents 按时间倒序返回（可过滤的）审计记录与过滤后总数。
func (s *Store) ListAPIAuditEvents(ctx context.Context, filter APIAuditFilter) ([]APIAuditEvent, int, error) {
	where, args := auditFilterClauses(filter)
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM api_audit_log`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	listArgs := append(append([]any{}, args...), filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, token_id, token_name, remote_ip, method, endpoint, operation, item_id, item_count, duration_ms, status_code, error_code, request_summary, request_body, channel, resource_uri, sensitive_accessed
		 FROM api_audit_log`+where+` ORDER BY id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	events := []APIAuditEvent{}
	for rows.Next() {
		var event APIAuditEvent
		var tsMS int64
		var summary, body, resourceURI sql.NullString
		if err := rows.Scan(&event.ID, &tsMS, &event.TokenID, &event.TokenName, &event.RemoteIP,
			&event.Method, &event.Endpoint, &event.Operation, &event.ItemID, &event.ItemCount,
			&event.DurationMs, &event.StatusCode, &event.ErrorCode, &summary, &body,
			&event.Channel, &resourceURI, &event.SensitiveAccessed); err != nil {
			return nil, 0, err
		}
		event.Timestamp = time.UnixMilli(tsMS).UTC()
		event.RequestSummary = summary.String
		event.RequestBody = body.String
		event.ResourceURI = resourceURI.String
		events = append(events, event)
	}
	return events, total, rows.Err()
}

// APIAuditStats 返回审计记录的统计概览（不受列表过滤影响；channel 为空时
// 统计全部通道，"http"/"mcp" 只统计该通道）。
func (s *Store) APIAuditStats(ctx context.Context, channel string) (APIAuditStats, error) {
	var stats APIAuditStats
	var avg sql.NullFloat64
	where := ""
	args := []any{}
	if channel != "" {
		where = " WHERE channel = ?"
		args = append(args, channel)
	}
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*),
			count(*) FILTER (WHERE status_code < 400),
			count(*) FILTER (WHERE status_code >= 400),
			avg(duration_ms),
			count(*) FILTER (WHERE ts >= ?)
		 FROM api_audit_log`+where,
		append([]any{time.Now().UTC().UnixMilli() - 24*60*60*1000}, args...)...).
		Scan(&stats.Total, &stats.Success, &stats.Error, &avg, &stats.Last24h)
	if err != nil {
		return stats, err
	}
	if avg.Valid {
		stats.AvgDurationMs = int64(avg.Float64)
	}
	return stats, nil
}

// ClearAPIAuditEvents 清空指定通道（http|mcp）的审计记录，另一通道保留。
func (s *Store) ClearAPIAuditEvents(ctx context.Context, channel string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_audit_log WHERE channel = ?`, channel)
	return err
}

// ItemGroupIDs 批量返回每个条目的第一个分组 ID（无分组则不出现）。
func (s *Store) ItemGroupIDs(ctx context.Context, ids []int64) (map[int64]int64, error) {
	result := map[int64]int64{}
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_id, MIN(group_id) FROM item_groups WHERE item_id IN (`+placeholders+`) GROUP BY item_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID, groupID int64
		if err := rows.Scan(&itemID, &groupID); err != nil {
			return nil, err
		}
		result[itemID] = groupID
	}
	return result, rows.Err()
}

// ItemAllGroupIDs 批量返回每个条目的全部分组 ID（升序；无分组则不出现）。
// 供外部读取服务构造 GroupIDs 列表，避免逐条 N+1 查询。
func (s *Store) ItemAllGroupIDs(ctx context.Context, ids []int64) (map[int64][]int64, error) {
	result := map[int64][]int64{}
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT item_id, group_id FROM item_groups WHERE item_id IN (`+placeholders+`) ORDER BY item_id ASC, group_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID, groupID int64
		if err := rows.Scan(&itemID, &groupID); err != nil {
			return nil, err
		}
		result[itemID] = append(result[itemID], groupID)
	}
	return result, rows.Err()
}
