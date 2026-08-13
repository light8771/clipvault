package clipvault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"clipboard_ext/internal/clipvault/transfer"
)

// ExportData is the complete portable export structure.
type ExportData struct {
	ExportVersion int             `json:"exportVersion"`
	App           string          `json:"app"`
	ExportedAt    string          `json:"exportedAt"`
	SourceEngine  string          `json:"sourceEngine"`
	Warning       string          `json:"warning"`
	Items         []StoredItem    `json:"items"`
	Groups        []StoredGroup   `json:"groups"`
	ItemGroups    []ItemGroupLink `json:"itemGroups"`
	Settings      ExportSettings  `json:"settings"`
}

// ExportSettings contains the portable settings required to restore the
// exported data's behavior. Credentials and machine-local settings stay out
// of data exports.
type ExportSettings struct {
	Sensitive SensitiveConfig `json:"sensitive"`
}

// UnmarshalJSON requires every portable settings field in the v3 contract.
// A missing sensitive rule set must not silently clear the destination rules.
func (settings *ExportSettings) UnmarshalJSON(data []byte) error {
	type exportSettings ExportSettings
	var decoded exportSettings
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	rawSensitive, ok := fields["sensitive"]
	if !ok || string(rawSensitive) == "null" {
		return errors.New("settings.sensitive is required")
	}
	var sensitiveFields map[string]json.RawMessage
	if err := json.Unmarshal(rawSensitive, &sensitiveFields); err != nil {
		return fmt.Errorf("settings.sensitive: %w", err)
	}
	rawPatterns, ok := sensitiveFields["customPatterns"]
	if !ok || string(rawPatterns) == "null" {
		return errors.New("settings.sensitive.customPatterns is required")
	}
	*settings = ExportSettings(decoded)
	return nil
}

// ItemGroupLink is a single item-group association for export/import.
type ItemGroupLink struct {
	ItemID  int64 `json:"itemId"`
	GroupID int64 `json:"groupId"`
}

// ExportScope describes the data range for export and backup operations.
// It is persisted in backup config (DirBackupConfig/EmailBackupConfig.Scope)
// but is not part of the exported document.
type ExportScope = transfer.Scope

// validateExportScope 校验已归一化的导出范围。range 使用 RFC3339
// [From,To) 边界；UI 的本地日期由 normalizeExportRange 先转换。
func validateExportScope(scope ExportScope) error {
	return transfer.ValidateScope(scope)
}

// ExportFullForJSON exports clipboard data, group hierarchy, and portable
// sensitive-data rules.
func (s *Store) ExportFullForJSON(ctx context.Context) (*ExportData, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin export transaction: %w", err)
	}
	defer tx.Rollback()

	items, err := exportAll(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("export items: %w", err)
	}

	groups, err := listGroups(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("export groups: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT item_id, group_id FROM item_groups ORDER BY item_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("export item_groups: %w", err)
	}
	var links []ItemGroupLink
	for rows.Next() {
		var l ItemGroupLink
		if err := rows.Scan(&l.ItemID, &l.GroupID); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close item_groups export: %w", err)
	}
	cfg, err := getConfig(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("export settings: %w", err)
	}
	if items == nil {
		items = []StoredItem{}
	}
	if groups == nil {
		groups = []StoredGroup{}
	}
	if links == nil {
		links = []ItemGroupLink{}
	}

	export := &ExportData{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "This export contains plaintext clipboard content.",
		Items:         items,
		Groups:        groups,
		ItemGroups:    links,
		Settings: ExportSettings{Sensitive: SensitiveConfig{
			CustomPatterns: append([]string{}, cfg.Sensitive.CustomPatterns...),
		}},
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit export transaction: %w", err)
	}
	return export, nil
}

