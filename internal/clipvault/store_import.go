package clipvault

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
)

// ImportFullJSON imports clipboard data, group hierarchy, and portable
// sensitive-data rules in one transaction.
func (s *Store) ImportFullJSON(ctx context.Context, data []byte) error {
	return s.ImportFullJSONReader(ctx, bytes.NewReader(data))
}

// decodeImportJSON parses a ClipVault JSON export into the format-agnostic
// ExportData carrier. It is the JSON half of the decoder/persister split and
// mirrors decodeImportXLSX: format- and envelope-level validation happens here
// (settings presence, app marker, export version), while data-semantic
// validation stays in importExportData so both import formats keep sharing one
// rule set.
func decodeImportJSON(data []byte) (ExportData, error) {
	var envelope struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ExportData{}, fmt.Errorf("parse export JSON: %w", err)
	}
	if len(envelope.Settings) == 0 || string(envelope.Settings) == "null" {
		return ExportData{}, errors.New("settings is required")
	}
	var export ExportData
	if err := decodeStrictJSON(data, &export); err != nil {
		return ExportData{}, fmt.Errorf("parse export JSON: %w", err)
	}
	if export.App != "ClipVault" {
		return ExportData{}, fmt.Errorf("not a ClipVault export (app=%q)", export.App)
	}
	if export.ExportVersion != currentExportVersion {
		return ExportData{}, fmt.Errorf("unsupported export version %d", export.ExportVersion)
	}
	return export, nil
}

