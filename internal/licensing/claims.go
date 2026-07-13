package licensing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	tokenPrefix       = "CV1"
	signatureDomain   = "clipvault-license-v1\x00"
	maxTokenLength    = 4096
	currentVersion    = 1
	productName       = "clipvault"
	perpetualEdition  = "perpetual"
	machineIDHexBytes = 32
)

var (
	licenseIDPattern = regexp.MustCompile(`^CV-[A-Z0-9][A-Z0-9-]{2,62}$`)
	keyIDPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
)

type Claims struct {
	Version   int    `json:"v"`
	Product   string `json:"product"`
	KeyID     string `json:"kid"`
	LicenseID string `json:"license_id"`
	MachineID string `json:"machine_id"`
	Edition   string `json:"edition"`
}

func (c Claims) Validate() error {
	if c.Version != currentVersion {
		return fmt.Errorf("%w: claims version %d", ErrUnsupportedVersion, c.Version)
	}
	if c.Product != productName {
		return ErrWrongProduct
	}
	if c.Edition != perpetualEdition {
		return ErrWrongEdition
	}
	if !keyIDPattern.MatchString(c.KeyID) {
		return ErrInvalidKeyID
	}
	if !licenseIDPattern.MatchString(c.LicenseID) {
		return ErrInvalidLicenseID
	}
	if len(c.MachineID) != machineIDHexBytes*2 || c.MachineID != strings.ToLower(c.MachineID) {
		return ErrInvalidMachineID
	}
	decoded, err := hex.DecodeString(c.MachineID)
	if err != nil || len(decoded) != machineIDHexBytes {
		return ErrInvalidMachineID
	}
	return nil
}

func SignToken(claims Claims, privateKey ed25519.PrivateKey) (string, error) {
	if err := claims.Validate(); err != nil {
		return "", err
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("invalid Ed25519 private key")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}
	signature := ed25519.Sign(privateKey, append([]byte(signatureDomain), payload...))
	return tokenPrefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func ParseAndVerify(token string, trustedKeys map[string]ed25519.PublicKey) (Claims, error) {
	var claims Claims
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxTokenLength {
		return claims, ErrBadTokenFormat
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix {
		return claims, ErrBadTokenFormat
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 || len(payload) > 2048 {
		return claims, ErrBadTokenFormat
	}
	// 只接受规范的无填充 Base64URL。否则攻击者可以改动末尾未使用的
	// padding bits，让字符串看似被篡改但仍解码为同一组字节。
	if base64.RawURLEncoding.EncodeToString(payload) != parts[1] {
		return claims, ErrBadTokenFormat
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != ed25519.SignatureSize {
		return claims, ErrBadTokenFormat
	}
	if base64.RawURLEncoding.EncodeToString(signature) != parts[2] {
		return claims, ErrBadTokenFormat
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&claims); err != nil {
		return Claims{}, ErrBadTokenFormat
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Claims{}, ErrBadTokenFormat
	}
	if err := claims.Validate(); err != nil {
		return Claims{}, err
	}
	publicKey, ok := trustedKeys[claims.KeyID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return Claims{}, ErrUnknownKey
	}
	if !ed25519.Verify(publicKey, append([]byte(signatureDomain), payload...), signature) {
		return Claims{}, ErrBadSignature
	}
	return claims, nil
}

func NewPerpetualClaims(keyID, licenseID, machineID string) Claims {
	return Claims{
		Version:   currentVersion,
		Product:   productName,
		KeyID:     strings.ToLower(strings.TrimSpace(keyID)),
		LicenseID: strings.ToUpper(strings.TrimSpace(licenseID)),
		MachineID: strings.ToLower(strings.TrimSpace(machineID)),
		Edition:   perpetualEdition,
	}
}