// StreamExport writes an export JSON document for the given scope to w.
// It returns the id of the LAST item included (0 when no items matched) so the
// caller can advance a backup watermark from the exact snapshot that was
// exported — querying the max id afterwards would race with concurrent writes.
func (s *Store) StreamExport(ctx context.Context, scope ExportScope, w io.Writer) (int64, error) {
	if err := validateExportScope(scope); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin export tx: %w", err)
	}
	defer tx.Rollback()

	header := struct {
		ExportVersion int    `json:"exportVersion"`
		App           string `json:"app"`
		ExportedAt    string `json:"exportedAt"`
		SourceEngine  string `json:"sourceEngine"`
		Warning       string `json:"warning"`
	}{
		ExportVersion: currentExportVersion,
		App:           "ClipVault",
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		SourceEngine:  "duckdb",
		Warning:       "This export contains plaintext clipboard content.",
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return 0, fmt.Errorf("marshal header: %w", err)
	}

	if _, err := w.Write(hb[:len(hb)-1]); err != nil {
		return 0, fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write([]byte(`,"items":[`)); err != nil {
		return 0, fmt.Errorf("write items header: %w", err)
	}

	itemSQL, itemArgs := exportItemsQuery(scope)
	rows, err := tx.QueryContext(ctx, itemSQL, itemArgs...)
	if err != nil {
		return 0, fmt.Errorf("query items: %w", err)
	}
	first := true
	var lastID int64
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan item: %w", err)
		}
		if !first {
			if _, err := w.Write([]byte(",")); err != nil {
				rows.Close()
				return 0, fmt.Errorf("write item separator: %w", err)
			}
		}
		b, err := json.Marshal(item)
		if err != nil {
			rows.Close()
			return 0, fmt.Errorf("marshal item: %w", err)
		}
		if _, err := w.Write(b); err != nil {
			rows.Close()
			return 0, fmt.Errorf("write item: %w", err)
		}
		first = false
		lastID = item.ID
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close items: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate items: %w", err)
	}

	groups, err := listGroups(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("export groups: %w", err)
	}
	if groups == nil {
		groups = []StoredGroup{}
	}
	gb, err := json.Marshal(groups)
	if err != nil {
		return 0, fmt.Errorf("marshal groups: %w", err)
	}
	if _, err := w.Write([]byte(`],"groups":`)); err != nil {
		return 0, fmt.Errorf("write groups header: %w", err)
	}
	if _, err := w.Write(gb); err != nil {
		return 0, fmt.Errorf("write groups: %w", err)
	}

	linkSQL, linkArgs := exportLinksQuery(scope)
	if _, err := w.Write([]byte(`,"itemGroups":[`)); err != nil {
		return 0, fmt.Errorf("write itemGroups header: %w", err)
	}
	lrows, err := tx.QueryContext(ctx, linkSQL, linkArgs...)
	if err != nil {
		return 0, fmt.Errorf("query item_groups: %w", err)
	}
	lfirst := true
	for lrows.Next() {
		var l ItemGroupLink
		if err := lrows.Scan(&l.ItemID, &l.GroupID); err != nil {
			lrows.Close()
			return 0, fmt.Errorf("scan link: %w", err)
		}
		if !lfirst {
			if _, err := w.Write([]byte(",")); err != nil {
				lrows.Close()
				return 0, fmt.Errorf("write link separator: %w", err)
			}
		}
		b, err := json.Marshal(l)
		if err != nil {
			lrows.Close()
			return 0, fmt.Errorf("marshal link: %w", err)
		}
		if _, err := w.Write(b); err != nil {
			lrows.Close()
			return 0, fmt.Errorf("write link: %w", err)
		}
		lfirst = false
	}
	if err := lrows.Close(); err != nil {
		return 0, fmt.Errorf("close item_groups: %w", err)
	}
	if err := lrows.Err(); err != nil {
		return 0, fmt.Errorf("iterate item_groups: %w", err)
	}

	cfg, err := getConfig(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("export settings: %w", err)
	}
	settings := ExportSettings{Sensitive: SensitiveConfig{
		CustomPatterns: append([]string{}, cfg.Sensitive.CustomPatterns...),
	}}
	sb, err := json.Marshal(settings)
	if err != nil {
		return 0, fmt.Errorf("marshal settings: %w", err)
	}
	if _, err := w.Write([]byte(`],"settings":`)); err != nil {
		return 0, fmt.Errorf("write settings header: %w", err)
	}
	if _, err := w.Write(sb); err != nil {
		return 0, fmt.Errorf("write settings: %w", err)
	}
	if _, err := w.Write([]byte("}")); err != nil {
		return 0, fmt.Errorf("write footer: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit export tx: %w", err)
	}
	return lastID, nil
}

// exportItemsQuery builds the clipboard_items SELECT for the given scope.
func exportItemsQuery(scope ExportScope) (string, []any) {
	base := `SELECT ` + clipboardItemColumns + ` FROM clipboard_items`
	where, args := exportScopeWhere(scope)
	return base + where + ` ORDER BY id ASC`, args
}

// exportLinksQuery builds the item_groups SELECT for the given scope. Links are
// filtered to the exported item range so a partial export stays self-consistent.
func exportLinksQuery(scope ExportScope) (string, []any) {
	const base = `SELECT item_id, group_id FROM item_groups`
	switch scope.Mode {
	case "incremental":
		sql := base + ` WHERE item_id > ?`
		args := []any{scope.SinceID}
		if scope.MaxID > 0 {
			sql += ` AND item_id <= ?`
			args = append(args, scope.MaxID)
		}
		return sql + ` ORDER BY item_id ASC`, args
	default:

		subWhere, args := exportScopeWhere(scope)
		return base + ` WHERE item_id IN (SELECT id FROM clipboard_items` + subWhere + `) ORDER BY item_id ASC`, args
	}
}

