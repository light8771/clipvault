package clipvault

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

func managedImageTestPNG(t *testing.T) []byte {
	t.Helper()
	var payload bytes.Buffer
	imageData := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	imageData.Set(0, 0, color.NRGBA{R: 20, G: 120, B: 220, A: 255})
	if err := png.Encode(&payload, imageData); err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}

func saveManagedImageForTransferTest(t *testing.T, store *Store, dataDir string) (StoredItem, []byte, string) {
	t.Helper()
	ctx := context.Background()
	payload := managedImageTestPNG(t)
	storedPath := "images/2026/09/original.png"
	full, err := resolveManagedImagePath(Environment{DataDir: dataDir}, storedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"imagePath": storedPath, "width": 2, "height": 2})
	if err := store.Save(ctx, ClipboardItem{
		CapturedAtMS: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC).UnixMilli(),
		RecordType:   domain.HistoryRecordContent, Kind: domain.KindImage, Format: domain.FormatImageFile,
		Text: storedPath, ByteLen: len(payload), CharLen: 0, ContentSHA256: hashBytes(payload), MetaJSON: string(meta),
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(ctx, SearchOptions{Kind: domain.KindImage, Limit: 10, IncludeSensitive: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("load saved image: items=%d err=%v", len(items), err)
	}
	return items[0], payload, full
}

func assertImportedManagedImage(t *testing.T, store *Store, dataDir string, payload []byte) {
	t.Helper()
	items, _, err := store.Search(context.Background(), SearchOptions{Kind: domain.KindImage, Limit: 10, IncludeSensitive: true})
	if err != nil || len(items) != 1 {
		t.Fatalf("imported image items=%d err=%v", len(items), err)
	}
	item := items[0]
	full, err := resolveManagedImagePath(Environment{DataDir: dataDir}, item.Text)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) || item.Text == "images/2026/09/original.png" {
		t.Fatalf("imported image was not recreated at a fresh managed path: %q", item.Text)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(item.MetaJSON), &meta); err != nil || meta["imagePath"] != item.Text {
		t.Fatalf("imported image metadata=%v err=%v", meta, err)
	}
}

func TestManagedImageRoundTripsThroughPortableExports(t *testing.T) {
	ctx := context.Background()
	sourceDir := t.TempDir()
	source, err := OpenStore(StoreOptions{DataDir: sourceDir})
	if err != nil {
		t.Fatal(err)
	}
	_, payload, _ := saveManagedImageForTransferTest(t, source, sourceDir)

	var jsonExport bytes.Buffer
	if _, err := source.StreamExport(ctx, ExportScope{Mode: "full"}, &jsonExport); err != nil {
		t.Fatal(err)
	}
	xlsxExport, err := source.ExportXLSX(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	for name, importData := range map[string]func(*Store) error{
		"json": func(target *Store) error { return target.ImportFullJSON(ctx, jsonExport.Bytes()) },
		"xlsx": func(target *Store) error { return target.ImportXLSX(ctx, xlsxExport) },
	} {
		t.Run(name, func(t *testing.T) {
			targetDir := t.TempDir()
			target, err := OpenStore(StoreOptions{DataDir: targetDir})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			if err := importData(target); err != nil {
				t.Fatal(err)
			}
			assertImportedManagedImage(t, target, targetDir, payload)
		})
	}
}

func TestManagedImageFilesAreRemovedWithHistory(t *testing.T) {
	for _, test := range []struct {
		name   string
		remove func(*Store, StoredItem) error
	}{
		{name: "delete", remove: func(store *Store, item StoredItem) error {
			return store.Delete(context.Background(), item.ID)
		}},
		{name: "cleanup", remove: func(store *Store, _ StoredItem) error {
			_, err := store.Cleanup(context.Background(), CleanupOptions{Mode: "all"})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			store, err := OpenStore(StoreOptions{DataDir: dataDir})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			item, _, full := saveManagedImageForTransferTest(t, store, dataDir)
			if err := test.remove(store, item); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(full); !os.IsNotExist(err) {
				t.Fatalf("managed image still exists after history removal: %v", err)
			}
		})
	}
}

func TestManagedImageFileIsKeptUntilItsLastHistoryReferenceIsDeleted(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first, payload, full := saveManagedImageForTransferTest(t, store, dataDir)
	if err := store.Save(ctx, ClipboardItem{
		CapturedAtMS: time.Date(2026, 9, 4, 12, 0, 1, 0, time.UTC).UnixMilli(),
		RecordType:   domain.HistoryRecordContent, Kind: domain.KindImage, Format: domain.FormatImageFile,
		Text: first.Text, ByteLen: len(payload), ContentSHA256: hashBytes(payload), MetaJSON: first.MetaJSON,
	}); err != nil {
		t.Fatal(err)
	}
	items, _, err := store.Search(ctx, SearchOptions{Kind: domain.KindImage, Limit: 10, IncludeSensitive: true})
	if err != nil || len(items) != 2 {
		t.Fatalf("shared image items=%d err=%v", len(items), err)
	}
	if err := store.Delete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("shared managed image was removed too early: %v", err)
	}
	remainingID := items[0].ID
	if remainingID == first.ID {
		remainingID = items[1].ID
	}
	if err := store.Delete(ctx, remainingID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Fatalf("managed image still exists after its final reference was deleted: %v", err)
	}
}

func TestManagedImageImportRejectsMissingAsset(t *testing.T) {
	dataDir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	payload := managedImageTestPNG(t)
	hash := hashBytes(payload)
	export := ExportData{
		ExportVersion: currentExportVersion, App: "ClipVault", ExportedAt: time.Now().UTC().Format(time.RFC3339),
		SourceEngine: "duckdb", Warning: "test",
		Items: []StoredItem{{
			ID: 1, CreatedAt: time.Now().UTC(), RecordType: domain.HistoryRecordContent, Kind: domain.KindImage,
			Format: domain.FormatImageFile, Text: "images/2026/09/missing.png", ByteLen: len(payload), ContentSHA256: &hash,
			MetaJSON: `{}`, SourceAttribution: "unknown",
		}},
		Settings: ExportSettings{Sensitive: SensitiveConfig{CustomPatterns: []string{}}},
	}
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ImportFullJSON(context.Background(), data); err == nil {
		t.Fatal("image import without its asset was accepted")
	}
}

func TestManagedImageValidationRejectsTruncatedPayload(t *testing.T) {
	payload := managedImageTestPNG(t)
	if len(payload) <= 33 {
		t.Fatalf("test PNG is unexpectedly short: %d", len(payload))
	}
	// Signature + IHDR is enough for DecodeConfig, but not a valid image.
	if err := validateManagedImagePNG(payload[:33]); err == nil {
		t.Fatal("truncated PNG payload was accepted")
	}
}
