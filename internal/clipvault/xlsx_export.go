package clipvault

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
	"unicode/utf16"

	"clipboard_ext/internal/clipvault/domain"

	"github.com/xuri/excelize/v2"
)

const (
	currentXLSXVersion      = 9
	xlsxManifestSheet       = "Manifest"
	xlsxRecordsSheet        = "Records"
	xlsxGroupsSheet         = "Groups"
	xlsxItemGroupsSheet     = "ItemGroups"
	xlsxSensitiveRulesSheet = "SensitiveRules"
	xlsxAssetsSheet         = "Assets"
	xlsxCellTextLimit       = 32767
	xlsxUnzipSizeLimit      = 512 << 20
	xlsxUnzipXMLSizeLimit   = 32 << 20
	xlsxAssetBytesLimit     = 400 << 20
)

var (
	errXLSXSizeLimit         = errors.New("XLSX size limit exceeded")
	xlsxManifestHeader       = []string{"app", "exportVersion"}
	xlsxRecordsHeader        = []string{"id", "createdAt", "recordType", "kind", "format", "text", "byteLen", "charLen", "contentSha256", "consecutiveDuplicate", "metaJson", "starred", "sensitive", "eventCode", "eventFingerprint", "captureSequence", "captureStage", "captureAttempts", "eventFormats", "errorCategory", "sourceExeName", "sourceExePath", "sourceWindowTitle", "sourceAttribution", "content"}
	xlsxGroupsHeader         = []string{"id", "name", "parentId", "createdAt"}
	xlsxItemGroupsHeader     = []string{"itemId", "groupId"}
	xlsxSensitiveRulesHeader = []string{"pattern"}
	xlsxAssetsHeader         = []string{"itemId", "sha256", "byteLen", "packageEntry"}
)

