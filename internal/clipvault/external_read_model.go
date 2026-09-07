package clipvault

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

// ── 协议中立 DTO 与请求/结果契约（MCP 工具/资源输出形态）────

// ExternalItem 是剪贴板条目的协议中立表示（MCP 工具/资源输出）。
// 托管图片的内部存储路径绝不外泄：Text 置空，仅保留元数据。
type ExternalItem struct {
	ID                   string    `json:"id"`
	CreatedAt            time.Time `json:"createdAt"`
	Kind                 string    `json:"kind"`
	Format               string    `json:"format,omitempty"`
	Text                 string    `json:"text"`
	TextTruncated        bool      `json:"textTruncated,omitempty"`
	ByteLen              int64     `json:"byteLen"`
	CharLen              int64     `json:"charLen"`
	RecordType           string    `json:"recordType"`
	ContentSHA256        *string   `json:"contentSha256"`
	ConsecutiveDuplicate bool      `json:"consecutiveDuplicate"`
	Starred              bool      `json:"starred"`
	Sensitive            bool      `json:"sensitive"`
	GroupIDs             []string  `json:"groupIds"`
	SourceApp            string    `json:"sourceApp,omitempty"`
	SourceWindow         string    `json:"sourceWindow,omitempty"`
}

// toExternalItem 把存储记录映射为协议中立 DTO。preview=true 时截断超长文本，
// 完整文本经单条获取。
func toExternalItem(item StoredItem, groupIDs []int64, preview bool) ExternalItem {
	out := ExternalItem{
		ID:                   strconv.FormatInt(item.ID, 10),
		CreatedAt:            item.CreatedAt,
		Kind:                 item.Kind,
		ByteLen:              int64(item.ByteLen),
		CharLen:              int64(item.CharLen),
		Starred:              item.Starred,
		Sensitive:            item.Sensitive,
		GroupIDs:             make([]string, 0, len(groupIDs)),
		RecordType:           string(item.RecordType),
		ContentSHA256:        item.ContentSHA256,
		ConsecutiveDuplicate: item.ConsecutiveDuplicate,
	}
	for _, gid := range groupIDs {
		out.GroupIDs = append(out.GroupIDs, strconv.FormatInt(gid, 10))
	}
	if item.SourceExeName != "" {
		out.SourceApp = item.SourceExeName
	}
	if item.SourceWindowTitle != "" {
		out.SourceWindow = item.SourceWindowTitle
	}
	if item.Format == "CLIPVAULT_IMAGE_FILE" {
		return out
	}
	out.Format = item.Format
	text := item.Text
	if preview {
		runes := []rune(text)
		if len(runes) > externalTextPreviewMax {
			text = string(runes[:externalTextPreviewMax])
			out.TextTruncated = true
		}
	}
	out.Text = text
	return out
}

// ExternalContent 是条目正文的协议中立内容。
// ImagePath 是托管图片的绝对路径，仅供协议层读取并流式输出，永不序列化。
type ExternalContent struct {
	Kind      string // "text" | "image"
	Text      string
	MimeType  string
	ImagePath string
	Sensitive bool // 实际返回的条目是否为敏感记录（供审计 sensitive_accessed）
}

// CountRequest 是计数/搜索共用的过滤条件。
type CountRequest struct {
	Query        string
	Kinds        []string
	SourceApp    string
	SourceWindow string
	Starred      *bool
	Sensitive    *bool
	GroupID      string
	From         *time.Time
	To           *time.Time
}

// SearchRequest 在过滤条件之上加分页参数。
type SearchRequest struct {
	CountRequest
	Limit  int
	Cursor string
}

// SearchResult 是搜索/列表的标准输出（JSON 形态与 MCP 工具输出一致）。
type SearchResult struct {
	Items      []ExternalItem `json:"items"`
	NextCursor *string        `json:"nextCursor"`
	HasMore    bool           `json:"hasMore"`
}

// StatsRequest 是统计请求。
type StatsRequest struct {
	From          *time.Time
	To            *time.Time
	Bucket        string // "hour" | "day"
	TopSourceApps int
}

// StatsBucket 是按类别聚合的一行统计。
type StatsBucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
	Bytes int64  `json:"bytes"`
}

// StatsTrendPoint 是时间趋势上的一个桶。
type StatsTrendPoint struct {
	Bucket time.Time `json:"bucket"`
	Count  int64     `json:"count"`
	Bytes  int64     `json:"bytes"`
}

// statsRange 是统计结果的时间范围（未指定时为 null）。
type statsRange struct {
	From *time.Time `json:"from"`
	To   *time.Time `json:"to"`
}

// StatsResult 是结构化统计输出；服务端不渲染图表，只供数据展示。
type StatsResult struct {
	Range       statsRange        `json:"range"`
	Total       int64             `json:"total"`
	TotalBytes  int64             `json:"totalBytes"`
	ByKind      []StatsBucket     `json:"byKind"`
	BySourceApp []StatsBucket     `json:"bySourceApp"`
	Trend       []StatsTrendPoint `json:"trend"`
}

// ExternalGroup 是分组的协议中立表示。
type ExternalGroup struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	ParentID         *string `json:"parentId"`
	VisibleItemCount int64   `json:"visibleItemCount"`
}

