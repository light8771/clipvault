package clipvault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"clipboard_ext/internal/clipvault/domain"
	"clipboard_ext/internal/clipvault/transfer"
)

// ImportFullJSONReader parses item payloads incrementally and keeps only IDs,
// groups, settings, and one database batch in memory. Large item bodies and
// links are spooled to owner-only temporary files before the atomic database
// transaction starts, so a slow upload never holds the single DB connection.
func (s *Store) ImportFullJSONReader(ctx context.Context, src io.Reader) error {
	staged, err := stageJSONImport(src)
	if err != nil {
		return err
	}
	defer staged.Close()
	return s.commitStagedJSONImport(ctx, staged)
}

type stagedJSONImport struct {
	items      *os.File
	links      *os.File
	itemTypes  map[int64]domain.HistoryRecordType
	imageItems map[int64]StoredItem
	assets     map[int64]stagedImageAsset
	groups     []StoredGroup
	groupIDs   map[int64]struct{}
	settings   ExportSettings
	linkCount  int
	itemCount  int
}

func (s *stagedJSONImport) Close() {
	if s.items != nil {
		name := s.items.Name()
		_ = s.items.Close()
		_ = os.Remove(name)
	}
	if s.links != nil {
		name := s.links.Name()
		_ = s.links.Close()
		_ = os.Remove(name)
	}
	for _, asset := range s.assets {
		_ = os.Remove(asset.fullPath)
	}
}

