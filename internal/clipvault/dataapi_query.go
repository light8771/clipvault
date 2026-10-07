package clipvault

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Query DSL：filter/orderBy/cursor 的解析、allowlist 校验与 SQL 构建。
// 这里只允许有限的字段与操作符白名单，永不拼接用户原文表达式（DATA-02/03）。

type dataAPIOrder struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

type dataAPIQueryRequest struct {
	Resource  string                                `json:"resource"`
	Filter    map[string]map[string]json.RawMessage `json:"filter"`
	OrderBy   []dataAPIOrder                        `json:"orderBy"`
	Limit     int                                   `json:"limit"`
	Cursor    string                                `json:"cursor"`
	GroupBy   []string                              `json:"groupBy"`
	Aggregate map[string]json.RawMessage            `json:"aggregate"`
}

// dataAPIFieldKind 决定字段允许的操作符与值类型。
type dataAPIFieldSpec struct {
	column string // 实际 SQL 列（空表示特殊处理，如 groupId）
	kind   string // int / time / string / bool / group
}

var dataAPIFilterFields = map[string]dataAPIFieldSpec{
	"id":           {column: "id", kind: "int"},
	"createdAt":    {column: "created_at", kind: "time"},
	"kind":         {column: "kind", kind: "string"},
	"text":         {column: "text", kind: "string"},
	"sourceApp":    {column: "source_exe_name", kind: "string"},
	"sourceWindow": {column: "source_window_title", kind: "string"},
	"starred":      {column: "starred", kind: "bool"},
	"sensitive":    {column: "sensitive", kind: "bool"},
	"groupId":      {kind: "group"},
}

// dataAPIGroupFields 是 groupBy 的字段白名单。
var dataAPIGroupFields = map[string]string{
	"sourceApp": "source_exe_name",
	"kind":      "kind",
	"starred":   "starred",
	"sensitive": "sensitive",
}

// dataAPIQueryPlan 是校验后的查询计划：SQL 片段与参数已经过白名单映射。
type dataAPIQueryPlan struct {
	where     string
	args      []any
	orderSQL  string // 不含 ORDER BY 前缀
	orderDesc bool
	orderByID bool // true 时游标只含 id
	limit     int
	cursorMS  int64 // 游标：上页末行 created_at（orderByID 时无用）
	cursorID  int64 // 游标：上页末行 id
	hasCursor bool

	aggregate  bool
	groupField string // groupBy 的对外字段名
	groupCol   string // groupBy 的 SQL 列
}

func invalidQuery(format string, args ...any) *dataAPIError {
	return newDataAPIError(http.StatusBadRequest, "invalid_request", fmt.Sprintf(format, args...))
}

func unsupportedFilter(field string) *dataAPIError {
	return newDataAPIError(http.StatusBadRequest, "unsupported_filter", "Unsupported or invalid filter: "+field)
}

// parseDataAPIQuery 校验并编译查询 DSL。所有失败都返回稳定的 4xx 错误契约。
func parseDataAPIQuery(req *dataAPIQueryRequest) (*dataAPIQueryPlan, *dataAPIError) {
	if req.Resource != "clipboardItems" {
		return nil, invalidQuery("Unknown resource %q.", req.Resource)
	}
	plan := &dataAPIQueryPlan{limit: req.Limit}
	if plan.limit <= 0 {
		plan.limit = dataAPIDefaultLimit
	}
	if plan.limit > dataAPIMaxLimit {
		plan.limit = dataAPIMaxLimit
	}

	if len(req.Aggregate) > 0 || len(req.GroupBy) > 0 {
		if err := plan.compileAggregate(req); err != nil {
			return nil, err
		}
	} else if err := plan.compileOrder(req.OrderBy); err != nil {
		return nil, err
	}

	var conds []string
	for field, ops := range req.Filter {
		spec, ok := dataAPIFilterFields[field]
		if !ok {
			return nil, unsupportedFilter(field)
		}
		if len(ops) == 0 {
			return nil, invalidQuery("Filter %s requires at least one operator.", field)
		}
		for op, raw := range ops {
			cond, args, err := spec.compileOp(field, op, raw)
			if err != nil {
				return nil, err
			}
			conds = append(conds, cond)
			plan.args = append(plan.args, args...)
		}
	}
	if req.Cursor != "" {
		if plan.aggregate {
			return nil, invalidQuery("Cursor pagination is not supported for aggregate queries.")
		}
		if err := plan.decodeCursor(req.Cursor); err != nil {
			return nil, err
		}
		conds = append(conds, plan.cursorCond())
	}
	if len(conds) > 0 {
		plan.where = " WHERE " + strings.Join(conds, " AND ")
	}
	return plan, nil
}

