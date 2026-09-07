package clipvault

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestXLSXPackageRejectsUnimportableSize(t *testing.T) {
	var workbook bytes.Buffer
	zw := zip.NewWriter(&workbook)
	entry, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	// The package limit concerns expanded bytes, even when compression makes
	// the workbook tiny. Stream the fixture without allocating a 512 MiB body.
	chunk := bytes.Repeat([]byte(" "), 1<<20)
	for written := 0; written < xlsxUnzipSizeLimit; written += len(chunk) {
		if _, err := entry.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := entry.Write([]byte(" ")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeXLSXPackage(io.Discard, workbook.Bytes(), nil); !errors.Is(err, errXLSXSizeLimit) {
		t.Fatalf("XLSX export must reject an expanded package above the import limit: %v", err)
	}
}
