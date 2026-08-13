package clipvault

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"time"
	"unicode/utf16"

	"github.com/xuri/excelize/v2"
)

const (
	xlsxManifestSheet       = "Manifest"
	xlsxRecordsSheet        = "Records"
	xlsxGroupsSheet         = "Groups"
	xlsxItemGroupsSheet     = "ItemGroups"
	xlsxSensitiveRulesSheet = "SensitiveRules"
	xlsxCellTextLimit       = 32767
	xlsxUnzipSizeLimit      = 256 << 20
	xlsxUnzipXMLSizeLimit   = 32 << 20
)

var (
	xlsxManifestHeader       = []string{"app", "exportVersion"}
	xlsxRecordsHeader        = []string{"id", "createdAt", "kind", "format", "text", "byteLen", "charLen", "sha256", "metaJson", "starred", "sensitive", "sourceExeName", "sourceExePath", "sourceWindowTitle", "sourceMethod"}
	xlsxGroupsHeader         = []string{"id", "name", "parentId", "createdAt"}
	xlsxItemGroupsHeader     = []string{"itemId", "groupId"}
	xlsxSensitiveRulesHeader = []string{"pattern"}
)

// ExportXLSX produces a complete, portable spreadsheet export. All five
// sheets are required by ImportXLSX and map exactly to ExportData.
func (s *Store) ExportXLSX(ctx context.Context) ([]byte, error) {
	return s.ExportXLSXScope(ctx, ExportScope{Mode: "full"})
}

// ExportXLSXScope exports the same full/range/incremental scope contract as
// JSON and cvexport. Materializing is required by excelize, but the source
// query and item-group filtering remain identical to StreamExport.
func (s *Store) ExportXLSXScope(ctx context.Context, scope ExportScope) ([]byte, error) {
	data, _, err := s.exportXLSXScope(ctx, scope)
	return data, err
}

func (s *Store) exportXLSXScope(ctx context.Context, scope ExportScope) ([]byte, int64, error) {
	var buf bytes.Buffer
	maxID, err := s.WriteXLSXScope(ctx, scope, &buf)
	return buf.Bytes(), maxID, err
}

