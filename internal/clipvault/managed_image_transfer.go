package clipvault

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"clipboard_ext/internal/clipvault/domain"
)

type exportAssetFile struct {
	ExportAsset
	fullPath string
}

func exportAssetPackageEntry(itemID int64) string {
	return "clipvault/assets/" + strconv.FormatInt(itemID, 10) + ".png"
}

func (s *Store) loadExportAsset(item StoredItem) (exportAssetFile, error) {
	if item.RecordType != domain.HistoryRecordContent || item.Format != domain.FormatImageFile || item.ContentSHA256 == nil {
		return exportAssetFile{}, fmt.Errorf("item %d is not a managed image", item.ID)
	}
	full, err := resolveManagedImagePath(Environment{DataDir: s.dataDir}, item.Text)
	if err != nil {
		return exportAssetFile{}, fmt.Errorf("resolve managed image for item %d: %w", item.ID, err)
	}
	info, err := os.Lstat(full)
	if err != nil {
		return exportAssetFile{}, fmt.Errorf("stat managed image for item %d: %w", item.ID, err)
	}
	if !info.Mode().IsRegular() {
		return exportAssetFile{}, fmt.Errorf("managed image for item %d is not a regular file", item.ID)
	}
	if info.Size() != int64(item.ByteLen) || item.ByteLen <= 0 || item.ByteLen > maxImageMaxBytes {
		return exportAssetFile{}, fmt.Errorf("managed image size mismatch for item %d", item.ID)
	}
	asset := exportAssetFile{
		ExportAsset: ExportAsset{ItemID: item.ID, SHA256: *item.ContentSHA256, ByteLen: item.ByteLen},
		fullPath:    full,
	}
	if !validContentSHA256(asset.SHA256) {
		return exportAssetFile{}, fmt.Errorf("validate managed image for item %d: invalid content hash", item.ID)
	}
	if err := asset.copyTo(io.Discard); err != nil {
		return exportAssetFile{}, fmt.Errorf("validate managed image for item %d: %w", item.ID, err)
	}
	file, err := os.Open(full)
	if err != nil {
		return exportAssetFile{}, fmt.Errorf("open managed image for item %d: %w", item.ID, err)
	}
	validateErr := validateManagedImagePNGReader(file)
	closeErr := file.Close()
	if validateErr != nil || closeErr != nil {
		return exportAssetFile{}, fmt.Errorf("validate managed image for item %d: %w", item.ID, errors.Join(validateErr, closeErr))
	}
	return asset, nil
}

