package clipvault

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

func compositeInlineImageURL(source string) string {
	data, err := readCompositeImage(source, defaultImageMaxBytes)
	if err != nil {
		return ""
	}
	media, err := compositeImageMediaType(data)
	if err != nil {
		return ""
	}
	return "data:" + media + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// Validate raster resources without transcoding: GIF animation and JPEG bytes
// belong to the source and must not be replaced with a derived PNG preview.
func compositeImageMediaType(raw []byte) (string, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	if err = validateDecodedImageDimensions(cfg.Width, cfg.Height); err != nil {
		return "", err
	}
	media := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[format]
	if media == "" {
		return "", errors.New("unsupported image resource")
	}
	if _, _, err = image.Decode(bytes.NewReader(raw)); err != nil {
		return "", err
	}
	return media, nil
}

func compositeImageExtension(media string) string {
	switch media {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	}
	return ""
}

// URL interpretation is derived only; the original attribute remains untouched.
func trimCompositeReference(source string) string {
	return strings.Trim(source, " \t\r\n\f")
}

func isCompositeInlineReference(source string) bool {
	source = trimCompositeReference(source)
	return len(source) >= 5 && strings.EqualFold(source[:5], "data:")
}
