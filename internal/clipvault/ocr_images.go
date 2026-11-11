package clipvault

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"clipboard_ext/internal/clipvault/domain"
	"clipboard_ext/internal/clipvault/ocr"
)

type ocrImage struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	number int
	data   []byte
	path   string
}

func itemOCRImages(item StoredItem, env Environment) ([]ocrImage, error) {
	images := []ocrImage{}
	if item.Format == domain.FormatImageFile {
		path, err := resolveManagedImagePath(env, item.Text)
		if err != nil {
			return nil, errors.New("ocr.error.image_path")
		}
		images = append(images, ocrImage{ID: "image", Name: "ocr.image_clipboard", path: path})
	}
	appendFiles := func(paths []string) {
		for index, path := range paths {
			if fileExtKind(path) == domain.KindImage {
				images = append(images, ocrImage{ID: "file:" + strconv.Itoa(index), Name: "ocr.image_file", number: index + 1, path: path})
			}
		}
	}
	if item.Format == domain.FormatFileDrop {
		appendFiles(splitNonEmptyLines(item.Text))
	}
	if item.Content != nil {
		for index, repr := range item.Content.Representations {
			if repr.Format == compositePNG {
				images = append(images, ocrImage{ID: "representation:" + strconv.Itoa(index), Name: "ocr.image_clipboard", data: repr.Data})
			}
			if repr.Format == compositeFiles {
				var paths []string
				if json.Unmarshal(repr.Data, &paths) != nil {
					return nil, errors.New("ocr.error.image_file_list")
				}
				appendFiles(paths)
			}
		}
		for _, asset := range item.Content.Assets {
			if strings.HasPrefix(asset.MediaType, "image/") {
				images = append(images, ocrImage{ID: "asset:" + asset.ID, Name: "ocr.image_embedded", number: len(images) + 1, data: asset.Data})
			}
		}
	}
	return images, nil
}

func readOCRImage(selected ocrImage) ([]byte, error) {
	if selected.path == "" {
		return selected.data, nil
	}
	f, err := os.Open(selected.path)
	if err != nil {
		return nil, errors.New("ocr.error.image_unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > ocr.MaxImageBytes {
		return nil, errors.New("ocr.error.image_regular")
	}
	raw, err := io.ReadAll(io.LimitReader(f, ocr.MaxImageBytes+1))
	if err != nil || len(raw) > ocr.MaxImageBytes {
		return nil, errors.New("ocr.error.image_read")
	}
	return raw, nil
}
