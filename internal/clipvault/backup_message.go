package clipvault

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"os"
	"strings"
	"time"
)

func renderEmailTemplate(cfg EmailBackupConfig, filename, format string, now time.Time) (string, string, string) {
	replacer := strings.NewReplacer(
		"{time}", now.Format("2006-01-02 15:04:05"),
		"{filename}", filename,
		"{format}", format,
	)
	subject := replacer.Replace(cfg.Subject)
	body := replacer.Replace(cfg.Body)
	return subject, body, renderEmailHTML(subject, body, cfg.Style, cfg.AccentColor)
}

func renderEmailHTML(subject, body, style, accentColor string) string {
	escapedSubject := html.EscapeString(subject)
	escapedBody := strings.ReplaceAll(html.EscapeString(normalizeEmailNewlines(body)), "\n", "<br>\n")
	switch style {
	case "plain":
		return fmt.Sprintf("<!doctype html><html><body style=\"margin:0;padding:24px;color:#1f2937;font:15px/1.7 Arial,sans-serif\"><strong style=\"font-size:18px\">%s</strong><div style=\"margin-top:16px\">%s</div></body></html>", escapedSubject, escapedBody)
	case "accent":
		return fmt.Sprintf("<!doctype html><html><body style=\"margin:0;padding:24px;background:#f3f4f6;font:15px/1.7 Arial,sans-serif\"><div style=\"max-width:640px;margin:auto;background:#ffffff;border-top:5px solid %s;padding:28px;color:#1f2937\"><strong style=\"font-size:18px;color:%s\">%s</strong><div style=\"margin-top:16px\">%s</div></div></body></html>", accentColor, accentColor, escapedSubject, escapedBody)
	case "card":
		return fmt.Sprintf("<!doctype html><html><body style=\"margin:0;padding:24px;background:#f3f4f6;font:15px/1.7 Arial,sans-serif\"><div style=\"max-width:640px;margin:auto;background:#ffffff;border:1px solid #e5e7eb;padding:28px;color:#1f2937\"><strong style=\"font-size:18px\">%s</strong><div style=\"width:36px;height:3px;margin:14px 0;background:%s\"></div><div>%s</div></div></body></html>", escapedSubject, accentColor, escapedBody)
	default:
		return ""
	}
}

func normalizeEmailNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

type emailAttachment struct {
	filename    string
	contentType string
	data        []byte
}

type emailBackupMessage struct {
	filename string
	data     []byte
}

func emailBackupFilename(ext string, now time.Time, index, total int) string {
	filename := fmt.Sprintf("clipvault-backup-%s.%s", now.Format("20060102-150405"), ext)
	if total > 1 {
		filename = fmt.Sprintf("clipvault-backup-%s-%dof%d.%s", now.Format("20060102-150405"), index+1, total, ext)
	}
	return filename
}

func buildEmailBackupMessage(cfg EmailBackupConfig, ext string, now time.Time, index, total int, attachment []byte) emailBackupMessage {
	filename := emailBackupFilename(ext, now, index, total)
	subject, body, htmlBody := renderEmailTemplate(cfg, filename, ext, now)
	if total > 1 {
		subject = fmt.Sprintf("[%d/%d] %s", index+1, total, subject)
	}
	contentType := "application/json"
	switch ext {
	case "cvexport":
		contentType = "application/octet-stream"
	case "xlsx":
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	return emailBackupMessage{
		filename: filename,
		data:     buildMIMEWithAttachment(cfg.From, cfg.To, subject, body, htmlBody, filename, contentType, attachment),
	}
}

func emailBackupFileMessageSize(cfg EmailBackupConfig, ext string, now time.Time, index, total int, attachmentPath string) (int64, error) {
	var counter byteCounter
	if err := writeEmailBackupMessage(&counter, cfg, ext, now, index, total, attachmentPath); err != nil {
		return 0, err
	}
	return counter.n, nil
}

func writeEmailBackupMessage(dst io.Writer, cfg EmailBackupConfig, ext string, now time.Time, index, total int, attachmentPath string) error {
	filename := emailBackupFilename(ext, now, index, total)
	subject, body, htmlBody := renderEmailTemplate(cfg, filename, ext, now)
	if total > 1 {
		subject = fmt.Sprintf("[%d/%d] %s", index+1, total, subject)
	}
	contentType := "application/json"
	switch ext {
	case "cvexport":
		contentType = "application/octet-stream"
	case "xlsx":
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	attachment, err := os.Open(attachmentPath)
	if err != nil {
		return fmt.Errorf("%s: %w", templateT("backup.err_open_part", nil), err)
	}
	defer attachment.Close()
	return writeMIMEAttachmentStream(dst, cfg.From, cfg.To, subject, body, htmlBody, filename, contentType, attachment)
}

func writeMIMEAttachmentStream(dst io.Writer, from, to, subject, body, htmlBody, filename, contentType string, attachment io.Reader) error {
	mixed := multipart.NewWriter(dst)
	if _, err := fmt.Fprintf(dst, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: %s\r\n\r\n",
		from, to, mime.QEncoding.Encode("UTF-8", subject), mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()})); err != nil {
		return err
	}
	alternativeBody, boundary := buildAlternativeBody(body, htmlBody)
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": boundary}))
	part, err := mixed.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(alternativeBody); err != nil {
		return err
	}
	header = textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "base64")
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	part, err = mixed.CreatePart(header)
	if err != nil {
		return err
	}
	lineWriter := &streamBase64LineWriter{dst: part}
	encoder := base64.NewEncoder(base64.StdEncoding, lineWriter)
	if _, err := io.Copy(encoder, attachment); err != nil {
		_ = encoder.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	if err := lineWriter.Flush(); err != nil {
		return err
	}
	return mixed.Close()
}