func (asset exportAssetFile) copyTo(dst io.Writer) error {
	info, err := os.Lstat(asset.fullPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(asset.ByteLen) {
		return errors.New("managed image asset changed during export")
	}
	file, err := os.Open(asset.fullPath)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(dst, hasher), file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if written != int64(asset.ByteLen) || hex.EncodeToString(hasher.Sum(nil)) != asset.SHA256 {
		return errors.New("managed image asset changed during export")
	}
	return nil
}

func (asset exportAssetFile) writeJSON(dst io.Writer) error {
	header := struct {
		ItemID  int64  `json:"itemId"`
		SHA256  string `json:"sha256"`
		ByteLen int    `json:"byteLen"`
	}{ItemID: asset.ItemID, SHA256: asset.SHA256, ByteLen: asset.ByteLen}
	encoded, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if _, err := dst.Write(encoded[:len(encoded)-1]); err != nil {
		return err
	}
	if _, err := io.WriteString(dst, `,"data":"`); err != nil {
		return err
	}
	base64Writer := base64.NewEncoder(base64.StdEncoding, dst)
	copyErr := asset.copyTo(base64Writer)
	closeErr := base64Writer.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	_, err = io.WriteString(dst, `"}`)
	return err
}

func validateExportAssetData(asset ExportAsset) error {
	if asset.ItemID <= 0 || asset.ByteLen <= 0 || asset.ByteLen > maxImageMaxBytes ||
		!validContentSHA256(asset.SHA256) || len(asset.Data) != asset.ByteLen {
		return errors.New("invalid managed image asset metadata")
	}
	if hashBytes(asset.Data) != asset.SHA256 {
		return errors.New("managed image asset hash mismatch")
	}
	if err := validateManagedImagePNG(asset.Data); err != nil {
		return fmt.Errorf("invalid managed image asset: %w", err)
	}
	return nil
}

func validateManagedImagePNG(data []byte) error {
	return validateManagedImagePNGReader(bytes.NewReader(data))
}

func validateManagedImagePNGReader(src io.Reader) error {
	// Inspect the fixed signature and IHDR prefix before png.Decode allocates
	// its output image. This keeps hostile dimensions behind the same pixel
	// boundary as clipboard capture while still validating the entire stream.
	var prefix [24]byte
	if _, err := io.ReadFull(src, prefix[:]); err != nil {
		return fmt.Errorf("invalid PNG header: %w", err)
	}
	if !bytes.Equal(prefix[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) ||
		binary.BigEndian.Uint32(prefix[8:12]) != 13 || !bytes.Equal(prefix[12:16], []byte("IHDR")) {
		return errors.New("invalid PNG header")
	}
	width := int(binary.BigEndian.Uint32(prefix[16:20]))
	height := int(binary.BigEndian.Uint32(prefix[20:24]))
	if err := validateDecodedImageDimensions(width, height); err != nil {
		return err
	}
	decoded, err := png.Decode(io.MultiReader(bytes.NewReader(prefix[:]), src))
	if err != nil {
		return fmt.Errorf("invalid PNG payload: %w", err)
	}
	if bounds := decoded.Bounds(); bounds.Dx() != width || bounds.Dy() != height {
		return errors.New("decoded PNG dimensions do not match IHDR")
	}
	return nil
}

func validateExportAssetSet(items []StoredItem, assets []ExportAsset) error {
	images := make(map[int64]StoredItem)
	for _, item := range items {
		if item.RecordType == domain.HistoryRecordContent && item.Format == domain.FormatImageFile {
			images[item.ID] = item
		}
	}
	seen := make(map[int64]struct{}, len(assets))
	for _, asset := range assets {
		if err := validateExportAssetData(asset); err != nil {
			return fmt.Errorf("asset for item %d: %w", asset.ItemID, err)
		}
		if _, duplicate := seen[asset.ItemID]; duplicate {
			return fmt.Errorf("duplicate asset for item %d", asset.ItemID)
		}
		item, ok := images[asset.ItemID]
		if !ok {
			return fmt.Errorf("asset references non-image item %d", asset.ItemID)
		}
		if err := validateExportAssetMatchesItem(asset, item); err != nil {
			return fmt.Errorf("asset metadata does not match image item %d", asset.ItemID)
		}
		seen[asset.ItemID] = struct{}{}
	}
	for itemID := range images {
		if _, ok := seen[itemID]; !ok {
			return fmt.Errorf("managed image item %d is missing its asset", itemID)
		}
	}
	return nil
}

func validateExportAssetMatchesItem(asset ExportAsset, item StoredItem) error {
	if item.RecordType != domain.HistoryRecordContent || item.Format != domain.FormatImageFile ||
		item.ContentSHA256 == nil || asset.ItemID != item.ID ||
		*item.ContentSHA256 != asset.SHA256 || item.ByteLen != asset.ByteLen {
		return errors.New("asset metadata does not match managed image item")
	}
	return nil
}

type preparedManagedImages struct {
	storedPaths map[int64]string
	fullPaths   []string
}

func (prepared *preparedManagedImages) cleanup() error {
	var cleanupErrors []error
	for _, full := range prepared.fullPaths {
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func (s *Store) prepareExportAssets(items []StoredItem, assets []ExportAsset) (*preparedManagedImages, error) {
	if err := validateExportAssetSet(items, assets); err != nil {
		return nil, err
	}
	itemsByID := make(map[int64]StoredItem, len(items))
	for _, item := range items {
		itemsByID[item.ID] = item
	}
	prepared := &preparedManagedImages{storedPaths: make(map[int64]string, len(assets))}
	for _, asset := range assets {
		stored, full, err := s.writeImportedImage(itemsByID[asset.ItemID], asset.SHA256, bytes.NewReader(asset.Data))
		if err != nil {
			_ = prepared.cleanup()
			return nil, err
		}
		prepared.storedPaths[asset.ItemID] = stored
		prepared.fullPaths = append(prepared.fullPaths, full)
	}
	return prepared, nil
}

type stagedImageAsset struct {
	ExportAsset
	fullPath string
}

func validateStagedAssetSet(images map[int64]StoredItem, assets map[int64]stagedImageAsset) error {
	for itemID, asset := range assets {
		item, ok := images[itemID]
		if !ok {
			return fmt.Errorf("asset references non-image item %d", itemID)
		}
		if err := validateExportAssetMatchesItem(asset.ExportAsset, item); err != nil {
			return fmt.Errorf("asset metadata does not match image item %d", itemID)
		}
	}
	for itemID := range images {
		if _, ok := assets[itemID]; !ok {
			return fmt.Errorf("managed image item %d is missing its asset", itemID)
		}
	}
	return nil
}

func (s *Store) prepareStagedAssets(images map[int64]StoredItem, assets map[int64]stagedImageAsset) (*preparedManagedImages, error) {
	if err := validateStagedAssetSet(images, assets); err != nil {
		return nil, err
	}
	prepared := &preparedManagedImages{storedPaths: make(map[int64]string, len(assets))}
	for itemID, asset := range assets {
		file, err := os.Open(asset.fullPath)
		if err != nil {
			_ = prepared.cleanup()
			return nil, fmt.Errorf("open staged image asset for item %d: %w", itemID, err)
		}
		stored, full, writeErr := s.writeImportedImage(images[itemID], asset.SHA256, file)
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			if full != "" {
				_ = os.Remove(full)
			}
			_ = prepared.cleanup()
			return nil, errors.Join(writeErr, closeErr)
		}
		prepared.storedPaths[itemID] = stored
		prepared.fullPaths = append(prepared.fullPaths, full)
	}
	return prepared, nil
}

func (s *Store) writeImportedImage(item StoredItem, contentHash string, src io.Reader) (string, string, error) {
	createdAt := item.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	year, month := createdAt.Format("2006"), createdAt.Format("01")
	dir := filepath.Join(s.dataDir, managedImageDirectoryName, year, month)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("create imported image directory: %w", err)
	}
	hashPrefix := contentHash
	if len(hashPrefix) > 8 {
		hashPrefix = hashPrefix[:8]
	}
	file, err := os.CreateTemp(dir, fmt.Sprintf("%d_%s_import_*.png", createdAt.UnixMilli(), hashPrefix))
	if err != nil {
		return "", "", fmt.Errorf("create imported image: %w", err)
	}
	full := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(full)
		}
	}()
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hasher), src)
	if err != nil {
		return "", "", fmt.Errorf("write imported image: %w", err)
	}
	if written != int64(item.ByteLen) {
		return "", "", fmt.Errorf("imported image length mismatch for item %d", item.ID)
	}
	if hex.EncodeToString(hasher.Sum(nil)) != contentHash {
		return "", "", fmt.Errorf("imported image hash mismatch for item %d", item.ID)
	}
	if err := file.Sync(); err != nil {
		return "", "", fmt.Errorf("sync imported image: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", "", fmt.Errorf("close imported image: %w", err)
	}
	keep = true
	return buildManagedImageStoredPath(year, month, filepath.Base(full)), full, nil
}

