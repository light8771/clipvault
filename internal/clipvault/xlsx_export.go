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
	xlsxRecordsSheet        = "Records"
	xlsxGroupsSheet         = "Groups"
	xlsxItemGroupsSheet     = "ItemGroups"
	xlsxSensitiveRulesSheet = "SensitiveRules"
	xlsxCellTextLimit       = 32767
	xlsxUnzipSizeLimit      = 256 << 20
	xlsxUnzipXMLSizeLimit   = 32 << 20
)

var (
	xlsxRecordsHeader        = []string{"id", "createdAt", "kind", "format", "text", "byteLen", "charLen", "sha256", "metaJson", "starred", "sensitive", "sourceExeName", "sourceExePath", "sourceWindowTitle", "sourceMethod"}
	xlsxGroupsHeader         = []string{"id", "name", "parentId", "createdAt"}
	xlsxItemGroupsHeader     = []string{"itemId", "groupId"}
	xlsxSensitiveRulesHeader = []string{"pattern"}
)

// ExportXLSX produces a complete, portable spreadsheet export. All four
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
	for _, sheet := range []string{xlsxGroupsSheet, xlsxItemGroupsSheet, xlsxSensitiveRulesSheet} {
		if _, err := book.NewSheet(sheet); err != nil {
			return 0, fmt.Errorf("create %s sheet: %w", sheet, err)
		}
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
		var name, created string
		if err := groupRows.Scan(&id, &name, &parentID, &created); err != nil {
			groupRows.Close()
			return 0, fmt.Errorf("scan XLSX group: %w", err)
		}
		if err := writeXLSXStreamRow(groups, xlsxGroupsSheet, rowNumber, []string{strconv.FormatInt(id, 10), name, strconv.FormatInt(parentID, 10), created}); err != nil {
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
	for _, sheet := range []string{xlsxGroupsSheet, xlsxItemGroupsSheet, xlsxSensitiveRulesSheet} {
		if _, err := book.NewSheet(sheet); err != nil {
			return nil, fmt.Errorf("create %s sheet: %w", sheet, err)
		}
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

// ImportXLSX parses the strict ClipVault workbook contract and commits it
// through the same transaction as JSON imports.
func (s *Store) ImportXLSX(ctx context.Context, data []byte) error {
	export, err := decodeImportXLSX(data)
	if err != nil {
		return err
	}
	return s.importExportData(ctx, export)
}

// decodeImportXLSX parses a ClipVault workbook into the format-agnostic
// ExportData carrier. It is the XLSX half of the decoder/persister split and
// mirrors decodeImportJSON: format- and envelope-level validation happens here
// (sheet set, headers, formula rejection, per-field syntax), while
// data-semantic validation stays in importExportData so both import formats
// keep sharing one rule set.
//
// The workbook is closed before this function returns, i.e. earlier than when
// the decode was still inlined in ImportXLSX. That is safe: readXLSXRows
// resolves every cell through GetRows/GetCellValue, which fully materialize
// into freshly allocated Go strings, and the returned ExportData holds no
// handle, decoder or lazy reference into the excelize file. Closing here also
// releases the workbook XML (up to xlsxUnzipSizeLimit) before the import
// transaction opens instead of holding it for the whole transaction.
func decodeImportXLSX(data []byte) (ExportData, error) {
	book, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{
		UnzipSizeLimit:    xlsxUnzipSizeLimit,
		UnzipXMLSizeLimit: xlsxUnzipXMLSizeLimit,
	})
	if err != nil {
		return ExportData{}, fmt.Errorf("open XLSX: %w", err)
	}
	defer book.Close()

	if err := validateXLSXSheets(book); err != nil {
		return ExportData{}, err
	}
	records, err := readXLSXRows(book, xlsxRecordsSheet, xlsxRecordsHeader)
	if err != nil {
		return ExportData{}, err
	}
	groups, err := readXLSXRows(book, xlsxGroupsSheet, xlsxGroupsHeader)
	if err != nil {
		return ExportData{}, err
	}
	itemGroups, err := readXLSXRows(book, xlsxItemGroupsSheet, xlsxItemGroupsHeader)
	if err != nil {
		return ExportData{}, err
	}
	rules, err := readXLSXRows(book, xlsxSensitiveRulesSheet, xlsxSensitiveRulesHeader)
	if err != nil {
		return ExportData{}, err
	}

	export := ExportData{App: "ClipVault", ExportVersion: 3}
	for row, values := range records {
		item, err := parseXLSXItem(values)
		if err != nil {
			return ExportData{}, fmt.Errorf("Records row %d: %w", row+2, err)
		}
		export.Items = append(export.Items, item)
	}
	for row, values := range groups {
		group, err := parseXLSXGroup(values)
		if err != nil {
			return ExportData{}, fmt.Errorf("Groups row %d: %w", row+2, err)
		}
		export.Groups = append(export.Groups, group)
	}
	for row, values := range itemGroups {
		itemID, err := parseXLSXPositiveInt(values[0])
		if err != nil {
			return ExportData{}, fmt.Errorf("ItemGroups row %d itemId: %w", row+2, err)
		}
		groupID, err := parseXLSXPositiveInt(values[1])
		if err != nil {
			return ExportData{}, fmt.Errorf("ItemGroups row %d groupId: %w", row+2, err)
		}
		export.ItemGroups = append(export.ItemGroups, ItemGroupLink{ItemID: itemID, GroupID: groupID})
	}
	for row, values := range rules {
		if values[0] == "" {
			return ExportData{}, fmt.Errorf("SensitiveRules row %d: pattern must not be empty", row+2)
		}
		export.Settings.Sensitive.CustomPatterns = append(export.Settings.Sensitive.CustomPatterns, values[0])
	}
	return export, nil
}

func validateXLSXSheets(book *excelize.File) error {
	want := map[string]struct{}{
		xlsxRecordsSheet: {}, xlsxGroupsSheet: {}, xlsxItemGroupsSheet: {}, xlsxSensitiveRulesSheet: {},
	}
	sheets := book.GetSheetList()
	if len(sheets) != len(want) {
		return fmt.Errorf("invalid XLSX sheets: expected Records, Groups, ItemGroups, SensitiveRules")
	}
	for _, sheet := range sheets {
		if _, ok := want[sheet]; !ok {
			return fmt.Errorf("invalid XLSX sheet %q", sheet)
		}
	}
	return nil
}

func readXLSXRows(book *excelize.File, sheet string, header []string) ([][]string, error) {
	rows, err := book.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read %s sheet: %w", sheet, err)
	}
	if len(rows) == 0 || !sameStrings(rows[0], header) {
		return nil, fmt.Errorf("invalid %s header", sheet)
	}
	for column := range header {
		cell, err := excelize.CoordinatesToCellName(column+1, 1)
		if err != nil {
			return nil, err
		}
		formula, err := book.GetCellFormula(sheet, cell)
		if err != nil {
			return nil, fmt.Errorf("read %s header: %w", sheet, err)
		}
		if formula != "" {
			return nil, fmt.Errorf("%s header column %d must be text, not a formula", sheet, column+1)
		}
	}
	values := make([][]string, 0, len(rows)-1)
	for row := 2; row <= len(rows); row++ {
		rowValues := rows[row-1]
		for column, value := range rowValues[min(len(rowValues), len(header)):] {
			cell, err := excelize.CoordinatesToCellName(len(header)+column+1, row)
			if err != nil {
				return nil, err
			}
			formula, err := book.GetCellFormula(sheet, cell)
			if err != nil {
				return nil, fmt.Errorf("read %s row %d: %w", sheet, row, err)
			}
			if value != "" || formula != "" {
				return nil, fmt.Errorf("%s row %d has an unexpected column %d", sheet, row, len(header)+column+1)
			}
		}
		result := make([]string, len(header))
		nonEmpty := false
		for column := range header {
			cell, err := excelize.CoordinatesToCellName(column+1, row)
			if err != nil {
				return nil, err
			}
			formula, err := book.GetCellFormula(sheet, cell)
			if err != nil {
				return nil, fmt.Errorf("read %s row %d: %w", sheet, row, err)
			}
			if formula != "" {
				return nil, fmt.Errorf("%s row %d column %d must be text, not a formula", sheet, row, column+1)
			}
			result[column], err = book.GetCellValue(sheet, cell)
			if err != nil {
				return nil, fmt.Errorf("read %s row %d: %w", sheet, row, err)
			}
			nonEmpty = nonEmpty || result[column] != ""
		}
		if !nonEmpty {
			return nil, fmt.Errorf("%s row %d is empty", sheet, row)
		}
		values = append(values, result)
	}
	return values, nil
}

func parseXLSXItem(values []string) (StoredItem, error) {
	id, err := parseXLSXPositiveInt(values[0])
	if err != nil {
		return StoredItem{}, fmt.Errorf("id: %w", err)
	}
	createdAt, err := parseXLSXTime(values[1])
	if err != nil {
		return StoredItem{}, fmt.Errorf("createdAt: %w", err)
	}
	byteLen, err := parseXLSXNonNegativeInt(values[5])
	if err != nil {
		return StoredItem{}, fmt.Errorf("byteLen: %w", err)
	}
	charLen, err := parseXLSXNonNegativeInt(values[6])
	if err != nil {
		return StoredItem{}, fmt.Errorf("charLen: %w", err)
	}
	starred, err := strconv.ParseBool(values[9])
	if err != nil {
		return StoredItem{}, fmt.Errorf("starred: %w", err)
	}
	sensitive, err := strconv.ParseBool(values[10])
	if err != nil {
		return StoredItem{}, fmt.Errorf("sensitive: %w", err)
	}
	maxInt := int64(^uint(0) >> 1)
	if byteLen > maxInt || charLen > maxInt {
		return StoredItem{}, fmt.Errorf("byteLen and charLen exceed the platform integer limit")
	}
	return StoredItem{
		ID: id, CreatedAt: createdAt, Kind: values[2], Format: values[3], Text: values[4],
		ByteLen: int(byteLen), CharLen: int(charLen), SHA256: values[7], MetaJSON: values[8],
		Starred: starred, Sensitive: sensitive, SourceExeName: values[11], SourceExePath: values[12],
		SourceWindowTitle: values[13], SourceMethod: values[14],
	}, nil
}

func parseXLSXGroup(values []string) (StoredGroup, error) {
	id, err := parseXLSXPositiveInt(values[0])
	if err != nil {
		return StoredGroup{}, fmt.Errorf("id: %w", err)
	}
	parentID, err := parseXLSXNonNegativeInt(values[2])
	if err != nil {
		return StoredGroup{}, fmt.Errorf("parentId: %w", err)
	}
	createdAt, err := parseXLSXTime(values[3])
	if err != nil {
		return StoredGroup{}, fmt.Errorf("createdAt: %w", err)
	}
	return StoredGroup{ID: id, Name: values[1], ParentID: parentID, CreatedAt: createdAt}, nil
}

func parseXLSXPositiveInt(value string) (int64, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("must be a positive integer")
	}
	return number, nil
}

func parseXLSXNonNegativeInt(value string) (int64, error) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("must be a non-negative integer")
	}
	return number, nil
}

func parseXLSXTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