// exportScopeWhere builds the WHERE clause (without leading " WHERE") for
// clipboard_items, shared by count/boundary/export queries.
// 除自身过滤条件外，当 MaxID>0 时叠加 keyset 分片窗口 (id > SinceID AND id <= MaxID)，
// 供邮件分片导出按行数切分使用；非分片调用不传 MaxID，不受影响。
func exportScopeWhere(scope ExportScope) (string, []any) {
	var where string
	var args []any
	switch scope.Mode {
	case "range":
		where = ` WHERE created_at >= ? AND created_at < ?`
		args = []any{scope.From, scope.To}
	case "incremental":
		where = ` WHERE id > ?`
		args = []any{scope.SinceID}
	default:
		where = ""
	}
	if scope.MaxID > 0 {
		if scope.Mode == "incremental" {

			where += ` AND id <= ?`
			args = append(args, scope.MaxID)
		} else {
			if where == "" {
				where = ` WHERE id > ? AND id <= ?`
			} else {
				where += ` AND id > ? AND id <= ?`
			}
			args = append(args, scope.SinceID, scope.MaxID)
		}
	}
	return where, args
}

// CountInScope returns the number of items matching the scope. Used by
// chunked exports to size parts by actual row count (id holes are irrelevant).
func (s *Store) CountInScope(ctx context.Context, scope ExportScope) (int, error) {
	where, args := exportScopeWhere(scope)
	q := `SELECT COUNT(*) FROM clipboard_items` + where
	var n int
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// NthIDInScopeAfter returns a keyset boundary from the same filtered scope
// that will be exported. Range exports therefore cannot choose boundaries
// from unrelated rows outside their date interval.
func (s *Store) NthIDInScopeAfter(ctx context.Context, scope ExportScope, after int64, n int) (int64, error) {
	scope.MaxID = 0
	where, args := exportScopeWhere(scope)
	if where == "" {
		where = ` WHERE id > ?`
	} else {
		where += ` AND id > ?`
	}
	args = append(args, after, n)
	q := `SELECT id FROM clipboard_items` + where + ` ORDER BY id ASC LIMIT 1 OFFSET ?`
	var id int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// BackupWatermark records the last successful incremental backup position,
// stored in the settings table so incremental backups can resume from it.
type BackupWatermark struct {
	LastItemID       int64  `json:"lastItemId"`
	LastSentAt       string `json:"lastSentAt"`
	IncrementalCount int    `json:"incrementalCount"` // consecutive incremental runs; force full after threshold
	LastFullAt       string `json:"lastFullAt"`       // timestamp of the last full backup
}

// GetBackupWatermark reads the watermark for the given backup kind ("dir" or "email").
// Returns a zero value (no error) when no watermark exists yet.
func (s *Store) GetBackupWatermark(ctx context.Context, kind string) (BackupWatermark, error) {
	if err := validateBackupKind(kind); err != nil {
		return BackupWatermark{}, err
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT CAST(value AS VARCHAR) FROM settings WHERE key = ?`, "backup."+kind+".watermark").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return BackupWatermark{}, nil
	}
	if err != nil {
		return BackupWatermark{}, fmt.Errorf("read backup watermark: %w", err)
	}
	var wm BackupWatermark
	if err := decodeStrictJSON([]byte(raw), &wm); err != nil {
		return BackupWatermark{}, fmt.Errorf("parse backup watermark: %w", err)
	}
	if err := validateBackupWatermark(wm); err != nil {
		return BackupWatermark{}, fmt.Errorf("parse backup watermark: %w", err)
	}
	return wm, nil
}

// SetBackupWatermark persists the watermark for the given backup kind.
func (s *Store) SetBackupWatermark(ctx context.Context, kind string, wm BackupWatermark) error {
	if err := validateBackupKind(kind); err != nil {
		return err
	}
	if err := validateBackupWatermark(wm); err != nil {
		return err
	}
	data, err := json.Marshal(wm)
	if err != nil {
		return fmt.Errorf("marshal backup watermark: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		"backup."+kind+".watermark", string(data))
	return err
}

func validateBackupKind(kind string) error {
	if kind != "dir" && kind != "email" {
		return fmt.Errorf("invalid backup kind %q", kind)
	}
	return nil
}

func validateBackupWatermark(wm BackupWatermark) error {
	if wm.LastItemID < 0 || wm.IncrementalCount < 0 {
		return errors.New("backup watermark counters must not be negative")
	}
	for _, field := range []struct {
		name  string
		value string
	}{{"lastSentAt", wm.LastSentAt}, {"lastFullAt", wm.LastFullAt}} {
		if field.value == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, field.value); err != nil {
			return fmt.Errorf("backup watermark %s is invalid: %w", field.name, err)
		}
	}
	return nil
}