// importExportData imports a validated complete export in one transaction.
// JSON and XLSX both use this path so their restore behavior cannot diverge.
func (s *Store) importExportData(ctx context.Context, export ExportData) error {
	if err := validateExportData(export); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	cfg, err := getConfig(ctx, tx)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	cfg.Sensitive = export.Settings.Sensitive
	normalizeConfig(&cfg)
	settings, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}

	groupIDMap := make(map[int64]int64)
	pendingGroups := append([]StoredGroup(nil), export.Groups...)
	for len(pendingGroups) > 0 {
		next := pendingGroups[:0]
		progressed := false
		for _, g := range pendingGroups {
			parentID, ready := groupIDMap[g.ParentID]
			if g.ParentID == 0 {
				ready = true
			}
			if !ready {
				next = append(next, g)
				continue
			}
			var existingID int64
			lookupErr := tx.QueryRowContext(ctx,
				`SELECT id FROM groups WHERE name = ? AND parent_id = ? ORDER BY id ASC LIMIT 1`,
				g.Name, parentID).Scan(&existingID)
			if lookupErr == nil {
				groupIDMap[g.ID] = existingID
				progressed = true
				continue
			}
			if !errors.Is(lookupErr, sql.ErrNoRows) {
				return fmt.Errorf("lookup group %q: %w", g.Name, lookupErr)
			}
			var newID int64
			err := tx.QueryRowContext(ctx,
				`INSERT INTO groups (name, parent_id, created_at) VALUES (?, ?, ?) RETURNING id`,
				g.Name, parentID, g.CreatedAt.UTC().UnixMilli(),
			).Scan(&newID)
			if err != nil {
				return fmt.Errorf("import group %d: %w", g.ID, err)
			}
			groupIDMap[g.ID] = newID
			progressed = true
		}
		if !progressed {
			return errors.New("import groups: cyclic parent hierarchy")
		}
		pendingGroups = next
	}

	// Import items with ID remapping, batched for throughput (multi-row INSERT).
	// DuckDB assigns sequential ids within a transaction, so after each batch we
	// read back the new ids greater than the previous max to rebuild oldID->newID.
	const importBatchSize = 500
	itemIDMap := make(map[int64]int64)
	for i := 0; i < len(export.Items); i += importBatchSize {
		end := i + importBatchSize
		if end > len(export.Items) {
			end = len(export.Items)
		}
		batch := export.Items[i:end]

		var beforeMax int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM clipboard_items`).Scan(&beforeMax); err != nil {
			return fmt.Errorf("query max id before batch: %w", err)
		}

		var sb strings.Builder
		sb.WriteString(`INSERT INTO clipboard_items (created_at, kind, format, text, byte_len, char_len, sha256, meta_json, starred, sensitive, content_available, is_duplicate, unavailable_reason, source_exe_name, source_exe_path, source_window_title, source_method) VALUES `)
		args := make([]any, 0, len(batch)*17)
		for j, item := range batch {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
			args = append(args,
				item.CreatedAt.UTC().UnixMilli(),
				item.Kind, item.Format, item.Text,
				item.ByteLen, item.CharLen, item.SHA256, item.MetaJSON,
				item.Starred, item.Sensitive,
				item.ContentAvailable, item.IsDuplicate, item.UnavailableReason,
				item.SourceExeName, item.SourceExePath, item.SourceWindowTitle, item.SourceMethod,
			)
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("import items batch at offset %d: %w", i, err)
		}

		rows, err := tx.QueryContext(ctx, `SELECT id FROM clipboard_items WHERE id > ? ORDER BY id ASC`, beforeMax)
		if err != nil {
			return fmt.Errorf("query imported ids: %w", err)
		}
		k := 0
		for rows.Next() {
			var newID int64
			if err := rows.Scan(&newID); err != nil {
				rows.Close()
				return fmt.Errorf("scan imported id: %w", err)
			}
			if k < len(batch) {
				itemIDMap[batch[k].ID] = newID
			}
			k++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate imported ids: %w", err)
		}
		if k != len(batch) {
			return fmt.Errorf("imported %d ids for batch of %d items", k, len(batch))
		}
	}

	// Import item_groups with remapped IDs (batched for throughput). Links are
	// deduplicated after remapping: merged groups can collapse multiple source
	// links onto the same (item_id, group_id) primary key.
	const linkBatchSize = 500
	seenLinks := make(map[ItemGroupLink]struct{}, len(export.ItemGroups))
	for i := 0; i < len(export.ItemGroups); i += linkBatchSize {
		end := i + linkBatchSize
		if end > len(export.ItemGroups) {
			end = len(export.ItemGroups)
		}
		batch := export.ItemGroups[i:end]
		var lb strings.Builder
		lb.WriteString(`INSERT INTO item_groups (item_id, group_id) VALUES `)
		largs := make([]any, 0, len(batch)*2)
		wrote := 0
		for _, link := range batch {
			mapped := ItemGroupLink{ItemID: itemIDMap[link.ItemID], GroupID: groupIDMap[link.GroupID]}
			if _, dup := seenLinks[mapped]; dup {
				continue
			}
			seenLinks[mapped] = struct{}{}
			if wrote > 0 {
				lb.WriteString(",")
			}
			lb.WriteString("(?,?)")
			largs = append(largs, mapped.ItemID, mapped.GroupID)
			wrote++
		}
		if wrote == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, lb.String(), largs...); err != nil {
			return fmt.Errorf("import item_groups batch at offset %d: %w", i, err)
		}
	}
	for _, key := range []string{"config", "config.last_good"} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(settings)); err != nil {
			return fmt.Errorf("import settings: %w", err)
		}
	}

	return tx.Commit()
}

func validateExportData(export ExportData) error {
	if err := validateSensitiveRules(export.Settings.Sensitive); err != nil {
		return fmt.Errorf("invalid sensitive rule: %w", err)
	}
	itemIDs := make(map[int64]struct{}, len(export.Items))
	for _, item := range export.Items {
		if item.ID <= 0 || item.CreatedAt.IsZero() || item.ByteLen < 0 || item.CharLen < 0 || !json.Valid([]byte(item.MetaJSON)) {
			return fmt.Errorf("invalid item %d", item.ID)
		}
		if !validItemKindFormat(item.Kind, item.Format) {
			return fmt.Errorf("invalid item %d: unsupported kind/format %q/%q", item.ID, item.Kind, item.Format)
		}
		if item.ByteLen != len([]byte(item.Text)) || item.CharLen != textRuneLen(item.Text) || item.SHA256 != hashString(item.Text) {
			return fmt.Errorf("invalid item %d: content metadata mismatch", item.ID)
		}
		if !validUnavailableState(item.ContentAvailable, item.UnavailableReason) {
			return fmt.Errorf("invalid item %d: inconsistent content availability state", item.ID)
		}
		if _, exists := itemIDs[item.ID]; exists {
			return fmt.Errorf("duplicate item ID %d", item.ID)
		}
		itemIDs[item.ID] = struct{}{}
	}
	groupIDs := make(map[int64]struct{}, len(export.Groups))
	for _, group := range export.Groups {
		if group.ID <= 0 || group.ParentID < 0 || group.CreatedAt.IsZero() {
			return fmt.Errorf("invalid group %d", group.ID)
		}
		if strings.TrimSpace(group.Name) != group.Name {
			return fmt.Errorf("invalid group %d: name has surrounding whitespace", group.ID)
		}
		if err := validateGroupName(group.Name); err != nil {
			return fmt.Errorf("invalid group %d: %w", group.ID, err)
		}
		if _, exists := groupIDs[group.ID]; exists {
			return fmt.Errorf("duplicate group ID %d", group.ID)
		}
		groupIDs[group.ID] = struct{}{}
	}
	for _, group := range export.Groups {
		if group.ParentID != 0 {
			if _, exists := groupIDs[group.ParentID]; !exists {
				return fmt.Errorf("group %d references missing parent %d", group.ID, group.ParentID)
			}
		}
	}
	links := make(map[ItemGroupLink]struct{}, len(export.ItemGroups))
	for _, link := range export.ItemGroups {
		if _, exists := itemIDs[link.ItemID]; !exists {
			return fmt.Errorf("item-group link references missing item %d", link.ItemID)
		}
		if _, exists := groupIDs[link.GroupID]; !exists {
			return fmt.Errorf("item-group link references missing group %d", link.GroupID)
		}
		if _, exists := links[link]; exists {
			return fmt.Errorf("duplicate item-group link %d:%d", link.ItemID, link.GroupID)
		}
		links[link] = struct{}{}
	}
	return nil
}

// validItemKindFormat 委托 domain 的唯一合法性矩阵（采集/导入/导出共用）。
func validItemKindFormat(kind, format string) bool {
	return domain.ValidItemKindFormat(kind, format)
}

// validUnavailableState 校验 content_available 与 unavailable_reason 的一致性：
// 内容可用时 reason 必须为空；不可用时必须给出已知原因。
func validUnavailableState(contentAvailable bool, reason string) bool {
	if contentAvailable {
		return reason == domain.ReasonNone
	}
	switch reason {
	case domain.ReasonSuperseded, domain.ReasonUnsupportedFormat,
		domain.ReasonTextLimitExceeded, domain.ReasonImageLimitExceeded, domain.ReasonReadFailed:
		return true
	default:
		return false
	}
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