// WriteXLSXScope writes a workbook without first materializing ExportData or
// the ZIP archive. Excelize stream writers spool worksheet XML as needed and
// book.Write emits the final archive directly to dst.
func (s *Store) WriteXLSXScope(ctx context.Context, scope ExportScope, dst io.Writer) (int64, error) {
	if err := validateExportScope(scope); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin XLSX export tx: %w", err)
	}
	defer tx.Rollback()
	book := excelize.NewFile()
	defer book.Close()
	if err := book.SetSheetName("Sheet1", xlsxRecordsSheet); err != nil {
		return 0, fmt.Errorf("create records sheet: %w", err)
	}
	for _, sheet := range []string{xlsxGroupsSheet, xlsxItemGroupsSheet, xlsxSensitiveRulesSheet, xlsxManifestSheet} {
		if _, err := book.NewSheet(sheet); err != nil {
			return 0, fmt.Errorf("create %s sheet: %w", sheet, err)
		}
	}
	manifest, err := book.NewStreamWriter(xlsxManifestSheet)
	if err != nil {
		return 0, fmt.Errorf("open manifest stream: %w", err)
	}
	if err := writeXLSXStreamRow(manifest, xlsxManifestSheet, 1, xlsxManifestHeader); err != nil {
		return 0, err
	}
	if err := writeXLSXStreamRow(manifest, xlsxManifestSheet, 2, []string{"ClipVault", strconv.Itoa(currentExportVersion)}); err != nil {
		return 0, err
	}
	if err := manifest.Flush(); err != nil {
		return 0, fmt.Errorf("flush manifest sheet: %w", err)
	}

	records, err := book.NewStreamWriter(xlsxRecordsSheet)
	if err != nil {
		return 0, fmt.Errorf("open records stream: %w", err)
	}
	if err := writeXLSXStreamRow(records, xlsxRecordsSheet, 1, xlsxRecordsHeader); err != nil {
		return 0, err
	}
	itemSQL, itemArgs := exportItemsQuery(scope)
	rows, err := tx.QueryContext(ctx, itemSQL, itemArgs...)
	if err != nil {
		return 0, fmt.Errorf("query XLSX records: %w", err)
	}
	rowNumber := 2
	var maxID int64
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan XLSX record: %w", err)
		}
		values := []string{
			strconv.FormatInt(item.ID, 10), item.CreatedAt.UTC().Format(time.RFC3339Nano), item.Kind, item.Format, item.Text,
			strconv.Itoa(item.ByteLen), strconv.Itoa(item.CharLen), item.SHA256, item.MetaJSON,
			strconv.FormatBool(item.Starred), strconv.FormatBool(item.Sensitive),
			item.SourceExeName, item.SourceExePath, item.SourceWindowTitle, item.SourceMethod,
		}
		if err := writeXLSXStreamRow(records, xlsxRecordsSheet, rowNumber, values); err != nil {
			rows.Close()
			return 0, err
		}
		maxID = item.ID
		rowNumber++
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close XLSX records: %w", err)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate XLSX records: %w", err)
	}
	if err := records.Flush(); err != nil {
		return 0, fmt.Errorf("flush records sheet: %w", err)
	}

	groups, err := book.NewStreamWriter(xlsxGroupsSheet)
	if err != nil {
		return 0, fmt.Errorf("open groups stream: %w", err)
	}
	if err := writeXLSXStreamRow(groups, xlsxGroupsSheet, 1, xlsxGroupsHeader); err != nil {
		return 0, err
	}
	groupRows, err := tx.QueryContext(ctx, `SELECT id, name, parent_id, created_at FROM groups ORDER BY id ASC`)
	if err != nil {
		return 0, fmt.Errorf("query XLSX groups: %w", err)
	}
	rowNumber = 2
	for groupRows.Next() {
		var id, parentID int64
		var name string
		var created time.Time
		if err := groupRows.Scan(&id, &name, &parentID, &created); err != nil {
			groupRows.Close()
			return 0, fmt.Errorf("scan XLSX group: %w", err)
		}
		if err := writeXLSXStreamRow(groups, xlsxGroupsSheet, rowNumber, []string{strconv.FormatInt(id, 10), name, strconv.FormatInt(parentID, 10), created.UTC().Format(time.RFC3339Nano)}); err != nil {
			groupRows.Close()
			return 0, err
		}
		rowNumber++
	}
	if err := groupRows.Close(); err != nil {
		return 0, fmt.Errorf("close XLSX groups: %w", err)
	}
	if err := groupRows.Err(); err != nil {
		return 0, fmt.Errorf("iterate XLSX groups: %w", err)
	}
	if err := groups.Flush(); err != nil {
		return 0, fmt.Errorf("flush groups sheet: %w", err)
	}

	links, err := book.NewStreamWriter(xlsxItemGroupsSheet)
	if err != nil {
		return 0, fmt.Errorf("open item-groups stream: %w", err)
	}
	if err := writeXLSXStreamRow(links, xlsxItemGroupsSheet, 1, xlsxItemGroupsHeader); err != nil {
		return 0, err
	}
	linkSQL, linkArgs := exportLinksQuery(scope)
	linkRows, err := tx.QueryContext(ctx, linkSQL, linkArgs...)
	if err != nil {
		return 0, fmt.Errorf("query XLSX item-groups: %w", err)
	}
	rowNumber = 2
	for linkRows.Next() {
		var itemID, groupID int64
		if err := linkRows.Scan(&itemID, &groupID); err != nil {
			linkRows.Close()
			return 0, fmt.Errorf("scan XLSX item-group: %w", err)
		}
		if err := writeXLSXStreamRow(links, xlsxItemGroupsSheet, rowNumber, []string{strconv.FormatInt(itemID, 10), strconv.FormatInt(groupID, 10)}); err != nil {
			linkRows.Close()
			return 0, err
		}
		rowNumber++
	}
	if err := linkRows.Close(); err != nil {
		return 0, fmt.Errorf("close XLSX item-groups: %w", err)
	}
	if err := linkRows.Err(); err != nil {
		return 0, fmt.Errorf("iterate XLSX item-groups: %w", err)
	}
	if err := links.Flush(); err != nil {
		return 0, fmt.Errorf("flush item-groups sheet: %w", err)
	}

	rules, err := book.NewStreamWriter(xlsxSensitiveRulesSheet)
	if err != nil {
		return 0, fmt.Errorf("open sensitive-rules stream: %w", err)
	}
	if err := writeXLSXStreamRow(rules, xlsxSensitiveRulesSheet, 1, xlsxSensitiveRulesHeader); err != nil {
		return 0, err
	}
	cfg, err := getConfig(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("export XLSX settings: %w", err)
	}
	for index, pattern := range cfg.Sensitive.CustomPatterns {
		if err := writeXLSXStreamRow(rules, xlsxSensitiveRulesSheet, index+2, []string{pattern}); err != nil {
			return 0, err
		}
	}
	if err := rules.Flush(); err != nil {
		return 0, fmt.Errorf("flush sensitive-rules sheet: %w", err)
	}
	if err := book.Write(dst); err != nil {
		return 0, fmt.Errorf("write XLSX: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit XLSX export tx: %w", err)
	}
	return maxID, nil
}

