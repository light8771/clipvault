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

// APIAuditEvent 是一次外部数据 API 操作的审计记录。绝不包含剪贴板正文、
// 原始搜索词、Token 原文或 Authorization 头（AUDIT-02）。
type APIAuditEvent struct {
	ID         int64     `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	TokenID    int64     `json:"tokenId"`   // 0 = 无关联（如认证失败）
	TokenName  string    `json:"tokenName"` // 认证失败时为空
	RemoteIP   string    `json:"remoteIP"`
	Method     string    `json:"method"`
	Endpoint   string    `json:"endpoint"`
	Operation  string    `json:"operation"`
	ItemID     int64     `json:"itemId"`    // 0 = 不适用
	ItemCount  int       `json:"itemCount"` // 返回条数，不适用为 0
	DurationMs int64     `json:"durationMs"`
	StatusCode int       `json:"statusCode"`
	ErrorCode  string    `json:"errorCode,omitempty"`
}

func msToTimePtr(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func timePtrToMS(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixMilli()
}

// scanAPIToken 扫描 api_tokens 行（列顺序固定）。
func scanAPIToken(row rowScanner, token *APIToken) error {
	var createdAtMS, expiresAtMS, lastUsedAtMS, revokedAtMS int64
	if err := row.Scan(&token.ID, &token.Name, &createdAtMS, &expiresAtMS, &lastUsedAtMS, &revokedAtMS); err != nil {
		return err
	}
	token.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	token.ExpiresAt = msToTimePtr(expiresAtMS)
	token.LastUsedAt = msToTimePtr(lastUsedAtMS)
	token.RevokedAt = msToTimePtr(revokedAtMS)
	return nil
}

const apiTokenColumns = `id, name, created_at, expires_at, last_used_at, revoked_at`

// CreateAPIToken 持久化一个 Token 的哈希与元数据，返回自增 ID。
func (s *Store) CreateAPIToken(ctx context.Context, name string, tokenHash []byte, expiresAtMS int64) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (name, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?) RETURNING id`,
		name, hex.EncodeToString(tokenHash), time.Now().UTC().UnixMilli(), expiresAtMS).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// AuthenticateAPIToken 按哈希查找 Token 并校验撤销/过期状态（AUTH-03）。
// 哈希比较使用恒定时间比较；过期/撤销与不存在区分错误，供上层映射 401。
func (s *Store) AuthenticateAPIToken(ctx context.Context, raw string) (APIToken, error) {
	var token APIToken
	var storedHash string
	row := s.db.QueryRowContext(ctx,
		`SELECT `+apiTokenColumns+`, token_hash FROM api_tokens WHERE token_hash = ?`,
		hex.EncodeToString(hashAPIToken(raw)))
	var createdAtMS, expiresAtMS, lastUsedAtMS, revokedAtMS int64
	err := row.Scan(&token.ID, &token.Name, &createdAtMS, &expiresAtMS, &lastUsedAtMS, &revokedAtMS, &storedHash)
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

// RevokeAPIToken 撤销一个 Token（幂等：已撤销则刷新撤销时间不报错）。
func (s *Store) RevokeAPIToken(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at = 0`,
		time.Now().UTC().UnixMilli(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var revokedAtMS int64
		row := s.db.QueryRowContext(ctx, `SELECT revoked_at FROM api_tokens WHERE id = ?`, id)
		if scanErr := row.Scan(&revokedAtMS); errors.Is(scanErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: %d", errAPITokenNotFound, id)
		} else if scanErr != nil {
			return scanErr
		}
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
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO api_audit_log (ts, token_id, token_name, remote_ip, method, endpoint, operation, item_id, item_count, duration_ms, status_code, error_code)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.Timestamp.UTC().UnixMilli(), event.TokenID, event.TokenName, event.RemoteIP,
			event.Method, event.Endpoint, event.Operation, event.ItemID, event.ItemCount,
			event.DurationMs, event.StatusCode, event.ErrorCode); err != nil {
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

// ListAPIAuditEvents 按时间倒序返回审计记录与总数。
func (s *Store) ListAPIAuditEvents(ctx context.Context, limit, offset int) ([]APIAuditEvent, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM api_audit_log`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, ts, token_id, token_name, remote_ip, method, endpoint, operation, item_id, item_count, duration_ms, status_code, error_code
		 FROM api_audit_log ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	events := []APIAuditEvent{}
	for rows.Next() {
		var event APIAuditEvent
		var tsMS int64
		if err := rows.Scan(&event.ID, &tsMS, &event.TokenID, &event.TokenName, &event.RemoteIP,
			&event.Method, &event.Endpoint, &event.Operation, &event.ItemID, &event.ItemCount,
			&event.DurationMs, &event.StatusCode, &event.ErrorCode); err != nil {
			return nil, 0, err
		}
		event.Timestamp = time.UnixMilli(tsMS).UTC()
		events = append(events, event)
	}
	return events, total, rows.Err()
}

// ClearAPIAuditEvents 清空数据接口审计记录。
func (s *Store) ClearAPIAuditEvents(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_audit_log`)
	return err
}

// GetByIDs 用一条 IN 查询批量读取条目，返回 id → 条目 的映射（不存在的 ID 不出现）。
// 替代逐条 GetByID 的 N+1 查询，降低 batch-get 对并发槽位的占用。
func (s *Store) GetByIDs(ctx context.Context, ids []int64) (map[int64]StoredItem, error) {
	result := map[int64]StoredItem{}
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+clipboardItemColumns+" FROM clipboard_items WHERE id IN ("+placeholders+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			return nil, err
		}
		result[item.ID] = item
	}
	return result, rows.Err()
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
