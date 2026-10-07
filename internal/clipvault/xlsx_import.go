package clipvault

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"clipboard_ext/internal/clipvault/domain"

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
// ExportData carrier. Format- and envelope-level validation happens here
// (sheet set, headers, formula rejection, per-field syntax), while data-semantic
// validation stays in importExportData so both import formats share one rule set.
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
	manifest, err := readXLSXRows(book, xlsxManifestSheet, xlsxManifestHeader)
	if err != nil {
		return ExportData{}, err
	}
	if len(manifest) != 1 || manifest[0][0] != "ClipVault" {
		return ExportData{}, fmt.Errorf("invalid XLSX manifest")
	}
	version, err := strconv.Atoi(manifest[0][1])
	if err != nil || version != currentXLSXVersion {
		return ExportData{}, fmt.Errorf("unsupported XLSX export version %q", manifest[0][1])
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
	assetRows, err := readXLSXRows(book, xlsxAssetsSheet, xlsxAssetsHeader)
	if err != nil {
		return ExportData{}, err
	}

	if err := restoreXLSXText(book, map[string][][]string{
		xlsxRecordsSheet: records, xlsxGroupsSheet: groups, xlsxItemGroupsSheet: itemGroups, xlsxSensitiveRulesSheet: rules, xlsxAssetsSheet: assetRows,
	}); err != nil {
		return ExportData{}, err
	}
	export := ExportData{App: "ClipVault", ExportVersion: currentExportVersion}
	for row, values := range records {
		item, err := parseXLSXItem(values)
		if err != nil {
			return ExportData{}, fmt.Errorf("records row %d: %w", row+2, err)
		}
		export.Items = append(export.Items, item)
	}
	for row, values := range groups {
		group, err := parseXLSXGroup(values)
		if err != nil {
			return ExportData{}, fmt.Errorf("groups row %d: %w", row+2, err)
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
	export.Assets, err = decodeXLSXAssets(data, assetRows)
	if err != nil {
		return ExportData{}, err
	}
	return export, nil
}

func validateXLSXSheets(book *excelize.File) error {
	want := map[string]struct{}{
		xlsxTextChunksSheet: {}, xlsxManifestSheet: {}, xlsxRecordsSheet: {}, xlsxGroupsSheet: {}, xlsxItemGroupsSheet: {}, xlsxSensitiveRulesSheet: {}, xlsxAssetsSheet: {},
	}
	sheets := book.GetSheetList()
	if len(sheets) != len(want) {
		return fmt.Errorf("invalid XLSX sheets: expected Manifest, Records, Groups, ItemGroups, SensitiveRules, Assets, TextChunks")
	}
	for _, sheet := range sheets {
		if _, ok := want[sheet]; !ok {
			return fmt.Errorf("invalid XLSX sheet %q", sheet)
		}
	}
	return nil
}

func decodeXLSXAssets(data []byte, rows [][]string) ([]ExportAsset, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open XLSX asset package: %w", err)
	}
	entries := make(map[string][]*zip.File)
	for _, entry := range reader.File {
		entries[entry.Name] = append(entries[entry.Name], entry)
	}
	assets := make([]ExportAsset, 0, len(rows))
	seenItems := make(map[int64]struct{}, len(rows))
	var totalBytes int64
	for row, values := range rows {
		itemID, err := parseXLSXPositiveInt(values[0])
		if err != nil {
			return nil, fmt.Errorf("Assets row %d itemId: %w", row+2, err)
		}
		if _, duplicate := seenItems[itemID]; duplicate {
			return nil, fmt.Errorf("Assets row %d duplicates item %d", row+2, itemID)
		}
		byteLen, err := parseXLSXPositiveInt(values[2])
		if err != nil || byteLen > maxImageMaxBytes {
			return nil, fmt.Errorf("Assets row %d has an invalid byteLen", row+2)
		}
		expectedEntry := exportAssetPackageEntry(itemID)
		if values[3] != expectedEntry {
			return nil, fmt.Errorf("Assets row %d has an invalid package entry", row+2)
		}
		matches := entries[expectedEntry]
		if len(matches) != 1 || matches[0].UncompressedSize64 != uint64(byteLen) {
			return nil, fmt.Errorf("Assets row %d package entry is missing, duplicated, or has the wrong size", row+2)
		}
		totalBytes += byteLen
		if totalBytes > xlsxAssetBytesLimit {
			return nil, fmt.Errorf("%w: managed image assets exceed %d bytes", errXLSXSizeLimit, xlsxAssetBytesLimit)
		}
		in, err := matches[0].Open()
		if err != nil {
			return nil, fmt.Errorf("open XLSX asset for item %d: %w", itemID, err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(in, byteLen+1))
		closeErr := in.Close()
		if readErr != nil || closeErr != nil {
			return nil, errors.Join(readErr, closeErr)
		}
		asset := ExportAsset{ItemID: itemID, SHA256: values[1], ByteLen: int(byteLen), Data: payload}
		if err := validateExportAssetData(asset); err != nil {
			return nil, fmt.Errorf("Assets row %d: %w", row+2, err)
		}
		assets = append(assets, asset)
		seenItems[itemID] = struct{}{}
	}
	for name := range entries {
		if strings.HasPrefix(name, "clipvault/assets/") {
			itemIDText := strings.TrimSuffix(strings.TrimPrefix(name, "clipvault/assets/"), ".png")
			itemID, err := strconv.ParseInt(itemIDText, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("unexpected XLSX asset entry %q", name)
			}
			if _, ok := seenItems[itemID]; !ok || name != exportAssetPackageEntry(itemID) {
				return nil, fmt.Errorf("unexpected XLSX asset entry %q", name)
			}
		}
	}
	return assets, nil
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
	var content *domain.CompositeContent
	if values[24] != "" {
		if err := json.Unmarshal([]byte(values[24]), &content); err != nil {
			return StoredItem{}, fmt.Errorf("content: %w", err)
		}
	}
	id, err := parseXLSXPositiveInt(values[0])
	if err != nil {
		return StoredItem{}, fmt.Errorf("id: %w", err)
	}
	createdAt, err := parseXLSXTime(values[1])
	if err != nil {
		return StoredItem{}, fmt.Errorf("createdAt: %w", err)
	}
	byteLen, err := parseXLSXNonNegativeInt(values[6])
	if err != nil {
		return StoredItem{}, fmt.Errorf("byteLen: %w", err)
	}
	charLen, err := parseXLSXNonNegativeInt(values[7])
	if err != nil {
		return StoredItem{}, fmt.Errorf("charLen: %w", err)
	}
	consecutiveDuplicate, err := strconv.ParseBool(values[9])
	if err != nil {
		return StoredItem{}, fmt.Errorf("consecutiveDuplicate: %w", err)
	}
	starred, err := strconv.ParseBool(values[11])
	if err != nil {
		return StoredItem{}, fmt.Errorf("starred: %w", err)
	}
	sensitive, err := strconv.ParseBool(values[12])
	if err != nil {
		return StoredItem{}, fmt.Errorf("sensitive: %w", err)
	}
	captureSequence, err := strconv.ParseUint(values[15], 10, 32)
	if err != nil {
		return StoredItem{}, fmt.Errorf("captureSequence: %w", err)
	}
	captureAttempts, err := parseXLSXNonNegativeInt(values[17])
	if err != nil {
		return StoredItem{}, fmt.Errorf("captureAttempts: %w", err)
	}
	var eventFormats []domain.ClipboardFormatInfo
	if err := json.Unmarshal([]byte(values[18]), &eventFormats); err != nil {
		return StoredItem{}, fmt.Errorf("eventFormats: %w", err)
	}
	var contentHash *string
	if values[8] != "" {
		value := values[8]
		contentHash = &value
	}
	maxInt := int64(^uint(0) >> 1)
	if byteLen > maxInt || charLen > maxInt {
		return StoredItem{}, fmt.Errorf("byteLen and charLen exceed the platform integer limit")
	}
	return StoredItem{
		Content: content,
		ID:      id, CreatedAt: createdAt, RecordType: domain.HistoryRecordType(values[2]), Kind: values[3], Format: values[4], Text: values[5],
		ByteLen: int(byteLen), CharLen: int(charLen), ContentSHA256: contentHash,
		ConsecutiveDuplicate: consecutiveDuplicate, MetaJSON: values[10],
		Starred: starred, Sensitive: sensitive,
		EventCode: domain.ClipboardEventCode(values[13]), EventFingerprint: values[14], CaptureSequence: uint32(captureSequence),
		CaptureStage: values[16], CaptureAttempts: int(captureAttempts), EventFormats: eventFormats, ErrorCategory: values[19],
		SourceExeName: values[20], SourceExePath: values[21],
		SourceWindowTitle: values[22], SourceAttribution: values[23],
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
