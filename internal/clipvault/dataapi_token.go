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

// 外部访问令牌通道：HTTP 数据接口与 MCP 各自只认本通道签发的 Token。
const (
	TokenChannelHTTP = "http"
	TokenChannelMCP  = "mcp"
)

// validTokenChannel 报告 channel 是否是受支持的外部访问通道。
func validTokenChannel(channel string) bool {
	return channel == TokenChannelHTTP || channel == TokenChannelMCP
}

var (
	errAPITokenNotFound = errors.New("api token not found")
	errAPITokenRevoked  = errors.New("api token revoked")
	errAPITokenExpired  = errors.New("api token expired")
	errAPITokenChannel  = errors.New("api token channel mismatch")
)

// APIToken 是 HTTP 数据接口与 MCP 共享的外部访问令牌元数据。
// 原始 Token 只在创建时返回一次，数据库只保存 SHA-256 哈希（AUTH-02）。
// Channel 限定令牌可用于的服务（http|mcp）；AllowSensitive 控制敏感数据
// 可见性：新令牌默认关闭，创建后可在令牌列表中单独调整。
type APIToken struct {
	ID      int64  `json:"-"`
	Name    string `json:"name"`
	Channel string `json:"channel"` // "http" | "mcp"
	// AllowSensitive 运行时权限只认此字段（ExternalReadService 强制执行）。
	AllowSensitive bool       `json:"allowSensitive"`
	CreatedAt      time.Time  `json:"createdAt"`
	ExpiresAt      *time.Time `json:"expiresAt"`  // null = 永不过期
	LastUsedAt     *time.Time `json:"lastUsedAt"` // null = 从未使用
	RevokedAt      *time.Time `json:"revokedAt"`  // null = 未撤销
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

// apiTokenExpiryMS 把创建/重新生成 Token 的有效期预设换算为过期毫秒时间戳；
// 永不过期返回 0。预设必须显式给出，空值报错。
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
		// 永不过期必须显式指定；空值（漏传字段）视为无效输入，
		// 避免客户端疏忽时静默签出永不过期 Token（权限意外放大）。
		return 0, nil
	default:
		return 0, fmt.Errorf("unsupported expiry preset %q", preset)
	}
	return now.Add(d).UnixMilli(), nil
}

// tokenExpiryPreset 按令牌的总有效期匹配最近的预设档（重新生成时用）。
// created_at 与 expires_at 由两次取时分别产生，存在毫秒级偏差，精确相等
// 比较会把 30d 令牌错配到兜底档，因此映射到最近的预设档；永不过期返回 "never"。
func tokenExpiryPreset(createdAt time.Time, expiresAt *time.Time) string {
	if expiresAt == nil {
		return "never"
	}
	diff := expiresAt.Sub(createdAt)
	presets := []struct {
		name string
		d    time.Duration
	}{
		{"1d", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"90d", 90 * 24 * time.Hour},
		{"1y", 365 * 24 * time.Hour},
	}
	best, bestDelta := presets[0].name, absDuration(diff-presets[0].d)
	for _, preset := range presets[1:] {
		if delta := absDuration(diff - preset.d); delta < bestDelta {
			best, bestDelta = preset.name, delta
		}
	}
	return best
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
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