// ── 可见性谓词（唯一实现点）────────────────────────────────

// visible(item, access) = item.sensitive == false OR access.allow_sensitive
// 谓词在此下推为 SQL 条件；未授权调用方额外请求 sensitive=true 时，
// 条件合取自然产生空结果，不会绕过限制。

// externalItemScopeAnd appends the invariant external-item scope to a query
// that already has a WHERE clause. Capture events are intentionally internal.
func externalItemScopeAnd(access ReadAccess) string {
	if access.AllowSensitive {
		return " AND record_type = 'content'"
	}
	return " AND record_type = 'content' AND sensitive = false"
}

// applyExternalItemScope adds the content-only and sensitive-data boundaries
// to a WHERE fragment. This is the single guard for all external list, query,
// aggregate, stats, and group-item reads.
func applyExternalItemScope(where string, access ReadAccess) string {
	condition := "record_type = 'content'"
	if !access.AllowSensitive {
		condition += " AND sensitive = false"
	}
	if where == "" {
		return " WHERE " + condition
	}
	return where + " AND " + condition
}

// validExternalKind 报告 kind 是否在受支持的种类白名单内。
func validExternalKind(kind string) bool {
	switch kind {
	case domain.KindText, domain.KindImage, domain.KindVideo, domain.KindArchive, domain.KindOffice, domain.KindFiles:
		return true
	default:
		return false
	}
}

// buildExternalFilter 把结构化过滤编译为 WHERE 片段与参数（含可见性下推）。
// 调用方 filter sensitive=true 且未授权时，条件合取为空集，安全返回空结果。
func buildExternalFilter(req CountRequest, access ReadAccess) (string, []any, error) {
	var conds []string
	var args []any
	if q := strings.TrimSpace(req.Query); q != "" {
		conds = append(conds, `text ILIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(q)+"%")
	}
	if len(req.Kinds) > 0 {
		for _, kind := range req.Kinds {
			if !validExternalKind(kind) {
				return "", nil, fmt.Errorf("%w: unsupported kind %q", ErrExternalInvalid, kind)
			}
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(req.Kinds)), ",")
		conds = append(conds, "kind IN ("+placeholders+")")
		for _, kind := range req.Kinds {
			args = append(args, kind)
		}
	}
	if app := strings.TrimSpace(req.SourceApp); app != "" {
		conds = append(conds, `source_exe_name ILIKE ? ESCAPE '\'`)
		args = append(args, escapeLike(app))
	}
	if window := strings.TrimSpace(req.SourceWindow); window != "" {
		conds = append(conds, `source_window_title ILIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(window)+"%")
	}
	if req.Starred != nil {
		conds = append(conds, "starred = ?")
		args = append(args, *req.Starred)
	}
	if req.Sensitive != nil {
		conds = append(conds, "sensitive = ?")
		args = append(args, *req.Sensitive)
	}
	if group := strings.TrimSpace(req.GroupID); group != "" {
		gid, err := strconv.ParseInt(group, 10, 64)
		if err != nil || gid <= 0 {
			return "", nil, fmt.Errorf("%w: invalid groupId %q", ErrExternalInvalid, group)
		}
		conds = append(conds, "id IN (SELECT item_id FROM item_groups WHERE group_id = ?)")
		args = append(args, gid)
	}
	if req.From != nil {
		conds = append(conds, "created_at >= ?")
		args = append(args, req.From.UnixMilli())
	}
	if req.To != nil {
		conds = append(conds, "created_at <= ?")
		args = append(args, req.To.UnixMilli())
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	return applyExternalItemScope(where, access), args, nil
}

// ── 游标（created_at DESC, id DESC 的 keyset 分页）─────────

func encodeExternalCursor(createdAtMS, id int64) string {
	payload := "t:" + strconv.FormatInt(createdAtMS, 10) + ":" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func decodeExternalCursor(cursor string) (createdAtMS, id int64, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) > 64 {
		return 0, 0, ErrExternalInvalid
	}
	parts := strings.Split(string(raw), ":")
	if len(parts) != 3 || parts[0] != "t" {
		return 0, 0, ErrExternalInvalid
	}
	ms, err1 := strconv.ParseInt(parts[1], 10, 64)
	id, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || id <= 0 {
		return 0, 0, ErrExternalInvalid
	}
	return ms, id, nil
}

// clampExternalLimit 收敛分页大小。
func clampExternalLimit(limit int) int {
	if limit <= 0 {
		return externalDefaultLimit
	}
	if limit > externalMaxLimit {
		return externalMaxLimit
	}
	return limit
}

// parseExternalGroupID 解析外部分组 ID 参数。
func parseExternalGroupID(id string) (int64, error) {
	gid, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil || gid <= 0 {
		return 0, fmt.Errorf("%w: invalid group id %q", ErrExternalInvalid, id)
	}
	return gid, nil
}

// externalGroupVisibleCond returns the complete external-item constraint for
// group count subqueries, whose clipboard_items alias is i.
func externalGroupVisibleCond(access ReadAccess) string {
	if access.AllowSensitive {
		return " AND i.record_type = 'content'"
	}
	return " AND i.record_type = 'content' AND i.sensitive = false"
}
