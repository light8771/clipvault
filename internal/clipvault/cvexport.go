package clipvault

import (
	"io"

	"clipboard_ext/internal/clipvault/transfer"
)

const cvexportMagicLen = transfer.MagicLength

func WriteCVExport(dst io.Writer, password string, writePlain func(io.Writer) error) error {
	return transfer.WriteCVExport(dst, password, writePlain)
}

func DecryptCVExport(dst io.Writer, src io.Reader, password string) error {
	return transfer.DecryptCVExport(dst, src, password)
}

func EncryptExport(data []byte, password string) ([]byte, error) {
	return transfer.EncryptExport(data, password)
}

func DecryptExport(data []byte, password string) ([]byte, error) {
	return transfer.DecryptExport(data, password)
}

func IsCVExport(data []byte) bool { return transfer.IsCVExport(data) }
