package clipvault

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// .cvexport is a streaming, chunk-authenticated envelope:
//
//	header = magic[8] + version[1] + reserved[3] + salt[32]
//	       + nonce_prefix[8] + kdf_N/r/p[12] + chunk_size[4]
//	frame  = type[1] + ciphertext_len[4] + AES-256-GCM ciphertext
//
// JSON is gzip-compressed before encryption. Every compressed chunk is sealed
// independently and an authenticated final frame terminates the file. This
// keeps memory bounded and detects truncation, reordering, tampering, and
// trailing data. There is deliberately no reader for the unpublished former
// whole-buffer format.
const (
	cvexportMagic                 = "CVEXP002"
	cvexportVersion          byte = 1
	cvexportMagicLen              = len(cvexportMagic)
	cvexportNoncePrefixSize       = 8
	cvexportChunkSize             = 64 << 10
	cvexportHeaderSize            = cvexportMagicLen + 1 + 3 + saltLen + cvexportNoncePrefixSize + 12 + 4
	cvexportFrameHeaderSize       = 5
	cvexportFrameData        byte = 0
	cvexportFrameFinal       byte = 1
	maxCVExportPlaintextSize      = 500 << 20
)

var errCVExportPlaintextTooLarge = errors.New("cvexport plaintext is too large")

// WriteCVExport writes one complete encrypted export. writePlain must write the
// JSON document to the supplied writer and return only after it is complete.
func WriteCVExport(dst io.Writer, password string, writePlain func(io.Writer) error) error {
	if password == "" {
		return errors.New("export password is required")
	}
	enc, err := newCVExportEncryptWriter(dst, password)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(enc)
	writeErr := writePlain(gz)
	gzipErr := gz.Close()
	closeErr := enc.Close()
	if writeErr != nil {
		return writeErr
	}
	if gzipErr != nil {
		return fmt.Errorf("gzip close: %w", gzipErr)
	}
	if closeErr != nil {
		return closeErr
	}
	return nil
}