type byteCounter struct{ n int64 }

func (w *byteCounter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

type streamBase64LineWriter struct {
	dst  io.Writer
	line []byte
}

func (w *streamBase64LineWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		space := base64MIMELineWidth - len(w.line)
		if space > len(p) {
			space = len(p)
		}
		w.line = append(w.line, p[:space]...)
		p = p[space:]
		if len(w.line) == base64MIMELineWidth {
			if err := w.flushLine(); err != nil {
				return total - len(p), err
			}
		}
	}
	return total, nil
}

func (w *streamBase64LineWriter) Flush() error {
	if len(w.line) == 0 {
		return nil
	}
	return w.flushLine()
}

func (w *streamBase64LineWriter) flushLine() error {
	if _, err := w.dst.Write(w.line); err != nil {
		return err
	}
	if _, err := io.WriteString(w.dst, "\r\n"); err != nil {
		return err
	}
	w.line = w.line[:0]
	return nil
}

func emailBackupMessageSize(cfg EmailBackupConfig, ext string, attachment []byte) int {

	message := buildEmailBackupMessage(cfg, ext, time.Unix(0, 0), maxEmailParts-1, maxEmailParts, attachment)
	return len(message.data)
}

func buildTemplateMIME(from, to, subject, body, htmlBody string) []byte {
	return buildMIME(from, to, subject, body, htmlBody, nil)
}

func buildMIMEWithAttachment(from, to, subject, body, htmlBody, filename, contentType string, attachment []byte) []byte {
	return buildMIME(from, to, subject, body, htmlBody, &emailAttachment{
		filename: filename, contentType: contentType, data: attachment,
	})
}

func buildMIME(from, to, subject, body, htmlBody string, attachment *emailAttachment) []byte {
	var buf bytes.Buffer
	if attachment == nil {
		alternativeBody, boundary := buildAlternativeBody(body, htmlBody)
		writeMIMEHeaders(&buf, from, to, subject, mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": boundary}))
		buf.Write(alternativeBody)
		return buf.Bytes()
	}

	var mixedBody bytes.Buffer
	mixed := multipart.NewWriter(&mixedBody)
	writeAlternativePart(mixed, body, htmlBody)
	writeAttachmentPart(mixed, *attachment)
	_ = mixed.Close()
	writeMIMEHeaders(&buf, from, to, subject, mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mixed.Boundary()}))
	buf.Write(mixedBody.Bytes())
	return buf.Bytes()
}

func writeMIMEHeaders(buf *bytes.Buffer, from, to, subject, contentType string) {
	fmt.Fprintf(buf, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: %s\r\n\r\n",
		from, to, mime.QEncoding.Encode("UTF-8", subject), contentType)
}

func buildAlternativeBody(body, htmlBody string) ([]byte, string) {
	var buf bytes.Buffer
	alternative := multipart.NewWriter(&buf)
	writeTextPart(alternative, "text/plain; charset=UTF-8", body)
	writeTextPart(alternative, "text/html; charset=UTF-8", htmlBody)
	_ = alternative.Close()
	return buf.Bytes(), alternative.Boundary()
}

func writeAlternativePart(parent *multipart.Writer, body, htmlBody string) {
	alternativeBody, boundary := buildAlternativeBody(body, htmlBody)
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": boundary}))
	part, _ := parent.CreatePart(header)
	_, _ = part.Write(alternativeBody)
}

func writeTextPart(parent *multipart.Writer, contentType, content string) {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, _ := parent.CreatePart(header)
	encoded := quotedprintable.NewWriter(part)
	_, _ = encoded.Write([]byte(normalizeEmailNewlines(content)))
	_ = encoded.Close()
}

func writeAttachmentPart(parent *multipart.Writer, attachment emailAttachment) {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", attachment.contentType)
	header.Set("Content-Transfer-Encoding", "base64")
	header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.filename}))
	part, _ := parent.CreatePart(header)

	// 流式 base64 编码 + 定宽折行，替代「先整体 EncodeToString 再逐行字符串拼接」。
	// 输出与旧实现逐字节等价：每满 base64MIMELineWidth 个字符输出一次 CRLF，
	// 末尾不足一行的残余同样以 CRLF 收尾；attachment.data 为空时不产生任何输出。
	var out bytes.Buffer
	lw := &base64LineWriter{buf: &out}
	encoder := base64.NewEncoder(base64.StdEncoding, lw)
	_, _ = encoder.Write(attachment.data)
	_ = encoder.Close()
	lw.flushLine()
	_, _ = part.Write(out.Bytes())
}

// base64MIMELineWidth 是 base64 附件正文的折行宽度（RFC 2045 建议 76 列）。
const base64MIMELineWidth = 76

// base64LineWriter 接收 base64 编码流，按 base64MIMELineWidth 折行写入缓冲区。
// 写入目标是 *bytes.Buffer（其 Write 不会失败），因此内部无需处理错误。
type base64LineWriter struct {
	buf *bytes.Buffer
	col int
}

func (lw *base64LineWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		n := base64MIMELineWidth - lw.col
		if n > len(p) {
			n = len(p)
		}
		lw.buf.Write(p[:n])
		lw.col += n
		p = p[n:]
		if lw.col == base64MIMELineWidth {
			lw.buf.WriteString("\r\n")
			lw.col = 0
		}
	}
	return total, nil
}

// flushLine 为不足整行的残余补上换行，与旧实现末行也带 CRLF 的行为保持一致。
func (lw *base64LineWriter) flushLine() {
	if lw.col > 0 {
		lw.buf.WriteString("\r\n")
		lw.col = 0
	}
}
