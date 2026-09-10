package clipvault

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/xuri/excelize/v2"
)

const xlsxTextChunksSheet = "TextChunks"
const xlsxTextMarker = "ClipVault:TextChunks:"

var xlsxTextChunksHeader = []string{"sheet", "row", "column", "part", "total", "text"}

type xlsxTextWriter struct {
	stream *excelize.StreamWriter
	row    int
}

func newXLSXTextWriter(book *excelize.File) (*xlsxTextWriter, error) {
	if _, err := book.NewSheet(xlsxTextChunksSheet); err != nil {
		return nil, err
	}
	stream, err := book.NewStreamWriter(xlsxTextChunksSheet)
	if err != nil {
		return nil, err
	}
	if err := writeXLSXStreamRow(stream, xlsxTextChunksSheet, 1, xlsxTextChunksHeader); err != nil {
		return nil, err
	}
	return &xlsxTextWriter{stream: stream, row: 2}, nil
}

// Split at rune boundaries while respecting Excel's UTF-16 cell budget.
func splitXLSXText(value string) []string {
	var parts []string
	start, units := 0, 0
	for offset, r := range value {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > xlsxCellTextLimit {
			parts = append(parts, value[start:offset])
			start, units = offset, 0
		}
		units += width
	}
	return append(parts, value[start:])
}

func (w *xlsxTextWriter) prepare(sheet string, row int, values []string) ([]string, error) {
	result := append([]string(nil), values...)
	for column, value := range values {
		parts := splitXLSXText(value)
		// Escape literal markers too, so arbitrary copied text remains lossless.
		if len(parts) == 1 && !strings.HasPrefix(value, xlsxTextMarker) {
			continue
		}
		for index, part := range parts {
			if err := writeXLSXStreamRow(w.stream, xlsxTextChunksSheet, w.row, []string{
				sheet, strconv.Itoa(row), strconv.Itoa(column + 1), strconv.Itoa(index + 1), strconv.Itoa(len(parts)), part,
			}); err != nil {
				return nil, err
			}
			w.row++
		}
		result[column] = xlsxTextMarker + strconv.Itoa(len(parts))
	}
	return result, nil
}

func (w *xlsxTextWriter) writeStreamRow(stream *excelize.StreamWriter, sheet string, row int, values []string) error {
	values, err := w.prepare(sheet, row, values)
	if err != nil {
		return err
	}
	return writeXLSXStreamRow(stream, sheet, row, values)
}

func (w *xlsxTextWriter) writeRow(book *excelize.File, sheet string, row int, values []string) error {
	values, err := w.prepare(sheet, row, values)
	if err != nil {
		return err
	}
	return writeXLSXRow(book, sheet, row, values)
}

func (w *xlsxTextWriter) flush() error { return w.stream.Flush() }

type xlsxTextCell struct {
	sheet       string
	row, column int
}

func restoreXLSXText(book *excelize.File, sheets map[string][][]string) error {
	rows, err := readXLSXRows(book, xlsxTextChunksSheet, xlsxTextChunksHeader)
	if err != nil {
		return err
	}
	pending := make(map[xlsxTextCell]string)
	for sheet, records := range sheets {
		for row, values := range records {
			for column, value := range values {
				if strings.HasPrefix(value, xlsxTextMarker) {
					pending[xlsxTextCell{sheet, row + 2, column + 1}] = value
				}
			}
		}
	}
	for index := 0; index < len(rows); {
		first := rows[index]
		row, rowErr := strconv.Atoi(first[1])
		column, colErr := strconv.Atoi(first[2])
		total, totalErr := strconv.Atoi(first[4])
		key := xlsxTextCell{first[0], row, column}
		if rowErr != nil || colErr != nil || totalErr != nil || total < 1 || total > len(rows)-index || pending[key] != xlsxTextMarker+strconv.Itoa(total) {
			return fmt.Errorf("invalid TextChunks target or count at row %d", index+2)
		}
		var text strings.Builder
		for part := 1; part <= total; part++ {
			values := rows[index+part-1]
			if values[0] != first[0] || values[1] != first[1] || values[2] != first[2] || values[3] != strconv.Itoa(part) || values[4] != first[4] || values[5] == "" || len(utf16.Encode([]rune(values[5]))) > xlsxCellTextLimit {
				return fmt.Errorf("invalid TextChunks sequence at row %d", index+part+1)
			}
			text.WriteString(values[5])
		}
		sheets[key.sheet][key.row-2][key.column-1] = text.String()
		delete(pending, key)
		index += total
	}
	if len(pending) != 0 {
		return fmt.Errorf("XLSX text chunks are missing")
	}
	return nil
}
