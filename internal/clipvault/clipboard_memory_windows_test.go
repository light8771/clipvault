//go:build windows

package clipvault

import (
	"errors"
	"testing"
)

func TestClipboardPayloadSizeIsRejectedBeforeCopy(t *testing.T) {
	limitErr := &clipboardTextPayloadLimitError{Limit: maxClipboardTextPayloadBytes}
	if err := checkClipboardPayloadSize(maxClipboardTextPayloadBytes, maxClipboardTextPayloadBytes, limitErr); err != nil {
		t.Fatalf("payload at limit was rejected: %v", err)
	}
	err := checkClipboardPayloadSize(maxClipboardTextPayloadBytes+1, maxClipboardTextPayloadBytes, limitErr)
	if !errors.Is(err, ErrTextLimitExceeded) {
		t.Fatalf("oversized payload error = %v", err)
	}
	var payloadErr *clipboardTextPayloadLimitError
	if !errors.As(err, &payloadErr) || payloadErr.ByteLen != maxClipboardTextPayloadBytes+1 || payloadErr.Limit != maxClipboardTextPayloadBytes {
		t.Fatalf("oversized payload details = %#v", payloadErr)
	}
}

func TestDecodeANSIClipboardStringUsesRequestedCodePage(t *testing.T) {
	raw := append([]byte("\u4e2d\u6587"), 0)
	decoded, err := decodeANSIClipboardStringWithCodePage(raw, 65001)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "\u4e2d\u6587" {
		t.Fatalf("UTF-8 code-page decode = %q", decoded)
	}
}
