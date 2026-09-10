package clipvault

import "fmt"

// maxDecodedImagePixels bounds every image decode path, including clipboard
// capture on Windows and portable backup imports.
const maxDecodedImagePixels = 40_000_000

// Bound decoded memory by pixel count, allowing narrow panoramas and long screenshots.
func validateDecodedImageDimensions(width, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid image dimensions %dx%d", width, height)
	}
	// Division also avoids overflow on untrusted PNG header dimensions.
	if width > maxDecodedImagePixels/height {
		return ErrImageLimitExceeded
	}
	return nil
}
