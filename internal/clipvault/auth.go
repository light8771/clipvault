package clipvault

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"clipboard_ext/internal/clipvault/security"
)

const (
	sessionCookieName = "cv_session"
	sessionTokenBytes = 32
	// 内部会话最长存活时间，用于清理过期会话
	maxSessionLifetime     = 24 * time.Hour
	secondaryTokenLifetime = 2 * time.Minute

	// 密码限流参数
	rateLimitThreshold   = 10              // 每个统计窗口最多失败次数
	rateLimitWindow      = 5 * time.Minute // 滚动统计窗口
	ratePurposeLock      = "lock"
	ratePurposeSecondary = "secondary"

	maxAuthPasswordBytes = security.MaxPasswordBytes
	minAuthPasswordRunes = security.MinPasswordRunes
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
	secondaryTokens map[string]secondaryToken
	rateMu          sync.Mutex
	rateBuckets     map[string]*rateBucket // 凭据类别 -> 失败记录

	stopCh    chan struct{} // startSessionCleaner 的退出信号（Close 关闭）
	closeOnce sync.Once     // 防 Close 重复关闭 stopCh panic
}

type secondaryToken struct {
	SessionToken string
	ExpiresAt    time.Time
}

// NewAuthManager 创建一个新的 AuthManager。
func NewAuthManager() *AuthManager {
	return &AuthManager{
		sessions:        make(map[string]*Session),
		secondaryTokens: make(map[string]secondaryToken),
		rateBuckets:     make(map[string]*rateBucket),
		stopCh:          make(chan struct{}),
	}
}

func newPasswordHash(password string) (string, error) {
	return security.HashPassword(password)
}

func validateAuthPassword(password string) error {
	return security.ValidatePassword(password)
}

func verifyPasswordHash(encoded, password string) bool {
	return security.VerifyPassword(encoded, password)
}

func parsePasswordHash(encoded string) (salt, expected []byte, err error) {
	return security.ParsePasswordHash(encoded)
}

func validateStoredPasswordHash(encoded string) error {
	if encoded == "" {
		return nil
	}
	return security.ValidateStoredPasswordHash(encoded)
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
	return verifyPasswordHash(cfg.Security.LockPasswordHash, password)
}

// VerifySecondaryPassword 验证二次密码。
func (am *AuthManager) VerifySecondaryPassword(cfg Config, password string) bool {
	if cfg.Security.SecondaryPasswordHash == "" {
		return true
	}
	return verifyPasswordHash(cfg.Security.SecondaryPasswordHash, password)
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

// GetSession 根据令牌获取会话快照，已过期则返回 nil。
func (am *AuthManager) GetSession(token string) *Session {
	am.mu.Lock()
	defer am.mu.Unlock()
	sess, ok := am.sessions[token]
	if !ok {
		return nil
	}
	if time.Now().After(sess.ExpiresAt) {
		am.destroySessionLocked(token)
		return nil
	}
	snapshot := *sess
	return &snapshot
}

// RefreshSessionExpiry 按 timeoutMin 重置当前会话的过期时间，用于 lockSessionTimeout
// 配置变更后让新超时立即对当前会话生效。timeoutMin 语义与 CreateSession 一致：
// 0 表示浏览器会话（内部按 maxSessionLifetime 管理），N 表示 N 分钟。
// 返回会话是否存在；不存在则不做任何操作。
func (am *AuthManager) RefreshSessionExpiry(token string, timeoutMin int) bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	sess, ok := am.sessions[token]
	if !ok {
		return false
	}
	now := time.Now()
	expiry := now.Add(maxSessionLifetime)
	if timeoutMin > 0 {
		expiry = now.Add(time.Duration(timeoutMin) * time.Minute)
	}
	sess.ExpiresAt = expiry
	return true
}

