package clipvault

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

const (
	managedImageDirectoryName = "images"
	managedImagePathPrefix    = managedImageDirectoryName + "/"
)

func buildManagedImageStoredPath(year, month, name string) string {
	return path.Join(managedImageDirectoryName, year, month, name)
}

func managedImageRelativePath(storedPath string) (string, error) {
	if !strings.HasPrefix(storedPath, managedImagePathPrefix) {
		return "", errors.New("managed image path must use images/ prefix")
	}
	relativeSlash := storedPath[len(managedImagePathPrefix):]
	relative := filepath.FromSlash(relativeSlash)
	if relative == "" || strings.Contains(relativeSlash, `\`) || !filepath.IsLocal(relative) || filepath.ToSlash(filepath.Clean(relative)) != relativeSlash {
		return "", errors.New("managed image path must be canonical and local")
	}
	return relative, nil
}

func validateManagedImageStoredPath(storedPath string) error {
	_, err := managedImageRelativePath(storedPath)
	return err
}

// resolveManagedImagePath implements the single storage contract for managed
// clipboard images: the database stores a canonical slash-separated path rooted
// at images/, while consumers receive a validated absolute local path.
func resolveManagedImagePath(env Environment, storedPath string) (string, error) {
	relative, err := managedImageRelativePath(storedPath)
	if err != nil {
		return "", err
	}
	imagesRoot := filepath.Clean(env.ImagesDirectory())
	full := filepath.Clean(filepath.Join(imagesRoot, relative))
	if !pathContains(imagesRoot, full) || !pathContains(canonicalPath(imagesRoot), canonicalPath(full)) {
		return "", errors.New("managed image path escapes images directory")
	}
	return full, nil
}
