package clipvault

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"clipboard_ext/internal/clipvault/transfer"
)

func TestOversizedJSONCannotBecomeSuccessfulBackup(t *testing.T) {
	store, _ := imageBackupSizeTestStore(t, 49)
	scope := ExportScope{Mode: "full"}
	if maxID, err := store.StreamExport(context.Background(), scope, io.Discard); maxID != 0 || !errors.Is(err, transfer.ErrJSONTooLarge) {
		t.Fatalf("oversized manual export: maxID=%d err=%v", maxID, err)
	}
	backupDir := t.TempDir()
	bs := &BackupService{store: store}
	path, size, maxID, err := bs.writeBackupFileStream(backupDir, time.Now(), "json", "SizeBoundaryPass", scope)
	if !errors.Is(err, transfer.ErrJSONTooLarge) || path != "" || size != 0 || maxID != 0 {
		t.Fatalf("oversized backup: path=%q size=%d maxID=%d err=%v", path, size, maxID, err)
	}
	files, err := os.ReadDir(backupDir)
	if err != nil || len(files) != 0 {
		t.Fatalf("failed backup left files: %v, %v", files, err)
	}
}

func imageBackupSizeTestStore(t *testing.T, count int) (*Store, string) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// A valid PNG plus trailing padding is accepted by the image validator.
	// Repeated references create a highly compressible >500 MiB JSON export
	// while keeping the fixture's database and on-disk image small.
	payload := append(managedImageTestPNG(t), make([]byte, 8<<20)...)
	storedPath := "images/2026/09/size-boundary.png"
	fullPath := filepath.Join(dataDir, filepath.FromSlash(storedPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := hashBytes(payload)
	for i := 0; i < count; i++ {
		if err := store.Save(context.Background(), ClipboardItem{
			RecordType: RecordTypeContent, Kind: "image", Format: "CLIPVAULT_IMAGE_FILE",
			Text: storedPath, ByteLen: len(payload), ContentSHA256: hash, MetaJSON: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return store, hash
}

func TestEmailBackupSplitsAtFormatSizeLimit(t *testing.T) {
	const recordCount = 51
	source, imageHash := imageBackupSizeTestStore(t, recordCount)
	bs := &BackupService{store: source}
	for _, format := range []string{"json", "cvexport", "xlsx"} {
		t.Run(format, func(t *testing.T) {
			cfg := DefaultConfig().Backup.EmailBackup
			cfg.Format, cfg.Password, cfg.MaxPartSize = format, "SizeBoundaryPass", 20<<20
			parts, ext, maxID, err := bs.exportEmailPartFilesSized(cfg, ExportScope{Mode: "full"})
			if err != nil {
				t.Fatal(err)
			}
			defer cleanupEmailPartFiles(parts)
			if len(parts) < 2 || ext != "cvexport" || maxID != recordCount {
				t.Fatalf("split backup: parts=%d ext=%q maxID=%d", len(parts), ext, maxID)
			}
			targetDir := t.TempDir()
			target, err := OpenStore(StoreOptions{DataDir: targetDir})
			if err != nil {
				t.Fatal(err)
			}
			defer target.Close()
			server := &SearchServer{store: target}
			lastID := int64(0)
			for _, part := range parts {
				if part.endID <= lastID || part.endID > maxID {
					t.Fatalf("invalid part boundary: %d after %d", part.endID, lastID)
				}
				size, err := emailBackupFileMessageSize(cfg, ext, time.Unix(0, 0), maxEmailParts-1, maxEmailParts, part.path)
				if err != nil || size > int64(cfg.MaxPartSize) {
					t.Fatalf("part MIME size=%d err=%v", size, err)
				}
				encrypted, err := os.Open(part.path)
				if err != nil {
					t.Fatal(err)
				}
				defer encrypted.Close()
				plain, err := os.CreateTemp(t.TempDir(), "plain-*")
				if err != nil {
					t.Fatal(err)
				}
				defer plain.Close()
				if err := DecryptCVExport(plain, encrypted, cfg.Password); err != nil {
					t.Fatal(err)
				}
				if err := server.importDecryptedExport(context.Background(), plain); err != nil {
					t.Fatal(err)
				}
				count, err := target.CountAll(context.Background())
				if err != nil || int64(count) != part.endID {
					t.Fatalf("restored through ID %d: count=%d err=%v", part.endID, count, err)
				}
				lastID = part.endID
			}
			if lastID != maxID {
				t.Fatalf("last part ends at %d, want %d", lastID, maxID)
			}
			items, total, err := target.Search(context.Background(), SearchOptions{Limit: 100, IncludeSensitive: true})
			if err != nil || total != recordCount || len(items) != recordCount {
				t.Fatalf("restored images: items=%d total=%d err=%v", len(items), total, err)
			}
			for _, item := range items {
				path, err := resolveManagedImagePath(Environment{DataDir: targetDir}, item.Text)
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil || hashBytes(data) != imageHash || item.ContentSHA256 == nil || *item.ContentSHA256 != imageHash {
					t.Fatalf("restored image %d differs: %v", item.ID, err)
				}
			}
		})
	}
}

func TestEmailBackupDoesNotSplitAroundMissingImage(t *testing.T) {
	dataDir := t.TempDir()
	source, err := OpenStore(StoreOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.Save(context.Background(), ClipboardItem{
		RecordType: RecordTypeContent, Kind: "text", Format: "CF_UNICODETEXT",
		Text: "record before missing image", MetaJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	_, _, imagePath := saveManagedImageForTransferTest(t, source, dataDir)
	if err := os.Remove(imagePath); err != nil {
		t.Fatal(err)
	}
	bs := &BackupService{store: source}
	for _, format := range []string{"json", "cvexport"} {
		t.Run(format, func(t *testing.T) {
			cfg := DefaultConfig().Backup.EmailBackup
			cfg.Format, cfg.Password, cfg.MaxPartSize = format, "SizeBoundaryPass", 20<<20
			parts, _, maxID, err := bs.exportEmailPartFilesSized(cfg, ExportScope{Mode: "full"})
			defer cleanupEmailPartFiles(parts)
			if !errors.Is(err, os.ErrNotExist) || len(parts) != 0 || maxID != 0 {
				t.Fatalf("missing image: parts=%d maxID=%d err=%v", len(parts), maxID, err)
			}
		})
	}
}