func stageJSONImport(src io.Reader) (_ *stagedJSONImport, resultErr error) {
	src = transfer.NewJSONReader(src)
	items, err := os.CreateTemp("", "clipvault-import-items-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("create item staging file: %w", err)
	}
	links, err := os.CreateTemp("", "clipvault-import-links-*.jsonl")
	if err != nil {
		_ = items.Close()
		_ = os.Remove(items.Name())
		return nil, fmt.Errorf("create link staging file: %w", err)
	}
	staged := &stagedJSONImport{
		items: items, links: links,
		itemTypes: make(map[int64]domain.HistoryRecordType), imageItems: make(map[int64]StoredItem),
		assets: make(map[int64]stagedImageAsset), groupIDs: make(map[int64]struct{}),
	}
	defer func() {
		if resultErr != nil {
			staged.Close()
		}
	}()

	dec := json.NewDecoder(src)
	dec.DisallowUnknownFields()
	token, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("parse export JSON: %w", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("parse export JSON: top-level object is required")
	}
	itemEncoder := json.NewEncoder(items)
	linkEncoder := json.NewEncoder(links)
	seenFields := make(map[string]struct{}, 10)
	var exportVersion int
	var app, exportedAt, sourceEngine, warning string
	settingsPresent := false
	var previousItemID int64
	for dec.More() {
		fieldToken, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("parse export JSON field: %w", err)
		}
		field, ok := fieldToken.(string)
		if !ok {
			return nil, errors.New("parse export JSON: invalid field name")
		}
		if _, duplicate := seenFields[field]; duplicate {
			return nil, fmt.Errorf("parse export JSON: duplicate field %q", field)
		}
		seenFields[field] = struct{}{}
		switch field {
		case "exportVersion":
			err = dec.Decode(&exportVersion)
		case "app":
			err = dec.Decode(&app)
		case "exportedAt":
			err = dec.Decode(&exportedAt)
		case "sourceEngine":
			err = dec.Decode(&sourceEngine)
		case "warning":
			err = dec.Decode(&warning)
		case "items":
			err = decodeJSONArray(dec, func() error {
				var item StoredItem
				if err := dec.Decode(&item); err != nil {
					return err
				}
				if err := validateImportedItem(item); err != nil {
					return err
				}
				if _, duplicate := staged.itemTypes[item.ID]; duplicate {
					return fmt.Errorf("duplicate item ID %d", item.ID)
				}
				if item.ID <= previousItemID {
					return errors.New("items must be ordered by ascending ID")
				}
				previousItemID = item.ID
				staged.itemTypes[item.ID] = item.RecordType
				if item.RecordType == domain.HistoryRecordContent && item.Format == domain.FormatImageFile {
					staged.imageItems[item.ID] = item
				}
				staged.itemCount++
				return itemEncoder.Encode(item)
			})
		case "assets":
			err = decodeJSONArray(dec, func() error {
				var asset ExportAsset
				if err := dec.Decode(&asset); err != nil {
					return err
				}
				if err := validateExportAssetData(asset); err != nil {
					return fmt.Errorf("asset for item %d: %w", asset.ItemID, err)
				}
				if _, duplicate := staged.assets[asset.ItemID]; duplicate {
					return fmt.Errorf("duplicate asset for item %d", asset.ItemID)
				}
				file, err := os.CreateTemp("", "clipvault-import-asset-*.png")
				if err != nil {
					return fmt.Errorf("create asset staging file: %w", err)
				}
				name := file.Name()
				if _, err := file.Write(asset.Data); err != nil {
					_ = file.Close()
					_ = os.Remove(name)
					return fmt.Errorf("write asset staging file: %w", err)
				}
				if err := file.Sync(); err != nil {
					_ = file.Close()
					_ = os.Remove(name)
					return fmt.Errorf("sync asset staging file: %w", err)
				}
				if err := file.Close(); err != nil {
					_ = os.Remove(name)
					return fmt.Errorf("close asset staging file: %w", err)
				}
				asset.Data = nil
				staged.assets[asset.ItemID] = stagedImageAsset{ExportAsset: asset, fullPath: name}
				return nil
			})
		case "groups":
			err = decodeJSONArray(dec, func() error {
				var group StoredGroup
				if err := dec.Decode(&group); err != nil {
					return err
				}
				if err := validateImportedGroup(group); err != nil {
					return err
				}
				if _, duplicate := staged.groupIDs[group.ID]; duplicate {
					return fmt.Errorf("duplicate group ID %d", group.ID)
				}
				staged.groupIDs[group.ID] = struct{}{}
				staged.groups = append(staged.groups, group)
				return nil
			})
		case "itemGroups":
			err = decodeJSONArray(dec, func() error {
				var link ItemGroupLink
				if err := dec.Decode(&link); err != nil {
					return err
				}
				staged.linkCount++
				return linkEncoder.Encode(link)
			})
		case "settings":
			settingsPresent = true
			err = dec.Decode(&staged.settings)
		default:
			return nil, fmt.Errorf("parse export JSON: unknown field %q", field)
		}
		if err != nil {
			return nil, fmt.Errorf("parse export JSON field %q: %w", field, err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("parse export JSON end: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return nil, err
	}
	if !settingsPresent {
		return nil, errors.New("settings is required")
	}
	for _, field := range []string{
		"exportVersion", "app", "exportedAt", "sourceEngine", "warning",
		"items", "assets", "groups", "itemGroups",
	} {
		if _, present := seenFields[field]; !present {
			return nil, fmt.Errorf("parse export JSON: required field %q is missing", field)
		}
	}
	if app != "ClipVault" {
		return nil, fmt.Errorf("not a ClipVault export (app=%q)", app)
	}
	if exportVersion != currentExportVersion {
		return nil, fmt.Errorf("unsupported export version %d", exportVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, exportedAt); err != nil {
		return nil, fmt.Errorf("invalid export timestamp %q", exportedAt)
	}
	if sourceEngine != "duckdb" {
		return nil, fmt.Errorf("unsupported source engine %q", sourceEngine)
	}
	if strings.TrimSpace(warning) == "" {
		return nil, errors.New("export warning is required")
	}
	if err := validateSensitiveRules(staged.settings.Sensitive); err != nil {
		return nil, fmt.Errorf("invalid sensitive rule: %w", err)
	}
	if err := validateStagedAssetSet(staged.imageItems, staged.assets); err != nil {
		return nil, err
	}
	for _, group := range staged.groups {
		if group.ParentID != 0 {
			if _, exists := staged.groupIDs[group.ParentID]; !exists {
				return nil, fmt.Errorf("group %d references missing parent %d", group.ID, group.ParentID)
			}
		}
	}
	if err := validateStagedLinks(staged); err != nil {
		return nil, err
	}
	if err := items.Sync(); err != nil {
		return nil, fmt.Errorf("sync item staging file: %w", err)
	}
	if err := links.Sync(); err != nil {
		return nil, fmt.Errorf("sync link staging file: %w", err)
	}
	return staged, nil
}

func decodeJSONArray(dec *json.Decoder, decodeElement func() error) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("array is required; null is not allowed")
	}
	if delim, ok := token.(json.Delim); !ok || delim != '[' {
		return errors.New("array is required")
	}
	for dec.More() {
		if err := decodeElement(); err != nil {
			return err
		}
	}
	token, err = dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); !ok || delim != ']' {
		return errors.New("invalid array terminator")
	}
	return nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("parse export JSON trailing data: %w", err)
	}
	return errors.New("parse export JSON: trailing value")
}

