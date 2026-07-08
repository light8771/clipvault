package clipvault

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookieName = "cv_session"
	sessionTokenBytes = 32
	// 内部会话最长存活时间，用于清理过期会话
	maxSessionLifetime = 24 * time.Hour
)

// Session 表示一个已认证的浏览器会话。
type Session struct {
	Token           string
	Authenticated   bool
	SecondaryAuth   bool
	SecondaryExpiry time.Time
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

// AuthManager 管理内存中的会话存储和密码验证。
type AuthManager struct {
	mu              sync.RWMutex
	sessions        map[string]*Session
	secondaryTokens map[string]string // 一次性二次认证 token -> session token
}

// NewAuthManager 创建一个新的 AuthManager。
func NewAuthManager() *AuthManager {
	return &AuthManager{
		sessions:        make(map[string]*Session),
		secondaryTokens: make(map[string]string),
	}
}

// hashPassword 使用 SHA-256(salt + password) 计算密码哈希。
func hashPassword(salt, password string) string {
	h := sha256.Sum256([]byte(salt + password))
	return hex.EncodeToString(h[:])
}

// generateSalt 生成一个随机的 16 字节盐值。
func generateSalt() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// generateToken 生成一个随机的会话令牌。
func generateToken() (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// VerifyLockPassword 验证锁屏密码。
func (am *AuthManager) VerifyLockPassword(cfg Config, password string) bool {
	if cfg.Security.LockPasswordHash == "" {
		return true
	}
	return hashPassword(cfg.Security.PasswordSalt, password) == cfg.Security.LockPasswordHash
}

// VerifySecondaryPassword 验证二次密码。
func (am *AuthManager) VerifySecondaryPassword(cfg Config, password string) bool {
	if cfg.Security.SecondaryPasswordHash == "" {
		return true
	}
	return hashPassword(cfg.Security.PasswordSalt, password) == cfg.Security.SecondaryPasswordHash
}

// HasLockPassword 检查是否已设置锁屏密码。
func (am *AuthManager) HasLockPassword(cfg Config) bool {
	return cfg.Security.LockPasswordHash != ""
}

// HasSecondaryPassword 检查是否已设置二次密码。
func (am *AuthManager) HasSecondaryPassword(cfg Config) bool {
	return cfg.Security.SecondaryPasswordHash != ""
}

// CreateSession 创建一个新的已认证会话并返回令牌。
// timeoutMin 为会话超时分钟数：0 表示浏览器会话（内部仍按 maxSessionLifetime 管理），N 表示 N 分钟。
func (am *AuthManager) CreateSession(timeoutMin int) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	expiry := now.Add(maxSessionLifetime)
	if timeoutMin > 0 {
		expiry = now.Add(time.Duration(timeoutMin) * time.Minute)
	}
	sess := &Session{
		Token:         token,
		Authenticated: true,
		CreatedAt:     now,
		ExpiresAt:     expiry,
	}
	am.mu.Lock()
	am.sessions[token] = sess
	am.mu.Unlock()
	return token, nil
}

// GetSession 根据令牌获取会话，已过期则返回 nil。
func (am *AuthManager) GetSession(token string) *Session {
	am.mu.RLock()
	sess, ok := am.sessions[token]
	am.mu.RUnlock()
	if !ok {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		am.DestroySession(token)
		return nil
	}
	return sess
}

// GrantSecondaryAuth 为会话授予二次认证权限。
// timeoutMin 为缓存分钟数：0 表示不缓存（仅使用一次性 token），N 表示 N 分钟内免重复验证。
func (am *AuthManager) GrantSecondaryAuth(token string, timeoutMin int) {
	if timeoutMin <= 0 {
		return
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	if sess, ok := am.sessions[token]; ok {
		sess.SecondaryAuth = true
		sess.SecondaryExpiry = time.Now().Add(time.Duration(timeoutMin) * time.Minute)
	}
}

// HasSecondaryAuth 检查会话是否拥有有效的二次认证。
func (am *AuthManager) HasSecondaryAuth(token string) bool {
	am.mu.RLock()
	defer am.mu.RUnlock()
	sess, ok := am.sessions[token]
	if !ok {
		return false
	}
	if !sess.SecondaryAuth {
		return false
	}
	if time.Now().After(sess.SecondaryExpiry) {
		sess.SecondaryAuth = false
		return false
	}
	return true
}

// GenerateSecondaryToken 生成一个一次性二次认证 token，绑定到指定会话。
func (am *AuthManager) GenerateSecondaryToken(sessionToken string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}
	am.mu.Lock()
	am.secondaryTokens[token] = sessionToken
	am.mu.Unlock()
	return token, nil
}

// ConsumeSecondaryToken 消费一个一次性二次认证 token，成功返回 true。
func (am *AuthManager) ConsumeSecondaryToken(token string) bool {
	if token == "" {
		return false
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	_, ok := am.secondaryTokens[token]
	if ok {
		delete(am.secondaryTokens, token)
	}
	return ok
}

// DestroySession 销毁会话。
func (am *AuthManager) DestroySession(token string) {
	am.mu.Lock()
	delete(am.sessions, token)
	// 清理绑定到该会话的一次性 token
	for st, sess := range am.secondaryTokens {
		if sess == token {
			delete(am.secondaryTokens, st)
		}
	}
	am.mu.Unlock()
}

// CleanExpiredSessions 清理所有过期会话和一次性 token。
func (am *AuthManager) CleanExpiredSessions() {
	am.mu.Lock()
	defer am.mu.Unlock()
	now := time.Now()
	for token, sess := range am.sessions {
		if now.After(sess.ExpiresAt) {
			delete(am.sessions, token)
		}
	}
	// 一次性 token 不做定期清理，因为它们在创建后很快被消费或随会话销毁
}

// GetSessionToken 从请求中提取会话令牌。
func GetSessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// SetSessionCookie 设置会话 cookie。
// timeoutMin 为超时分钟数：0 表示浏览器会话 cookie（关闭浏览器后失效），N 表示 N 分钟持久 cookie。
func SetSessionCookie(w http.ResponseWriter, token string, timeoutMin int) {
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
	if timeoutMin > 0 {
		cookie.MaxAge = timeoutMin * 60
	}
	// timeoutMin == 0 时不设置 MaxAge，浏览器会话 cookie
	http.SetCookie(w, cookie)
}

// ClearSessionCookie 清除会话 cookie。
func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// startSessionCleaner 启动一个后台 goroutine 定期清理过期会话。
func (am *AuthManager) startSessionCleaner() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			am.CleanExpiredSessions()
		}
	}()
}