func writeXLSXStreamRow(stream *excelize.StreamWriter, sheet string, row int, values []string) error {
	rowValues := make([]any, len(values))
	for column, value := range values {
		if len(utf16.Encode([]rune(value))) > xlsxCellTextLimit {
			return fmt.Errorf("XLSX %s row %d column %d exceeds the %d-character cell limit", sheet, row, column+1, xlsxCellTextLimit)
		}
		rowValues[column] = value
	}
	cell, err := excelize.CoordinatesToCellName(1, row)
	if err != nil {
		return fmt.Errorf("XLSX %s row %d: %w", sheet, row, err)
	}
	if err := stream.SetRow(cell, rowValues); err != nil {
		return fmt.Errorf("XLSX %s row %d: %w", sheet, row, err)
	}
	return nil
}

func encodeExportXLSX(export *ExportData) ([]byte, error) {
	book := excelize.NewFile()
	defer book.Close()
	if err := book.SetSheetName("Sheet1", xlsxRecordsSheet); err != nil {
		return nil, fmt.Errorf("create records sheet: %w", err)
	}
	for _, sheet := range []string{xlsxGroupsSheet, xlsxItemGroupsSheet, xlsxSensitiveRulesSheet, xlsxManifestSheet} {
		if _, err := book.NewSheet(sheet); err != nil {
			return nil, fmt.Errorf("create %s sheet: %w", sheet, err)
		}
	}
	if err := writeXLSXRow(book, xlsxManifestSheet, 1, xlsxManifestHeader); err != nil {
		return nil, err
	}
	if err := writeXLSXRow(book, xlsxManifestSheet, 2, []string{"ClipVault", strconv.Itoa(currentExportVersion)}); err != nil {
		return nil, err
	}

	if err := writeXLSXRow(book, xlsxRecordsSheet, 1, xlsxRecordsHeader); err != nil {
		return nil, err
	}
	for index, item := range export.Items {
		row := []string{
			strconv.FormatInt(item.ID, 10), item.CreatedAt.UTC().Format(time.RFC3339Nano), item.Kind, item.Format, item.Text,
			strconv.Itoa(item.ByteLen), strconv.Itoa(item.CharLen), item.SHA256, item.MetaJSON,
			strconv.FormatBool(item.Starred), strconv.FormatBool(item.Sensitive),
			item.SourceExeName, item.SourceExePath, item.SourceWindowTitle, item.SourceMethod,
		}
		if err := writeXLSXRow(book, xlsxRecordsSheet, index+2, row); err != nil {
			return nil, err
		}
	}

	if err := writeXLSXRow(book, xlsxGroupsSheet, 1, xlsxGroupsHeader); err != nil {
		return nil, err
	}
	for index, group := range export.Groups {
		row := []string{strconv.FormatInt(group.ID, 10), group.Name, strconv.FormatInt(group.ParentID, 10), group.CreatedAt.UTC().Format(time.RFC3339Nano)}
		if err := writeXLSXRow(book, xlsxGroupsSheet, index+2, row); err != nil {
			return nil, err
		}
	}

	if err := writeXLSXRow(book, xlsxItemGroupsSheet, 1, xlsxItemGroupsHeader); err != nil {
		return nil, err
	}
	for index, link := range export.ItemGroups {
		if err := writeXLSXRow(book, xlsxItemGroupsSheet, index+2, []string{strconv.FormatInt(link.ItemID, 10), strconv.FormatInt(link.GroupID, 10)}); err != nil {
			return nil, err
		}
	}

	if err := writeXLSXRow(book, xlsxSensitiveRulesSheet, 1, xlsxSensitiveRulesHeader); err != nil {
		return nil, err
	}
	for index, pattern := range export.Settings.Sensitive.CustomPatterns {
		if err := writeXLSXRow(book, xlsxSensitiveRulesSheet, index+2, []string{pattern}); err != nil {
			return nil, err
		}
	}

	data, err := book.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("write XLSX: %w", err)
	}
	return data.Bytes(), nil
}

func writeXLSXRow(book *excelize.File, sheet string, row int, values []string) error {
	for column, value := range values {
		if len(utf16.Encode([]rune(value))) > xlsxCellTextLimit {
			return fmt.Errorf("XLSX %s row %d column %d exceeds the %d-character cell limit", sheet, row, column+1, xlsxCellTextLimit)
		}
		cell, err := excelize.CoordinatesToCellName(column+1, row)
		if err != nil {
			return fmt.Errorf("XLSX %s row %d: %w", sheet, row, err)
		}
		if err := book.SetCellStr(sheet, cell, value); err != nil {
			return fmt.Errorf("XLSX %s row %d column %d: %w", sheet, row, column+1, err)
		}
	}
	return nil
}