func rewriteImportedImageItem(item StoredItem, storedPaths map[int64]string) (StoredItem, error) {
	if item.RecordType != domain.HistoryRecordContent || item.Format != domain.FormatImageFile {
		return item, nil
	}
	storedPath, ok := storedPaths[item.ID]
	if !ok {
		return StoredItem{}, fmt.Errorf("managed image item %d has no prepared asset", item.ID)
	}
	item.Text = storedPath
	var meta map[string]json.RawMessage
	if err := json.Unmarshal([]byte(item.MetaJSON), &meta); err != nil {
		return StoredItem{}, fmt.Errorf("parse image metadata for item %d: %w", item.ID, err)
	}
	if meta == nil {
		meta = make(map[string]json.RawMessage)
	}
	pathJSON, _ := json.Marshal(storedPath)
	meta["imagePath"] = pathJSON
	encoded, err := json.Marshal(meta)
	if err != nil {
		return StoredItem{}, fmt.Errorf("encode image metadata for item %d: %w", item.ID, err)
	}
	item.MetaJSON = string(encoded)
	return item, nil
}

func (s *Store) removeManagedImagesIfUnreferenced(ctx context.Context, storedPaths []string) error {
	seen := make(map[string]struct{}, len(storedPaths))
	var cleanupErrors []error
	for _, storedPath := range storedPaths {
		if _, duplicate := seen[storedPath]; duplicate {
			continue
		}
		seen[storedPath] = struct{}{}
		var references int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clipboard_items
			WHERE record_type = ? AND format = ? AND text = ?`,
			domain.HistoryRecordContent, domain.FormatImageFile, storedPath).Scan(&references); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("check managed image references: %w", err))
			continue
		}
		if references != 0 {
			continue
		}
		full, err := resolveManagedImagePath(Environment{DataDir: s.dataDir}, storedPath)
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove managed image: %w", err))
		}
	}
	return errors.Join(cleanupErrors...)
}