// DecryptCVExport streams a .cvexport reader into plaintext JSON.
func DecryptCVExport(dst io.Writer, src io.Reader, password string) error {
	if password == "" {
		return errors.New("export password is required")
	}
	dec, err := newCVExportDecryptReader(src, password)
	if err != nil {
		return err
	}
	gz, err := gzip.NewReader(dec)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	lw := &cvexportLimitWriter{dst: dst, remaining: maxCVExportPlaintextSize}
	_, copyErr := io.Copy(lw, gz)
	closeErr := gz.Close()
	if copyErr != nil {
		if errors.Is(copyErr, errCVExportPlaintextTooLarge) {
			return fmt.Errorf("cvexport plaintext exceeds %d bytes", maxCVExportPlaintextSize)
		}
		return fmt.Errorf("gzip read: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("gzip close: %w", closeErr)
	}
	return nil
}

// EncryptExport and DecryptExport are convenience helpers for small in-memory
// callers and tests. Production export/import paths use the streaming APIs.
func EncryptExport(data []byte, password string) ([]byte, error) {
	var out bytes.Buffer
	err := WriteCVExport(&out, password, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func DecryptExport(data []byte, password string) ([]byte, error) {
	var out bytes.Buffer
	if err := DecryptCVExport(&out, bytes.NewReader(data), password); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func IsCVExport(data []byte) bool {
	return len(data) >= cvexportMagicLen && string(data[:cvexportMagicLen]) == cvexportMagic
}

type cvexportEncryptWriter struct {
	dst         io.Writer
	aead        cipher.AEAD
	header      []byte
	noncePrefix [cvexportNoncePrefixSize]byte
	chunk       []byte
	counter     uint32
	closed      bool
}

func newCVExportEncryptWriter(dst io.Writer, password string) (*cvexportEncryptWriter, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	key, err := deriveWrappingKey([]byte(password), salt)
	if err != nil {
		return nil, fmt.Errorf("derive export key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create export cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create export GCM: %w", err)
	}
	w := &cvexportEncryptWriter{dst: dst, aead: aead, chunk: make([]byte, 0, cvexportChunkSize)}
	if _, err := rand.Read(w.noncePrefix[:]); err != nil {
		return nil, fmt.Errorf("generate nonce prefix: %w", err)
	}
	w.header = makeCVExportHeader(salt, w.noncePrefix[:])
	if _, err := dst.Write(w.header); err != nil {
		return nil, fmt.Errorf("write cvexport header: %w", err)
	}
	return w, nil
}

func makeCVExportHeader(salt, noncePrefix []byte) []byte {
	header := make([]byte, cvexportHeaderSize)
	copy(header, cvexportMagic)
	header[cvexportMagicLen] = cvexportVersion
	offset := cvexportMagicLen + 4
	copy(header[offset:], salt)
	offset += saltLen
	copy(header[offset:], noncePrefix)
	offset += cvexportNoncePrefixSize
	binary.LittleEndian.PutUint32(header[offset:], uint32(scryptN))
	offset += 4
	binary.LittleEndian.PutUint32(header[offset:], uint32(scryptR))
	offset += 4
	binary.LittleEndian.PutUint32(header[offset:], uint32(scryptP))
	offset += 4
	binary.LittleEndian.PutUint32(header[offset:], uint32(cvexportChunkSize))
	return header
}

func (w *cvexportEncryptWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("write to closed cvexport writer")
	}
	total := len(p)
	for len(p) > 0 {
		n := cvexportChunkSize - len(w.chunk)
		if n > len(p) {
			n = len(p)
		}
		w.chunk = append(w.chunk, p[:n]...)
		p = p[n:]
		if len(w.chunk) == cvexportChunkSize {
			if err := w.writeFrame(cvexportFrameData, w.chunk); err != nil {
				return total - len(p), err
			}
			w.chunk = w.chunk[:0]
		}
	}
	return total, nil
}

func (w *cvexportEncryptWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if len(w.chunk) > 0 {
		if err := w.writeFrame(cvexportFrameData, w.chunk); err != nil {
			return err
		}
	}
	return w.writeFrame(cvexportFrameFinal, nil)
}

func (w *cvexportEncryptWriter) writeFrame(frameType byte, plaintext []byte) error {
	nonce := makeCVExportNonce(w.noncePrefix[:], w.counter)
	aad := makeCVExportAAD(w.header, w.counter, frameType)
	ciphertext := w.aead.Seal(nil, nonce, plaintext, aad)
	var frameHeader [cvexportFrameHeaderSize]byte
	frameHeader[0] = frameType
	binary.LittleEndian.PutUint32(frameHeader[1:], uint32(len(ciphertext)))
	if _, err := w.dst.Write(frameHeader[:]); err != nil {
		return fmt.Errorf("write cvexport frame header: %w", err)
	}
	if _, err := w.dst.Write(ciphertext); err != nil {
		return fmt.Errorf("write cvexport frame: %w", err)
	}
	if w.counter == ^uint32(0) && frameType != cvexportFrameFinal {
		return errors.New("cvexport has too many chunks")
	}
	w.counter++
	return nil
}

type cvexportDecryptReader struct {
	src         io.Reader
	aead        cipher.AEAD
	header      []byte
	noncePrefix [cvexportNoncePrefixSize]byte
	chunk       []byte
	counter     uint32
	finished    bool
}

func newCVExportDecryptReader(src io.Reader, password string) (*cvexportDecryptReader, error) {
	header := make([]byte, cvexportHeaderSize)
	if _, err := io.ReadFull(src, header); err != nil {
		return nil, fmt.Errorf("read cvexport header: %w", err)
	}
	if string(header[:cvexportMagicLen]) != cvexportMagic {
		return nil, errors.New("bad magic: not a current .cvexport file")
	}
	if header[cvexportMagicLen] != cvexportVersion {
		return nil, fmt.Errorf("unsupported cvexport version %d", header[cvexportMagicLen])
	}
	if !bytes.Equal(header[cvexportMagicLen+1:cvexportMagicLen+4], []byte{0, 0, 0}) {
		return nil, errors.New("invalid cvexport reserved header")
	}
	offset := cvexportMagicLen + 4
	salt := header[offset : offset+saltLen]
	offset += saltLen
	var noncePrefix [cvexportNoncePrefixSize]byte
	copy(noncePrefix[:], header[offset:offset+cvexportNoncePrefixSize])
	offset += cvexportNoncePrefixSize
	nVal := binary.LittleEndian.Uint32(header[offset:])
	offset += 4
	rVal := binary.LittleEndian.Uint32(header[offset:])
	offset += 4
	pVal := binary.LittleEndian.Uint32(header[offset:])
	offset += 4
	chunkSize := binary.LittleEndian.Uint32(header[offset:])
	if nVal != uint32(scryptN) || rVal != uint32(scryptR) || pVal != uint32(scryptP) || chunkSize != uint32(cvexportChunkSize) {
		return nil, errors.New("unsupported cvexport parameters")
	}
	key, err := deriveWrappingKey([]byte(password), salt)
	if err != nil {
		return nil, fmt.Errorf("derive export key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create export cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create export GCM: %w", err)
	}
	return &cvexportDecryptReader{src: src, aead: aead, header: header, noncePrefix: noncePrefix}, nil
}

func (r *cvexportDecryptReader) Read(p []byte) (int, error) {
	for len(r.chunk) == 0 {
		if r.finished {
			return 0, io.EOF
		}
		if err := r.readFrame(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.chunk)
	r.chunk = r.chunk[n:]
	return n, nil
}

func (r *cvexportDecryptReader) readFrame() error {
	var frameHeader [cvexportFrameHeaderSize]byte
	if _, err := io.ReadFull(r.src, frameHeader[:]); err != nil {
		return fmt.Errorf("read cvexport frame header: %w", err)
	}
	frameType := frameHeader[0]
	ciphertextLen := binary.LittleEndian.Uint32(frameHeader[1:])
	if frameType != cvexportFrameData && frameType != cvexportFrameFinal {
		return fmt.Errorf("invalid cvexport frame type %d", frameType)
	}
	if ciphertextLen < uint32(r.aead.Overhead()) || ciphertextLen > uint32(cvexportChunkSize+r.aead.Overhead()) {
		return errors.New("invalid cvexport frame length")
	}
	if frameType == cvexportFrameData && ciphertextLen == uint32(r.aead.Overhead()) {
		return errors.New("empty cvexport data frame")
	}
	if frameType == cvexportFrameFinal && ciphertextLen != uint32(r.aead.Overhead()) {
		return errors.New("invalid cvexport final frame")
	}
	ciphertext := make([]byte, int(ciphertextLen))
	if _, err := io.ReadFull(r.src, ciphertext); err != nil {
		return fmt.Errorf("read cvexport frame: %w", err)
	}
	nonce := makeCVExportNonce(r.noncePrefix[:], r.counter)
	aad := makeCVExportAAD(r.header, r.counter, frameType)
	plaintext, err := r.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return errors.New("cvexport authentication failed")
	}
	if r.counter == ^uint32(0) && frameType != cvexportFrameFinal {
		return errors.New("cvexport has too many chunks")
	}
	r.counter++
	if frameType == cvexportFrameFinal {
		var trailing [1]byte
		if _, err := io.ReadFull(r.src, trailing[:]); err == nil {
			return errors.New("cvexport has trailing data")
		} else if !errors.Is(err, io.EOF) {
			return fmt.Errorf("check cvexport trailer: %w", err)
		}
		r.finished = true
		return nil
	}
	r.chunk = plaintext
	return nil
}

func makeCVExportNonce(prefix []byte, counter uint32) []byte {
	nonce := make([]byte, nonceLen)
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[cvexportNoncePrefixSize:], counter)
	return nonce
}

func makeCVExportAAD(header []byte, counter uint32, frameType byte) []byte {
	aad := make([]byte, len(header)+5)
	copy(aad, header)
	binary.BigEndian.PutUint32(aad[len(header):], counter)
	aad[len(aad)-1] = frameType
	return aad
}

type cvexportLimitWriter struct {
	dst       io.Writer
	remaining int64
}

func (w *cvexportLimitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errCVExportPlaintextTooLarge
	}
	n, err := w.dst.Write(p)
	w.remaining -= int64(n)
	return n, err
}
