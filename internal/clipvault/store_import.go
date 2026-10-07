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

// importExportData imports a validated complete export in one transaction.
// JSON and XLSX both use this path so their restore behavior cannot diverge.
func (s *Store) importExportData(ctx context.Context, export ExportData) error {
	if err := validateExportData(export); err != nil {
		return err
	}
	prepared, err := s.prepareExportAssets(export.Items, export.Assets)
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
		sb.WriteString(`INSERT INTO clipboard_items (created_at, record_type, kind, format, text, byte_len, char_len, content_sha256, consecutive_duplicate, meta_json, starred, sensitive, event_code, event_fingerprint, capture_sequence, capture_stage, capture_attempts, event_formats, error_category, source_exe_name, source_exe_path, source_window_title, source_attribution, content_json, content_types) VALUES `)
		args := make([]any, 0, len(batch)*25)
		for j, item := range batch {
			item, err = rewriteImportedImageItem(item, prepared.storedPaths)
			if err != nil {
				return err
			}
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
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
			args = append(args,
				item.CreatedAt.UTC().UnixMilli(),
				item.RecordType, item.Kind, item.Format, item.Text,
				item.ByteLen, item.CharLen, item.ContentSHA256, false, item.MetaJSON,
				item.Starred, item.Sensitive, eventCode, eventFingerprint,
				item.CaptureSequence, item.CaptureStage, item.CaptureAttempts, eventFormats, item.ErrorCategory,
				item.SourceExeName, item.SourceExePath, item.SourceWindowTitle, attribution, compositeJSONValue(item.Content), contentTypes,
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

func validateExportData(export ExportData) error {
	if err := validateSensitiveRules(export.Settings.Sensitive); err != nil {
		return fmt.Errorf("invalid sensitive rule: %w", err)
	}
	itemTypes := make(map[int64]domain.HistoryRecordType, len(export.Items))
	var previousID int64
	for _, item := range export.Items {
		if err := validateStoredHistoryItem(item); err != nil {
			return err
		}
		if _, exists := itemTypes[item.ID]; exists {
			return fmt.Errorf("duplicate item ID %d", item.ID)
		}
		if item.ID <= previousID {
			return errors.New("items must be ordered by ascending ID")
		}
		previousID = item.ID
		itemTypes[item.ID] = item.RecordType
	}
	if err := validateExportAssetSet(export.Items, export.Assets); err != nil {
		return err
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
		recordType, exists := itemTypes[link.ItemID]
		if !exists {
			return fmt.Errorf("item-group link references missing item %d", link.ItemID)
		}
		if recordType != domain.HistoryRecordContent {
			return fmt.Errorf("item-group link references event item %d", link.ItemID)
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

func validateStoredHistoryItem(item StoredItem) error {
	if item.ID <= 0 || item.CreatedAt.IsZero() || item.ByteLen < 0 || item.CharLen < 0 || !json.Valid([]byte(item.MetaJSON)) {
		return fmt.Errorf("invalid item %d", item.ID)
	}
	if !validSourceAttribution(item.SourceAttribution) {
		return fmt.Errorf("invalid item %d: invalid source attribution", item.ID)
	}
	switch item.RecordType {
	case domain.HistoryRecordContent:
		validHash := item.ContentSHA256 != nil && validContentSHA256(*item.ContentSHA256)
		validLengths := item.ByteLen == len([]byte(item.Text)) && item.CharLen == textRuneLen(item.Text)
		if item.Format == domain.FormatComposite {
			text, size, err := validateComposite(item.Content)
			validLengths = err == nil && text == item.Text && size == item.ByteLen && textRuneLen(text) == item.CharLen
			validHash = validHash && err == nil && compositeFingerprint(item.Content) == *item.ContentSHA256
		} else if item.Content != nil {
			return fmt.Errorf("unexpected composite package on item %d", item.ID)
		} else if item.Format == domain.FormatImageFile {
			validLengths = item.ByteLen > 0 && item.CharLen == 0 && validateManagedImageStoredPath(item.Text) == nil
		} else if validHash {
			validHash = *item.ContentSHA256 == hashString(item.Text)
		}
		if !validHash || strings.IndexByte(item.Text, 0) >= 0 ||
			!validLengths ||
			!domain.ValidContentKindFormat(item.Kind, item.Format) || item.EventCode != "" ||
			item.EventFingerprint != "" || item.CaptureSequence != 0 || item.CaptureStage != "" ||
			item.CaptureAttempts != 0 || len(item.EventFormats) != 0 || item.ErrorCategory != "" ||
			item.OccurrenceCount != 0 || !item.FirstOccurredAt.IsZero() || !item.LastOccurredAt.IsZero() {
			return fmt.Errorf("invalid item %d: inconsistent content record", item.ID)
		}
	case domain.HistoryRecordEvent:
		if item.Content != nil {
			return errors.New("event cannot contain a composite package")
		}
		expectedFingerprint := buildEventFingerprintFromInfo(item.EventCode, item.EventFormats,
			item.CaptureStage, item.ErrorCategory, item.SourceExeName)
		if item.ContentSHA256 != nil || item.ConsecutiveDuplicate || item.Text != "" || item.Kind != "" || item.Format != "" ||
			item.ByteLen != 0 || item.CharLen != 0 || item.Starred || item.Sensitive ||
			!domain.ValidEventCode(item.EventCode) || item.EventFingerprint != expectedFingerprint || item.CaptureAttempts < 0 ||
			!validImportedEventOccurrence(item) {
			return fmt.Errorf("invalid item %d: inconsistent event record", item.ID)
		}
	default:
		return fmt.Errorf("invalid item %d: invalid record type", item.ID)
	}
	return nil
}

// Event occurrence fields are a read-model projection. Raw XLSX rows omit
// them, while the shared JSON history DTO renders a raw event as one
// occurrence at CreatedAt. Neither representation is persisted during import;
// reject every other shape so grouped API results cannot masquerade as raw
// export rows.
func validImportedEventOccurrence(item StoredItem) bool {
	if item.OccurrenceCount == 0 {
		return item.FirstOccurredAt.IsZero() && item.LastOccurredAt.IsZero()
	}
	return item.OccurrenceCount == 1 &&
		item.FirstOccurredAt.Equal(item.CreatedAt) && item.LastOccurredAt.Equal(item.CreatedAt)
}

func validSourceAttribution(attribution string) bool {
	switch attribution {
	case "clipboard_owner", "foreground", "unknown":
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