func validateImportedItem(item StoredItem) error {
	return validateStoredHistoryItem(item)
}

func validateImportedGroup(group StoredGroup) error {
	if group.ID <= 0 || group.ParentID < 0 || group.CreatedAt.IsZero() {
		return fmt.Errorf("invalid group %d", group.ID)
	}
	if strings.TrimSpace(group.Name) != group.Name {
		return fmt.Errorf("invalid group %d: name has surrounding whitespace", group.ID)
	}
	if err := validateGroupName(group.Name); err != nil {
		return fmt.Errorf("invalid group %d: %w", group.ID, err)
	}
	return nil
}

func validateStagedLinks(staged *stagedJSONImport) error {
	if _, err := staged.links.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind link staging file: %w", err)
	}
	dec := json.NewDecoder(staged.links)
	for {
		var link ItemGroupLink
		if err := dec.Decode(&link); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return fmt.Errorf("read staged item-group link: %w", err)
		}
		recordType, exists := staged.itemTypes[link.ItemID]
		if !exists {
			return fmt.Errorf("item-group link references missing item %d", link.ItemID)
		}
		if recordType != domain.HistoryRecordContent {
			return fmt.Errorf("item-group link references event item %d", link.ItemID)
		}
		if _, exists := staged.groupIDs[link.GroupID]; !exists {
			return fmt.Errorf("item-group link references missing group %d", link.GroupID)
		}
	}
	return nil
}