// ExportXLSX produces a complete, portable spreadsheet export. All six
// data sheets and the TextChunks sheet are required by ImportXLSX and map exactly to ExportData.
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
// the ZIP archive in memory. Excelize stream writers spool worksheet XML as
// needed; the generated workbook is then repackaged from an owner-only temp
// file while managed image entries are streamed into dst.
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
	for _, sheet := range []string{xlsxGroupsSheet, xlsxItemGroupsSheet, xlsxSensitiveRulesSheet, xlsxAssetsSheet, xlsxManifestSheet} {
		if _, err := book.NewSheet(sheet); err != nil {
			return 0, fmt.Errorf("create %s sheet: %w", sheet, err)
		}
	}
	chunks, err := newXLSXTextWriter(book)
	if err != nil {
		return 0, err
	}
	manifest, err := book.NewStreamWriter(xlsxManifestSheet)
	if err != nil {
		return 0, fmt.Errorf("open manifest stream: %w", err)
	}
	if err := chunks.writeStreamRow(manifest, xlsxManifestSheet, 1, xlsxManifestHeader); err != nil {
		return 0, err
	}
	if err := chunks.writeStreamRow(manifest, xlsxManifestSheet, 2, []string{"ClipVault", strconv.Itoa(currentXLSXVersion)}); err != nil {
		return 0, err
	}
	if err := manifest.Flush(); err != nil {
		return 0, fmt.Errorf("flush manifest sheet: %w", err)
	}

	records, err := book.NewStreamWriter(xlsxRecordsSheet)
	if err != nil {
		return 0, fmt.Errorf("open records stream: %w", err)
	}
	if err := chunks.writeStreamRow(records, xlsxRecordsSheet, 1, xlsxRecordsHeader); err != nil {
		return 0, err
	}
	itemSQL, itemArgs, err := exportItemsQuery(scope)
	if err != nil {
		return 0, fmt.Errorf("build XLSX records query: %w", err)
	}
	rows, err := tx.QueryContext(ctx, itemSQL, itemArgs...)
	if err != nil {
		return 0, fmt.Errorf("query XLSX records: %w", err)
	}
	rowNumber := 2
	var maxID int64
	var assetFiles []exportAssetFile
	for rows.Next() {
		var item StoredItem
		if err := scanStoredItem(rows, &item); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan XLSX record: %w", err)
		}
		eventFormats := encodeClipboardFormats(item.EventFormats)
		values := []string{
			strconv.FormatInt(item.ID, 10), item.CreatedAt.UTC().Format(time.RFC3339Nano), string(item.RecordType), item.Kind, item.Format, item.Text,
			strconv.Itoa(item.ByteLen), strconv.Itoa(item.CharLen), valueOrEmpty(item.ContentSHA256), strconv.FormatBool(item.ConsecutiveDuplicate), item.MetaJSON,
			strconv.FormatBool(item.Starred), strconv.FormatBool(item.Sensitive), string(item.EventCode), item.EventFingerprint,
			strconv.FormatUint(uint64(item.CaptureSequence), 10), item.CaptureStage, strconv.Itoa(item.CaptureAttempts), eventFormats, item.ErrorCategory,
			item.SourceExeName, item.SourceExePath, item.SourceWindowTitle, item.SourceAttribution, compositeJSONString(item.Content),
		}
		if err := chunks.writeStreamRow(records, xlsxRecordsSheet, rowNumber, values); err != nil {
			rows.Close()
			return 0, err
		}
		if item.Format == domain.FormatImageFile {
			asset, err := s.loadExportAsset(item)
			if err != nil {
				rows.Close()
				return 0, err
			}
			assetFiles = append(assetFiles, asset)
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
	if err := chunks.writeStreamRow(groups, xlsxGroupsSheet, 1, xlsxGroupsHeader); err != nil {
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
		var createdMS int64
		if err := groupRows.Scan(&id, &name, &parentID, &createdMS); err != nil {
			groupRows.Close()
			return 0, fmt.Errorf("scan XLSX group: %w", err)
		}
		if err := chunks.writeStreamRow(groups, xlsxGroupsSheet, rowNumber, []string{strconv.FormatInt(id, 10), name, strconv.FormatInt(parentID, 10), time.UnixMilli(createdMS).UTC().Format(time.RFC3339Nano)}); err != nil {
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
	if err := chunks.writeStreamRow(links, xlsxItemGroupsSheet, 1, xlsxItemGroupsHeader); err != nil {
		return 0, err
	}
	linkSQL, linkArgs, err := exportLinksQuery(scope)
	if err != nil {
		return 0, fmt.Errorf("build XLSX item-groups query: %w", err)
	}
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
		if err := chunks.writeStreamRow(links, xlsxItemGroupsSheet, rowNumber, []string{strconv.FormatInt(itemID, 10), strconv.FormatInt(groupID, 10)}); err != nil {
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
	if err := chunks.writeStreamRow(rules, xlsxSensitiveRulesSheet, 1, xlsxSensitiveRulesHeader); err != nil {
		return 0, err
	}
	cfg, err := getConfig(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("export XLSX settings: %w", err)
	}
	for index, pattern := range cfg.Sensitive.CustomPatterns {
		if err := chunks.writeStreamRow(rules, xlsxSensitiveRulesSheet, index+2, []string{pattern}); err != nil {
			return 0, err
		}
	}
	if err := rules.Flush(); err != nil {
		return 0, fmt.Errorf("flush sensitive-rules sheet: %w", err)
	}

	assets, err := book.NewStreamWriter(xlsxAssetsSheet)
	if err != nil {
		return 0, fmt.Errorf("open assets stream: %w", err)
	}
	if err := chunks.writeStreamRow(assets, xlsxAssetsSheet, 1, xlsxAssetsHeader); err != nil {
		return 0, err
	}
	for index, asset := range assetFiles {
		if err := chunks.writeStreamRow(assets, xlsxAssetsSheet, index+2, []string{
			strconv.FormatInt(asset.ItemID, 10), asset.SHA256, strconv.Itoa(asset.ByteLen), exportAssetPackageEntry(asset.ItemID),
		}); err != nil {
			return 0, err
		}
	}
	if err := assets.Flush(); err != nil {
		return 0, fmt.Errorf("flush assets sheet: %w", err)
	}
	sources := make([]xlsxAssetSource, 0, len(assetFiles))
	for _, asset := range assetFiles {
		sources = append(sources, xlsxAssetSource{asset: asset.ExportAsset, fullPath: asset.fullPath})
	}
	if err := chunks.flush(); err != nil {
		return 0, err
	}
	if err := writeGeneratedXLSXPackage(dst, book, sources); err != nil {
		return 0, err
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
	for _, sheet := range []string{xlsxGroupsSheet, xlsxItemGroupsSheet, xlsxSensitiveRulesSheet, xlsxAssetsSheet, xlsxManifestSheet} {
		if _, err := book.NewSheet(sheet); err != nil {
			return nil, fmt.Errorf("create %s sheet: %w", sheet, err)
		}
	}
	chunks, err := newXLSXTextWriter(book)
	if err != nil {
		return nil, err
	}
	if err := chunks.writeRow(book, xlsxManifestSheet, 1, xlsxManifestHeader); err != nil {
		return nil, err
	}
	if err := chunks.writeRow(book, xlsxManifestSheet, 2, []string{"ClipVault", strconv.Itoa(currentXLSXVersion)}); err != nil {
		return nil, err
	}

	if err := chunks.writeRow(book, xlsxRecordsSheet, 1, xlsxRecordsHeader); err != nil {
		return nil, err
	}
	for index, item := range export.Items {
		eventFormats := encodeClipboardFormats(item.EventFormats)
		row := []string{
			strconv.FormatInt(item.ID, 10), item.CreatedAt.UTC().Format(time.RFC3339Nano), string(item.RecordType), item.Kind, item.Format, item.Text,
			strconv.Itoa(item.ByteLen), strconv.Itoa(item.CharLen), valueOrEmpty(item.ContentSHA256), strconv.FormatBool(item.ConsecutiveDuplicate), item.MetaJSON,
			strconv.FormatBool(item.Starred), strconv.FormatBool(item.Sensitive), string(item.EventCode), item.EventFingerprint,
			strconv.FormatUint(uint64(item.CaptureSequence), 10), item.CaptureStage, strconv.Itoa(item.CaptureAttempts), eventFormats, item.ErrorCategory,
			item.SourceExeName, item.SourceExePath, item.SourceWindowTitle, item.SourceAttribution, compositeJSONString(item.Content),
		}
		if err := chunks.writeRow(book, xlsxRecordsSheet, index+2, row); err != nil {
			return nil, err
		}
	}

	if err := chunks.writeRow(book, xlsxGroupsSheet, 1, xlsxGroupsHeader); err != nil {
		return nil, err
	}
	for index, group := range export.Groups {
		row := []string{strconv.FormatInt(group.ID, 10), group.Name, strconv.FormatInt(group.ParentID, 10), group.CreatedAt.UTC().Format(time.RFC3339Nano)}
		if err := chunks.writeRow(book, xlsxGroupsSheet, index+2, row); err != nil {
			return nil, err
		}
	}

	if err := chunks.writeRow(book, xlsxItemGroupsSheet, 1, xlsxItemGroupsHeader); err != nil {
		return nil, err
	}
	for index, link := range export.ItemGroups {
		if err := chunks.writeRow(book, xlsxItemGroupsSheet, index+2, []string{strconv.FormatInt(link.ItemID, 10), strconv.FormatInt(link.GroupID, 10)}); err != nil {
			return nil, err
		}
	}

	if err := chunks.writeRow(book, xlsxSensitiveRulesSheet, 1, xlsxSensitiveRulesHeader); err != nil {
		return nil, err
	}
	for index, pattern := range export.Settings.Sensitive.CustomPatterns {
		if err := chunks.writeRow(book, xlsxSensitiveRulesSheet, index+2, []string{pattern}); err != nil {
			return nil, err
		}
	}
	if err := validateExportAssetSet(export.Items, export.Assets); err != nil {
		return nil, err
	}
	if err := chunks.writeRow(book, xlsxAssetsSheet, 1, xlsxAssetsHeader); err != nil {
		return nil, err
	}
	for index, asset := range export.Assets {
		if err := chunks.writeRow(book, xlsxAssetsSheet, index+2, []string{
			strconv.FormatInt(asset.ItemID, 10), asset.SHA256, strconv.Itoa(asset.ByteLen), exportAssetPackageEntry(asset.ItemID),
		}); err != nil {
			return nil, err
		}
	}

	if err := chunks.flush(); err != nil {
		return nil, err
	}
	data, err := book.WriteToBuffer()
	if err != nil {
		return nil, fmt.Errorf("write XLSX: %w", err)
	}
	var packaged bytes.Buffer
	sources := make([]xlsxAssetSource, 0, len(export.Assets))
	for _, asset := range export.Assets {
		sources = append(sources, xlsxAssetSource{asset: asset})
	}
	if err := writeXLSXPackage(&packaged, data.Bytes(), sources); err != nil {
		return nil, err
	}
	return packaged.Bytes(), nil
}

type xlsxAssetSource struct {
	asset    ExportAsset
	fullPath string
}

func (source xlsxAssetSource) writeTo(dst io.Writer) error {
	if source.fullPath == "" {
		if err := validateExportAssetData(source.asset); err != nil {
			return err
		}
		_, err := io.Copy(dst, bytes.NewReader(source.asset.Data))
		return err
	}
	return (exportAssetFile{ExportAsset: source.asset, fullPath: source.fullPath}).copyTo(dst)
}

func writeXLSXPackage(dst io.Writer, workbook []byte, assets []xlsxAssetSource) error {
	return writeXLSXPackageReader(dst, bytes.NewReader(workbook), int64(len(workbook)), assets)
}

func writeGeneratedXLSXPackage(dst io.Writer, book *excelize.File, assets []xlsxAssetSource) error {
	workbook, err := os.CreateTemp("", "clipvault-xlsx-workbook-*.xlsx")
	if err != nil {
		return fmt.Errorf("create XLSX staging file: %w", err)
	}
	name := workbook.Name()
	defer func() {
		_ = workbook.Close()
		_ = os.Remove(name)
	}()
	if err := book.Write(workbook); err != nil {
		return fmt.Errorf("write XLSX staging file: %w", err)
	}
	info, err := workbook.Stat()
	if err != nil {
		return fmt.Errorf("stat XLSX staging file: %w", err)
	}
	return writeXLSXPackageReader(dst, workbook, info.Size(), assets)
}

func writeXLSXPackageReader(dst io.Writer, workbook io.ReaderAt, workbookSize int64, assets []xlsxAssetSource) error {
	var assetBytes int64
	for _, source := range assets {
		if source.asset.ByteLen <= 0 {
			return fmt.Errorf("invalid XLSX asset size for item %d", source.asset.ItemID)
		}
		assetBytes += int64(source.asset.ByteLen)
		if assetBytes > xlsxAssetBytesLimit {
			return fmt.Errorf("%w: managed image assets exceed %d bytes", errXLSXSizeLimit, xlsxAssetBytesLimit)
		}
	}
	reader, err := zip.NewReader(workbook, workbookSize)
	if err != nil {
		return fmt.Errorf("open generated XLSX package: %w", err)
	}
	// Import counts all expanded ZIP entries, including the image assets.
	// Check the generated workbook before writing a success-shaped archive.
	remaining := uint64(xlsxUnzipSizeLimit) - uint64(assetBytes)
	for _, entry := range reader.File {
		if entry.UncompressedSize64 > remaining {
			return fmt.Errorf("%w: expanded package exceeds %d bytes", errXLSXSizeLimit, xlsxUnzipSizeLimit)
		}
		remaining -= entry.UncompressedSize64
	}
	writer := zip.NewWriter(dst)
	closeWithError := func(result error) error {
		return errors.Join(result, writer.Close())
	}
	for _, entry := range reader.File {
		header := entry.FileHeader
		out, err := writer.CreateHeader(&header)
		if err != nil {
			return closeWithError(fmt.Errorf("copy XLSX entry %s: %w", entry.Name, err))
		}
		in, err := entry.Open()
		if err != nil {
			return closeWithError(fmt.Errorf("open XLSX entry %s: %w", entry.Name, err))
		}
		_, copyErr := io.Copy(out, in)
		closeErr := in.Close()
		if copyErr != nil || closeErr != nil {
			return closeWithError(errors.Join(copyErr, closeErr))
		}
	}
	for _, source := range assets {
		if source.asset.ItemID <= 0 || !validContentSHA256(source.asset.SHA256) {
			return closeWithError(fmt.Errorf("invalid XLSX asset metadata for item %d", source.asset.ItemID))
		}
		header := &zip.FileHeader{Name: exportAssetPackageEntry(source.asset.ItemID), Method: zip.Store}
		header.SetMode(0o600)
		out, err := writer.CreateHeader(header)
		if err != nil {
			return closeWithError(fmt.Errorf("create XLSX asset for item %d: %w", source.asset.ItemID, err))
		}
		if err := source.writeTo(out); err != nil {
			return closeWithError(fmt.Errorf("write XLSX asset for item %d: %w", source.asset.ItemID, err))
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close XLSX package: %w", err)
	}
	return nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