// compileOrder 校验 orderBy 并生成 ORDER BY 片段（默认 createdAt desc）。
func (p *dataAPIQueryPlan) compileOrder(orders []dataAPIOrder) *dataAPIError {
	if len(orders) > 1 {
		return invalidQuery("At most one orderBy entry is supported.")
	}
	field, direction := "createdAt", "desc"
	if len(orders) == 1 {
		field, direction = orders[0].Field, strings.ToLower(orders[0].Direction)
	}
	if field != "createdAt" && field != "id" {
		return invalidQuery("Unsupported orderBy field %q.", field)
	}
	if direction != "asc" && direction != "desc" {
		return invalidQuery("Unsupported orderBy direction %q.", direction)
	}
	p.orderDesc = direction == "desc"
	p.orderByID = field == "id"
	dir := "ASC"
	if p.orderDesc {
		dir = "DESC"
	}
	if p.orderByID {
		p.orderSQL = "id " + dir
	} else {
		p.orderSQL = "created_at " + dir + ", id " + dir
	}
	return nil
}

// compileAggregate 校验 groupBy + aggregate count 查询（V1 只允许 count）。
func (p *dataAPIQueryPlan) compileAggregate(req *dataAPIQueryRequest) *dataAPIError {
	if len(req.GroupBy) != 1 {
		return invalidQuery("Exactly one groupBy field is required for aggregate queries.")
	}
	if len(req.Aggregate) != 1 {
		return invalidQuery("Exactly one aggregate is required.")
	}
	col, ok := dataAPIGroupFields[req.GroupBy[0]]
	if !ok {
		return unsupportedFilter(req.GroupBy[0])
	}
	for fn, raw := range req.Aggregate {
		if fn != "count" {
			return invalidQuery("Unsupported aggregate %q.", fn)
		}
		var star string
		if err := json.Unmarshal(raw, &star); err != nil || star != "*" {
			return invalidQuery("Aggregate count only accepts \"*\".")
		}
	}
	p.aggregate = true
	p.groupField = req.GroupBy[0]
	p.groupCol = col
	// 聚合排序：count 或分组字段。
	field, direction := "count", "desc"
	if len(req.OrderBy) == 1 {
		field, direction = req.OrderBy[0].Field, strings.ToLower(req.OrderBy[0].Direction)
	}
	if len(req.OrderBy) > 1 || (field != "count" && field != p.groupField) {
		return invalidQuery("Aggregate orderBy only supports \"count\" or the groupBy field.")
	}
	if direction != "asc" && direction != "desc" {
		return invalidQuery("Unsupported orderBy direction %q.", direction)
	}
	col2 := "c"
	if field == p.groupField {
		col2 = p.groupCol
	}
	dir := "ASC"
	if direction == "desc" {
		dir = "DESC"
	}
	p.orderSQL = col2 + " " + dir
	return nil
}

// compileOp 把单个字段操作符编译为 SQL 条件；值类型按字段类型严格解析。
func (spec dataAPIFieldSpec) compileOp(field, op string, raw json.RawMessage) (string, []any, *dataAPIError) {
	if field == "kind" {
		negate := op == "neq"
		if negate {
			op = "eq"
		}
		condition, args, err := (dataAPIFieldSpec{column: "content_kind", kind: "string"}).compileStringOp(field, op, raw)
		if err != nil {
			return "", nil, err
		}
		prefix := ""
		if negate {
			prefix = "NOT "
		}
		return prefix + "EXISTS (SELECT 1 FROM unnest(list_append(" + contentKindsSQL + ", kind)) AS kinds(content_kind) WHERE " + condition + ")", args, nil
	}
	switch spec.kind {
	case "int", "group":
		return spec.compileIntOp(field, op, raw)
	case "time":
		return spec.compileTimeOp(field, op, raw)
	case "string":
		return spec.compileStringOp(field, op, raw)
	case "bool":
		return spec.compileBoolOp(field, op, raw)
	default:
		return "", nil, unsupportedFilter(field)
	}
}

func integerValue(raw json.RawMessage) (int64, *dataAPIError) {
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, invalidQuery("Expected an integer.")
	}
	return value, nil
}

func integerValues(raw json.RawMessage) ([]int64, *dataAPIError) {
	var list []int64
	if err := json.Unmarshal(raw, &list); err != nil || len(list) == 0 {
		return nil, invalidQuery("Expected a non-empty integer array.")
	}
	if len(list) > 100 {
		return nil, invalidQuery("The \"in\" operator accepts at most 100 values.")
	}
	return list, nil
}