func (s *Store) commitStagedJSONImport(ctx context.Context, staged *stagedJSONImport) error {
	prepared, err := s.prepareStagedAssets(staged.imageItems, staged.assets)
	if err != nil {
		return fmt.Errorf("prepare managed image assets: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = prepared.cleanup()
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	settings, err := importedSettingsJSON(ctx, tx, staged.settings)
	if err != nil {
		return err
	}
	groupIDMap, err := importGroupsTx(ctx, tx, staged.groups)
	if err != nil {
		return err
	}
	itemIDMap, err := importStagedItemsTx(ctx, tx, staged, prepared.storedPaths)
	if err != nil {
		return err
	}
	if err := importStagedLinksTx(ctx, tx, staged, itemIDMap, groupIDMap); err != nil {
		return err
	}
	if err := recomputeAllConsecutiveDuplicatesTx(ctx, tx); err != nil {
		return fmt.Errorf("recompute consecutive duplicates: %w", err)
	}
	for _, key := range []string{"config", "config.last_good"} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, string(settings)); err != nil {
			return fmt.Errorf("import settings: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func importedSettingsJSON(ctx context.Context, tx *sql.Tx, imported ExportSettings) ([]byte, error) {
	cfg, err := getConfig(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	cfg.Sensitive = imported.Sensitive
	normalizeConfig(&cfg)
	settings, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	return settings, nil
}

func importGroupsTx(ctx context.Context, tx *sql.Tx, groups []StoredGroup) (map[int64]int64, error) {
	groupIDMap := make(map[int64]int64, len(groups))
	pending := append([]StoredGroup(nil), groups...)
	for len(pending) > 0 {
		next := pending[:0]
		progressed := false
		for _, group := range pending {
			parentID, ready := groupIDMap[group.ParentID]
			if group.ParentID == 0 {
				ready = true
			}
			if !ready {
				next = append(next, group)
				continue
			}
			var existingID int64
			lookupErr := tx.QueryRowContext(ctx,
				`SELECT id FROM groups WHERE name = ? AND parent_id = ? ORDER BY id ASC LIMIT 1`,
				group.Name, parentID).Scan(&existingID)
			if lookupErr == nil {
				groupIDMap[group.ID] = existingID
				progressed = true
				continue
			}
			if !errors.Is(lookupErr, sql.ErrNoRows) {
				return nil, fmt.Errorf("lookup group %q: %w", group.Name, lookupErr)
			}
			var newID int64
			if err := tx.QueryRowContext(ctx,
				`INSERT INTO groups (name, parent_id, created_at) VALUES (?, ?, ?) RETURNING id`,
				group.Name, parentID, group.CreatedAt.UTC().UnixMilli()).Scan(&newID); err != nil {
				return nil, fmt.Errorf("import group %d: %w", group.ID, err)
			}
			groupIDMap[group.ID] = newID
			progressed = true
		}
		if !progressed {
			return nil, errors.New("import groups: cyclic parent hierarchy")
		}
		pending = next
	}
	return groupIDMap, nil
}

func importStagedItemsTx(ctx context.Context, tx *sql.Tx, staged *stagedJSONImport, storedPaths map[int64]string) (map[int64]int64, error) {
	if _, err := staged.items.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind item staging file: %w", err)
	}
	dec := json.NewDecoder(staged.items)
	itemIDMap := make(map[int64]int64, staged.itemCount)
	const batchSize = 500
	batch := make([]StoredItem, 0, batchSize)
	offset := 0
	for {
		var item StoredItem
		err := dec.Decode(&item)
		if errors.Is(err, io.EOF) {
			if len(batch) > 0 {
				if err := insertItemBatchTx(ctx, tx, batch, itemIDMap, offset); err != nil {
					return nil, err
				}
			}
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read staged item: %w", err)
		}
		item, err = rewriteImportedImageItem(item, storedPaths)
		if err != nil {
			return nil, err
		}
		batch = append(batch, item)
		if len(batch) == batchSize {
			if err := insertItemBatchTx(ctx, tx, batch, itemIDMap, offset); err != nil {
				return nil, err
			}
			offset += len(batch)
			batch = batch[:0]
		}
	}
	return itemIDMap, nil
}

func insertItemBatchTx(ctx context.Context, tx *sql.Tx, batch []StoredItem, idMap map[int64]int64, offset int) error {
	var beforeMax int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM clipboard_items`).Scan(&beforeMax); err != nil {
		return fmt.Errorf("query max id before batch: %w", err)
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO clipboard_items (created_at, record_type, kind, format, text, byte_len, char_len, content_sha256, consecutive_duplicate, meta_json, starred, sensitive, event_code, event_fingerprint, capture_sequence, capture_stage, capture_attempts, event_formats, error_category, source_exe_name, source_exe_path, source_window_title, source_attribution, content_json, content_types) VALUES `)
	args := make([]any, 0, len(batch)*25)
	for index, item := range batch {
		if index > 0 {
			query.WriteByte(',')
		}
		query.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
		contentTypes, err := historyContentTypesJSON(item.Format, item.Text, item.Content)
		if err != nil {
			return err
		}
		eventFormats := encodeClipboardFormats(item.EventFormats)
		var eventCode, eventFingerprint any
		if item.RecordType == domain.HistoryRecordEvent {
			eventCode, eventFingerprint = item.EventCode, item.EventFingerprint
		}
		attribution := item.SourceAttribution
		if attribution == "" {
			attribution = "unknown"
		}
		args = append(args, item.CreatedAt.UTC().UnixMilli(), item.RecordType, item.Kind, item.Format, item.Text,
			item.ByteLen, item.CharLen, item.ContentSHA256, false, item.MetaJSON, item.Starred, item.Sensitive,
			eventCode, eventFingerprint, item.CaptureSequence, item.CaptureStage, item.CaptureAttempts,
			eventFormats, item.ErrorCategory,
			item.SourceExeName, item.SourceExePath, item.SourceWindowTitle, attribution, compositeJSONValue(item.Content), contentTypes)
	}
	if _, err := tx.ExecContext(ctx, query.String(), args...); err != nil {
		return fmt.Errorf("import items batch at offset %d: %w", offset, err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM clipboard_items WHERE id > ? ORDER BY id ASC`, beforeMax)
	if err != nil {
		return fmt.Errorf("query imported ids: %w", err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var newID int64
		if err := rows.Scan(&newID); err != nil {
			return fmt.Errorf("scan imported id: %w", err)
		}
		if index < len(batch) {
			idMap[batch[index].ID] = newID
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate imported ids: %w", err)
	}
	if index != len(batch) {
		return fmt.Errorf("imported %d ids for batch of %d items", index, len(batch))
	}
	return nil
}

func importStagedLinksTx(ctx context.Context, tx *sql.Tx, staged *stagedJSONImport, itemIDs, groupIDs map[int64]int64) error {
	if _, err := staged.links.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind link staging file: %w", err)
	}
	const sourceLinksTable = "clipvault_import_source_links"
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+sourceLinksTable); err != nil {
		return fmt.Errorf("reset import link validation table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE `+sourceLinksTable+` (
		source_item_id BIGINT NOT NULL,
		source_group_id BIGINT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create import link validation table: %w", err)
	}
	dec := json.NewDecoder(staged.links)
	const batchSize = 500
	batch := make([]ItemGroupLink, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		var sourceQuery strings.Builder
		sourceQuery.WriteString(`INSERT INTO ` + sourceLinksTable + ` (source_item_id, source_group_id) VALUES `)
		sourceArgs := make([]any, 0, len(batch)*2)
		for index, link := range batch {
			if index > 0 {
				sourceQuery.WriteByte(',')
			}
			sourceQuery.WriteString("(?,?)")
			sourceArgs = append(sourceArgs, link.ItemID, link.GroupID)
		}
		if _, err := tx.ExecContext(ctx, sourceQuery.String(), sourceArgs...); err != nil {
			return fmt.Errorf("validate item_groups uniqueness: %w", err)
		}

		var mappedQuery strings.Builder
		mappedQuery.WriteString(`INSERT INTO item_groups (item_id, group_id) VALUES `)
		mappedArgs := make([]any, 0, len(batch)*2)
		for index, source := range batch {
			if index > 0 {
				mappedQuery.WriteByte(',')
			}
			mappedQuery.WriteString("(?,?)")
			mappedArgs = append(mappedArgs, itemIDs[source.ItemID], groupIDs[source.GroupID])
		}
		mappedQuery.WriteString(` ON CONFLICT DO NOTHING`)
		if _, err := tx.ExecContext(ctx, mappedQuery.String(), mappedArgs...); err != nil {
			return fmt.Errorf("import item_groups: %w", err)
		}
		batch = batch[:0]
		return nil
	}
	for {
		var source ItemGroupLink
		err := dec.Decode(&source)
		if errors.Is(err, io.EOF) {
			if err := flush(); err != nil {
				return err
			}
			var duplicateItemID, duplicateGroupID int64
			err := tx.QueryRowContext(ctx, `
				SELECT source_item_id, source_group_id
				FROM `+sourceLinksTable+`
				GROUP BY source_item_id, source_group_id
				HAVING COUNT(*) > 1
				LIMIT 1
			`).Scan(&duplicateItemID, &duplicateGroupID)
			switch {
			case err == nil:
				return fmt.Errorf("duplicate item-group link %d:%d", duplicateItemID, duplicateGroupID)
			case !errors.Is(err, sql.ErrNoRows):
				return fmt.Errorf("check duplicate item-group links: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `DROP TABLE `+sourceLinksTable); err != nil {
				return fmt.Errorf("drop import link validation table: %w", err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("read staged item-group link: %w", err)
		}
		batch = append(batch, source)
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}