// RefreshSecondaryExpiry 按 timeoutMin 重置当前会话的二次认证缓存，用于
// secondaryAuthTimeout 配置变更后让新超时立即对当前会话生效。语义与
// GrantSecondaryAuth 一致：<=0 撤销缓存（每次操作都需要验证），N 重置为 now+Nmin。
// 仅当会话已通过二次认证时才调整缓存到期时间；会话不存在则不做任何操作。
func (am *AuthManager) RefreshSecondaryExpiry(token string, timeoutMin int) bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	sess, ok := am.sessions[token]
	if !ok {
		return false
	}
	if timeoutMin <= 0 {
		sess.SecondaryAuth = false
		return true
	}
	if sess.SecondaryAuth {
		sess.SecondaryExpiry = time.Now().Add(time.Duration(timeoutMin) * time.Minute)
	}
	return true
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
	am.mu.Lock()
	defer am.mu.Unlock()
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
	defer am.mu.Unlock()
	sess, ok := am.sessions[sessionToken]
	if !ok || !sess.Authenticated || time.Now().After(sess.ExpiresAt) {
		return "", fmt.Errorf("secondary token requires an active session")
	}
	am.secondaryTokens[token] = secondaryToken{
		SessionToken: sessionToken,
		ExpiresAt:    time.Now().Add(secondaryTokenLifetime),
	}
	return token, nil
}

// ConsumeSecondaryToken 消费一个一次性二次认证 token。令牌必须属于当前
// 浏览器会话、仍在短有效期内，且该会话本身仍然有效。
func (am *AuthManager) ConsumeSecondaryToken(token, sessionToken string) bool {
	if token == "" || sessionToken == "" {
		return false
	}
	am.mu.Lock()
	defer am.mu.Unlock()
	grant, ok := am.secondaryTokens[token]
	if !ok {
		return false
	}
	// 无论成功与否都消费，避免攻击者拿着窃取的令牌反复试探会话。
	delete(am.secondaryTokens, token)
	if grant.SessionToken != sessionToken || time.Now().After(grant.ExpiresAt) {
		return false
	}
	sess, ok := am.sessions[sessionToken]
	return ok && sess.Authenticated && time.Now().Before(sess.ExpiresAt)
}

// DestroySession 销毁会话。
func (am *AuthManager) DestroySession(token string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.destroySessionLocked(token)
}

func (am *AuthManager) destroySessionLocked(token string) {
	delete(am.sessions, token)
	for st, grant := range am.secondaryTokens {
		if grant.SessionToken == token {
			delete(am.secondaryTokens, st)
		}
	}
}

// RevokeAllSecondaryAuth 撤销所有会话的二次认证权限。
// 用于二次密码被修改后，强制所有会话重新进行二次验证。
func (am *AuthManager) RevokeAllSecondaryAuth() {
	am.mu.Lock()
	defer am.mu.Unlock()
	for _, sess := range am.sessions {
		sess.SecondaryAuth = false
	}
	// 清理所有一次性 token
	am.secondaryTokens = make(map[string]secondaryToken)
}

// DestroyAllSessionsExcept 销毁除指定 token 外的所有会话。
// 用于锁屏密码被修改后，踢出其他设备的会话。
func (am *AuthManager) DestroyAllSessionsExcept(exceptToken string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	for token := range am.sessions {
		if token != exceptToken {
			delete(am.sessions, token)
		}
	}
	// 清理绑定到已销毁会话的一次性 token
	for st, grant := range am.secondaryTokens {
		if grant.SessionToken != exceptToken {
			delete(am.secondaryTokens, st)
		}
	}
}

// CleanExpiredSessions 清理所有过期会话和一次性 token。
func (am *AuthManager) CleanExpiredSessions() {
	am.mu.Lock()
	defer am.mu.Unlock()
	now := time.Now()
	alive := make(map[string]bool)
	for token, sess := range am.sessions {
		if now.After(sess.ExpiresAt) {
			delete(am.sessions, token)
		} else {
			alive[token] = true
		}
	}
	// 清理绑定到已过期会话的一次性 token
	for st, grant := range am.secondaryTokens {
		if !alive[grant.SessionToken] || now.After(grant.ExpiresAt) {
			delete(am.secondaryTokens, st)
		}
	}
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
func SetSessionCookie(w http.ResponseWriter, token string, timeoutMin int, secure bool) {
	cookie := &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	}
	if timeoutMin > 0 {
		cookie.MaxAge = timeoutMin * 60
	}
	// timeoutMin == 0 时不设置 MaxAge，浏览器会话 cookie
	http.SetCookie(w, cookie)
}

