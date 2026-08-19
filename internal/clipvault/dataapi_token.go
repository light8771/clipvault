package clipvault

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// apiTokenPrefix 是 Access Token 的固定前缀，便于用户识别与日志脱敏。
const apiTokenPrefix = "cv_"

// apiTokenEntropyBytes 是 Token 的随机熵（256 bit），base64url 编码后 43 字符。
const apiTokenEntropyBytes = 32

var (
	errAPITokenNotFound = errors.New("api token not found")
	errAPITokenRevoked  = errors.New("api token revoked")
	errAPITokenExpired  = errors.New("api token expired")
)

// APIToken 是一个数据接口 Access Token 的元数据。原始 Token 只在创建时
// 返回一次，数据库只保存 SHA-256 哈希（AUTH-02）。
type APIToken struct {
	ID         int64      `json:"-"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`  // null = 永不过期
	LastUsedAt *time.Time `json:"lastUsedAt"` // null = 从未使用
	RevokedAt  *time.Time `json:"revokedAt"`  // null = 未撤销
}

// generateAPIToken 生成加密安全的随机 Token，返回原文（仅此一次可见）与存储哈希。
func generateAPIToken() (string, []byte, error) {
	raw := make([]byte, apiTokenEntropyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("generate api token: %w", err)
	}
	token := apiTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, hashAPIToken(token), nil
}

// hashAPIToken 计算 Token 的 SHA-256 存储哈希。
func hashAPIToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// apiTokenExpiryPreset 是创建/重新生成 Token 时的有效期预设。
func apiTokenExpiryMS(preset string, now time.Time) (int64, error) {
	var d time.Duration
	switch preset {
	case "1d":
		d = 24 * time.Hour
	case "7d":
		d = 7 * 24 * time.Hour
	case "30d":
		d = 30 * 24 * time.Hour
	case "90d":
		d = 90 * 24 * time.Hour
	case "1y":
		d = 365 * 24 * time.Hour
	case "never":
		return 0, nil
	default:
		return 0, fmt.Errorf("unsupported expiry preset %q", preset)
	}
	return now.Add(d).UnixMilli(), nil
}

// validateAPITokenName 收敛并校验 Token 名称。
func validateAPITokenName(name string) (string, error) {
	name = truncateRunes(strings.TrimSpace(name), 60)
	if name == "" {
		return "", errors.New("token name cannot be empty")
	}
	return name, nil
}

// constantTimeHashEqual 以恒定时间比较两个十六进制哈希字符串。
func constantTimeHashEqual(storedHex string, candidate []byte) bool {
	stored, err := hex.DecodeString(storedHex)
	if err != nil || len(stored) != len(candidate) {
		return false
	}
	return subtle.ConstantTimeCompare(stored, candidate) == 1
}
