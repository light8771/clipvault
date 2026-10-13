package clipvault

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
)

type clipboardErrorCategoryProvider interface {
	ClipboardErrorCategory() string
}

func (formats ClipboardFormatSet) Info() []domain.ClipboardFormatInfo {
	count := max(len(formats.IDs), len(formats.Names))
	result := make([]domain.ClipboardFormatInfo, 0, count)
	for i := 0; i < count; i++ {
		var info domain.ClipboardFormatInfo
		if i < len(formats.IDs) {
			info.ID = formats.IDs[i]
		}
		if i < len(formats.Names) {
			info.Name = formats.Names[i]
		}
		result = append(result, info)
	}
	return result
}

func (formats ClipboardFormatSet) HasName(name string) bool {
	for _, candidate := range formats.Names {
		if strings.EqualFold(strings.TrimSpace(candidate), name) {
			return true
		}
	}
	return false
}

func isVirtualFileSet(formats ClipboardFormatSet) bool {
	hasDescriptor := formats.HasName("FileGroupDescriptorW") || formats.HasName("FileGroupDescriptor")
	return hasDescriptor && formats.HasName("FileContents")
}

// Explorer may advertise virtual-file formats alongside CF_HDROP for the same
// selection. A successfully captured file list is already restorable; merely
// advertising the alternative does not mean files were lost.
func hasUncapturedVirtualFiles(formats ClipboardFormatSet, primary *ClipboardSnapshot) bool {
	if !isVirtualFileSet(formats) {
		return false
	}
	return !hasCompleteFileDrop(primary)
}

func hasCompleteFileDrop(primary *ClipboardSnapshot) bool {
	if primary == nil || primary.Format != domain.FormatFileDrop || strings.TrimSpace(primary.Text) == "" {
		return false
	}
	truncated, _ := primary.Meta["truncated"].(bool)
	return !truncated
}

func classifyEmptyCapture(formats ClipboardFormatSet) domain.ClipboardEventCode {
	if len(formats.IDs) == 0 && len(formats.Names) == 0 {
		return domain.EventClipboardEmpty
	}
	if isVirtualFileSet(formats) {
		return domain.EventVirtualFile
	}
	return domain.EventUnsupportedFormat
}

func classifyCaptureError(err error) domain.ClipboardEventCode {
	var provider clipboardErrorCategoryProvider
	if errors.As(err, &provider) {
		if code := domain.ClipboardEventCode(provider.ClipboardErrorCategory()); domain.ValidEventCode(code) {
			return code
		}
	}
	return domain.EventCaptureFailed
}

func buildEventFingerprint(code domain.ClipboardEventCode, formats ClipboardFormatSet, stage, category, sourceExe string) string {
	encodedFormats, _ := json.Marshal(normalizedFingerprintFormats(formats))
	parts := []string{
		"v2",
		string(code),
		string(encodedFormats),
		strings.ToLower(strings.TrimSpace(stage)),
		strings.ToLower(strings.TrimSpace(category)),
		strings.ToLower(strings.TrimSpace(sourceExe)),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func normalizedFingerprintFormats(formats ClipboardFormatSet) []string {
	info := formats.Info()
	normalized := make([]string, 0, len(info))
	seen := make(map[string]struct{}, len(info))
	for _, format := range info {
		name := strings.ToLower(strings.TrimSpace(format.Name))
		var identity string
		switch {
		case format.ID > 0 && format.ID < 0xc000:
			// Standard clipboard format IDs are fixed by Win32.
			identity = "id:" + strconv.FormatUint(uint64(format.ID), 10)
		case name != "":
			// Registered format IDs are allocated per Windows session. Their
			// normalized names are the stable semantic identity.
			identity = "name:" + name
		case format.ID != 0:
			identity = "id:" + strconv.FormatUint(uint64(format.ID), 10)
		default:
			continue
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		normalized = append(normalized, identity)
	}
	slices.Sort(normalized)
	return normalized
}

func buildEventFingerprintFromInfo(code domain.ClipboardEventCode, formats []domain.ClipboardFormatInfo, stage, category, sourceExe string) string {
	formatSet := ClipboardFormatSet{
		IDs:   make([]uint32, len(formats)),
		Names: make([]string, len(formats)),
	}
	for index, format := range formats {
		formatSet.IDs[index] = format.ID
		formatSet.Names[index] = format.Name
	}
	return buildEventFingerprint(code, formatSet, stage, category, sourceExe)
}

func encodeClipboardFormats(formats []domain.ClipboardFormatInfo) string {
	if len(formats) == 0 {
		return "[]"
	}
	encoded, _ := json.Marshal(formats)
	return string(encoded)
}
