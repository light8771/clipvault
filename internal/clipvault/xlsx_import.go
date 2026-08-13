package clipvault

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/xuri/excelize/v2"
)

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

	export := ExportData{App: "ClipVault", ExportVersion: currentExportVersion}
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