func (spec dataAPIFieldSpec) compileIntOp(field, op string, raw json.RawMessage) (string, []any, *dataAPIError) {
	column := spec.column
	toAny := func(v []int64) []any {
		out := make([]any, len(v))
		for i, value := range v {
			out[i] = value
		}
		return out
	}
	switch op {
	case "in":
		values, err := integerValues(raw)
		if err != nil {
			return "", nil, err
		}
		if (field == "id" || spec.kind == "group") && slices.ContainsFunc(values, func(value int64) bool { return value <= 0 }) {
			return "", nil, invalidQuery("Field %s expects positive IDs.", field)
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
		if spec.kind == "group" {
			return "id IN (SELECT item_id FROM item_groups WHERE group_id IN (" + ph + "))", toAny(values), nil
		}
		return column + " IN (" + ph + ")", toAny(values), nil
	case "eq", "neq", "gte", "gt", "lte", "lt":
		if spec.kind == "group" {
			if op != "eq" && op != "neq" {
				return "", nil, unsupportedFilter(field + "." + op)
			}
		}
		value, err := integerValue(raw)
		if err != nil {
			return "", nil, err
		}
		if (field == "id" || spec.kind == "group") && value <= 0 {
			return "", nil, invalidQuery("Field %s expects a positive ID.", field)
		}
		if op == "eq" {
			if spec.kind == "group" {
				return "id IN (SELECT item_id FROM item_groups WHERE group_id = ?)", []any{value}, nil
			}
			return column + " = ?", []any{value}, nil
		}
		if op == "neq" {
			if spec.kind == "group" {
				return "id NOT IN (SELECT item_id FROM item_groups WHERE group_id = ?)", []any{value}, nil
			}
			return column + " <> ?", []any{value}, nil
		}
		symbol := map[string]string{"gte": ">=", "gt": ">", "lte": "<=", "lt": "<"}[op]
		return column + " " + symbol + " ?", []any{value}, nil
	default:
		return "", nil, unsupportedFilter(field + "." + op)
	}
}

func (spec dataAPIFieldSpec) compileTimeOp(field, op string, raw json.RawMessage) (string, []any, *dataAPIError) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", nil, invalidQuery("Field %s expects an RFC3339 datetime string.", field)
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return "", nil, invalidQuery("Field %s has an invalid datetime %q.", field, text)
	}
	symbol, ok := map[string]string{"gte": ">=", "gt": ">", "lte": "<=", "lt": "<"}[op]
	if !ok {
		return "", nil, unsupportedFilter(field + "." + op)
	}
	return spec.column + " " + symbol + " ?", []any{parsed.UnixMilli()}, nil
}

func (spec dataAPIFieldSpec) compileStringOp(field, op string, raw json.RawMessage) (string, []any, *dataAPIError) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		switch op {
		case "eq":
			return spec.column + " = ?", []any{value}, nil
		case "neq":
			return spec.column + " <> ?", []any{value}, nil
		case "contains", "startsWith", "endsWith":
			pattern := "%" + escapeLike(value) + "%"
			if op == "startsWith" {
				pattern = escapeLike(value) + "%"
			} else if op == "endsWith" {
				pattern = "%" + escapeLike(value)
			}
			return spec.column + ` ILIKE ? ESCAPE '\'`, []any{pattern}, nil
		case "in":
			return "", nil, invalidQuery("Field %s operator \"in\" expects a string array.", field)
		default:
			return "", nil, unsupportedFilter(field + "." + op)
		}
	}
	if op != "in" {
		return "", nil, invalidQuery("Field %s expects a string value.", field)
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil || len(list) == 0 {
		return "", nil, invalidQuery("Field %s operator \"in\" expects a non-empty string array.", field)
	}
	if len(list) > 100 {
		return "", nil, invalidQuery("The \"in\" operator accepts at most 100 values.")
	}
	args := make([]any, len(list))
	for i, v := range list {
		args[i] = v
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(list)), ",")
	return spec.column + " IN (" + ph + ")", args, nil
}

func (spec dataAPIFieldSpec) compileBoolOp(field, op string, raw json.RawMessage) (string, []any, *dataAPIError) {
	if op != "eq" && op != "neq" {
		return "", nil, unsupportedFilter(field + "." + op)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", nil, invalidQuery("Field %s expects a boolean value.", field)
	}
	if op == "eq" {
		return spec.column + " = ?", []any{value}, nil
	}
	return spec.column + " <> ?", []any{value}, nil
}

// ── 游标（keyset 分页，对外不透明）────────────────────────

