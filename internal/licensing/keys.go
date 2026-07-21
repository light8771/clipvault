package licensing

import (
	"crypto/ed25519"
	"encoding/base64"
)

// These values are injected into builds with -ldflags -X.
// The public key is not secret. The private key must never enter this repo.
var (
	builtinKeyID           = "cv-main-2026"
	builtinPublicKeyBase64 = ""
)

func BuiltinTrustedKeys() map[string]ed25519.PublicKey {
	keys := make(map[string]ed25519.PublicKey)
	decoded, err := base64.RawStdEncoding.DecodeString(builtinPublicKeyBase64)
	if err == nil && len(decoded) == ed25519.PublicKeySize && keyIDPattern.MatchString(builtinKeyID) {
		keys[builtinKeyID] = ed25519.PublicKey(decoded)
	}
	return keys
}

func BuiltinKeyID() string { return builtinKeyID }