// ClearSessionCookie 清除会话 cookie。
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
		MaxAge:   -1,
	})
}

// rateBucket 记录全局密码失败尝试。
type rateBucket struct {
	failures    []time.Time // 失败时间戳
	lockedUntil time.Time   // 锁定截止时间
}

// RateLimitStatus 描述当前密码尝试额度。
type RateLimitStatus struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

func pruneRateFailures(b *rateBucket, now time.Time) {
	cutoff := now.Add(-rateLimitWindow)
	kept := b.failures[:0]
	for _, failedAt := range b.failures {
		if failedAt.After(cutoff) {
			kept = append(kept, failedAt)
		}
	}
	b.failures = kept
	if !b.lockedUntil.IsZero() && !now.Before(b.lockedUntil) {
		b.lockedUntil = time.Time{}
	}
}

func rateLimitStatus(b *rateBucket, now time.Time) RateLimitStatus {
	remaining := rateLimitThreshold - len(b.failures)
	if remaining < 0 {
		remaining = 0
	}
	status := RateLimitStatus{Allowed: true, Remaining: remaining}
	if now.Before(b.lockedUntil) {
		status.Allowed = false
		status.Remaining = 0
		status.RetryAfter = b.lockedUntil.Sub(now)
	}
	return status
}

// CheckRateLimitFor 检查指定凭据类别的密码尝试额度。锁屏密码与二次密码
// 使用独立桶，任一凭据验证成功都不会替另一凭据清空失败次数。
func (am *AuthManager) CheckRateLimitFor(purpose string) RateLimitStatus {
	am.rateMu.Lock()
	defer am.rateMu.Unlock()
	b, ok := am.rateBuckets[purpose]
	if !ok {
		return RateLimitStatus{Allowed: true, Remaining: rateLimitThreshold}
	}
	now := time.Now()
	pruneRateFailures(b, now)
	return rateLimitStatus(b, now)
}

// RecordFailedAttempt 记录一次密码验证失败并返回剩余额度。
func (am *AuthManager) RecordFailedAttemptFor(purpose string) RateLimitStatus {
	am.rateMu.Lock()
	defer am.rateMu.Unlock()
	now := time.Now()
	b, ok := am.rateBuckets[purpose]
	if !ok {
		b = &rateBucket{}
		am.rateBuckets[purpose] = b
	}
	pruneRateFailures(b, now)
	b.failures = append(b.failures, now)
	if len(b.failures) >= rateLimitThreshold {
		// 滚动窗口：直到最早一次失败离开 5 分钟窗口后恢复额度。
		b.lockedUntil = b.failures[0].Add(rateLimitWindow)
	}
	return rateLimitStatus(b, now)
}

// ResetRateLimit 清除失败记录（密码验证成功后调用）。
func (am *AuthManager) ResetRateLimitFor(purpose string) {
	am.rateMu.Lock()
	defer am.rateMu.Unlock()
	delete(am.rateBuckets, purpose)
}

// CleanExpiredRateBuckets 清理过期的限流桶。
func (am *AuthManager) CleanExpiredRateBuckets() {
	am.rateMu.Lock()
	defer am.rateMu.Unlock()
	now := time.Now()
	for purpose, b := range am.rateBuckets {
		pruneRateFailures(b, now)
		if now.After(b.lockedUntil) && len(b.failures) == 0 {
			delete(am.rateBuckets, purpose)
		}
	}
}

// startSessionCleaner 启动一个后台 goroutine 定期清理过期会话和限流桶。
// goroutine 监听 stopCh，Close 调用后立即退出，避免 Web 重启时泄漏。
func (am *AuthManager) startSessionCleaner() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				am.CleanExpiredSessions()
				am.CleanExpiredRateBuckets()
			case <-am.stopCh:
				return
			}
		}
	}()
}

// Close 停止 cleaner goroutine（幂等，可多次调用）。
// SearchServer.Close 时调用，保证每次 Web 重启不泄漏一个 cleaner。
func (am *AuthManager) Close() {
	am.closeOnce.Do(func() { close(am.stopCh) })
}
