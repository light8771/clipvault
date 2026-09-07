package clipvault

// maxDecodedImagePixels bounds every image decode path, including clipboard
// capture on Windows and portable backup imports.
const maxDecodedImagePixels = 40_000_000