// encodeCursor 生成不透明游标：t:<createdAtMS>:<id> 或 i:<id>。
func (p *dataAPIQueryPlan) encodeCursor(createdAtMS, id int64) string {
	payload := "t:" + strconv.FormatInt(createdAtMS, 10) + ":" + strconv.FormatInt(id, 10)
	if p.orderByID {
		payload = "i:" + strconv.FormatInt(id, 10)
	}
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// decodeCursor 解析并校验游标形态与当前排序方式匹配。
func (p *dataAPIQueryPlan) decodeCursor(cursor string) *dataAPIError {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	invalid := newDataAPIError(http.StatusBadRequest, "invalid_cursor", "The cursor is invalid.")
	if err != nil || len(raw) > 64 {
		return invalid
	}
	parts := strings.Split(string(raw), ":")
	if p.orderByID {
		if len(parts) != 2 || parts[0] != "i" {
			return invalid
		}
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || id <= 0 {
			return invalid
		}
		p.cursorID, p.hasCursor = id, true
		return nil
	}
	if len(parts) != 3 || parts[0] != "t" {
		return invalid
	}
	ms, err1 := strconv.ParseInt(parts[1], 10, 64)
	id, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || id <= 0 {
		return invalid
	}
	p.cursorMS, p.cursorID, p.hasCursor = ms, id, true
	return nil
}

// cursorCond 返回 keyset 过滤条件（严格落在上一页最后一行之后）。
func (p *dataAPIQueryPlan) cursorCond() string {
	if p.orderByID {
		if p.orderDesc {
			return "id < ?"
		}
		return "id > ?"
	}
	if p.orderDesc {
		return "(created_at < ? OR (created_at = ? AND id < ?))"
	}
	return "(created_at > ? OR (created_at = ? AND id > ?))"
}

// cursorArgs 返回游标条件对应的参数。
func (p *dataAPIQueryPlan) cursorArgs() []any {
	if !p.hasCursor {
		return nil
	}
	if p.orderByID {
		return []any{p.cursorID}
	}
	return []any{p.cursorMS, p.cursorMS, p.cursorID}
}

// ── 请求摘要（审计展示用）──────────────────────────────────

// summarizeDataAPIQuery 把查询请求编译为紧凑可读的条件摘要，写入审计记录，
// 让本机用户能看清"每次调用到底问了什么"。只含调用方主动提交的查询条件
// （字段.操作符=值，逐段截断），不含剪贴板正文或 Token 原文。
func summarizeDataAPIQuery(req *dataAPIQueryRequest) string {
	var parts []string
	fields := make([]string, 0, len(req.Filter))
	for field := range req.Filter {
		fields = append(fields, field)
	}
	sort.Strings(fields) // map 遍历无序，排序保证摘要稳定可比较
	for _, field := range fields {
		ops := req.Filter[field]
		opNames := make([]string, 0, len(ops))
		for op := range ops {
			opNames = append(opNames, op)
		}
		sort.Strings(opNames)
		for _, op := range opNames {
			parts = append(parts, field+"."+op+"="+truncateRunes(string(ops[op]), 48))
		}
	}
	if len(req.GroupBy) > 0 {
		parts = append(parts, "groupBy="+strings.Join(req.GroupBy, ","))
	}
	if len(req.OrderBy) > 0 {
		order := req.OrderBy[0]
		if order.Direction == "" {
			order.Direction = "desc"
		}
		parts = append(parts, "sort="+order.Field+" "+order.Direction)
	}
	if req.Limit > 0 {
		parts = append(parts, "limit="+strconv.Itoa(req.Limit))
	}
	if req.Cursor != "" {
		parts = append(parts, "cursor=…")
	}
	return truncateRunes(strings.Join(parts, " · "), 400)
}

// summarizeDataAPIBatchIDs 摘要 batch-get 的 ID 列表（最多展示 12 个，其余计数）。
func summarizeDataAPIBatchIDs(ids []string) string {
	const show = 12
	if len(ids) <= show {
		return "ids=[" + strings.Join(ids, ",") + "]"
	}
	return "ids=[" + strings.Join(ids[:show], ",") + " +" + strconv.Itoa(len(ids)-show) + "]"
}

// capAuditBody 按上限截断审计保存的原始请求体；截断时追加标记，
// 前端 JSON 高亮失败时回退展示原文并能看到"已截断"提示。
func capAuditBody(body string) string {
	runes := []rune(body)
	if len(runes) <= dataAPIAuditBodyMax {
		return body
	}
	return string(runes[:dataAPIAuditBodyMax]) + "…(truncated)"
}
