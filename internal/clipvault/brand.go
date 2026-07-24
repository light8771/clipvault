package clipvault

import (
	"net/http"
	"strconv"
)

const (
	brandLogoAsset       = "brand-logo"
	brandFaviconAsset    = "brand-favicon"
	backgroundMediaAsset = "background-media"
)

func isBrandContentType(contentType, kind string) bool {
	switch kind {
	case "logo":
		return contentType == "image/png" || contentType == "image/jpeg"
	case "favicon":
		return contentType == "image/png" || contentType == "image/x-icon"
	default:
		return false
	}
}

func isBackgroundContentType(contentType string) bool {
	switch contentType {
	case "image/png", "image/jpeg", "video/mp4", "video/webm":
		return true
	default:
		return false
	}
}

// assetVersionMatches allows unversioned requests for the current asset and
// rejects stale versioned URLs so immutable browser caches remain coherent.
func assetVersionMatches(r *http.Request, currentVersion int64) bool {
	if currentVersion <= 0 {
		return false
	}
	requested := r.URL.Query().Get("v")
	if requested == "" {
		return true
	}
	version, err := strconv.ParseInt(requested, 10, 64)
	return err == nil && version > 0 && version == currentVersion
}

func assetCacheControl(r *http.Request) string {
	if r.URL.Query().Get("v") == "" {
		return "private, no-cache"
	}
	return "private, max-age=31536000, immutable"
}

func assetFileName(kind string, version int64) string {
	return kind + "-" + strconv.FormatInt(version, 10)
}
