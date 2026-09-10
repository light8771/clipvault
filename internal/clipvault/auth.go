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
	rateBuckets     map[rateBucketKey]*rateBucket // 凭据类别 + 来源 IP -> 失败记录

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
		rateBuckets:     make(map[rateBucketKey]*rateBucket),
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
// GrantSecondaryAuth 一致：<=0 表示跟随浏览器会话（缓存到期对齐会话过期时间，
// 关闭浏览器后失效），N 重置为 now+Nmin。
// 仅当会话已通过二次认证时才调整缓存到期时间；会话不存在则不做任何操作。
func (am *AuthManager) RefreshSecondaryExpiry(token string, timeoutMin int) bool {
	am.mu.Lock()
	defer am.mu.Unlock()
	sess, ok := am.sessions[token]
	if !ok {
		return false
	}
	if sess.SecondaryAuth {
		sess.SecondaryExpiry = secondaryExpiryFor(sess, timeoutMin)
	}
	return true
}

// secondaryExpiryFor 计算二次认证缓存到期时间：timeoutMin <= 0 时跟随会话
// 过期时间（浏览器会话），timeoutMin > 0 时为 now+timeoutMin 分钟。
func secondaryExpiryFor(sess *Session, timeoutMin int) time.Time {
	if timeoutMin > 0 {
		return time.Now().Add(time.Duration(timeoutMin) * time.Minute)
	}
	return sess.ExpiresAt
}

// GrantSecondaryAuth 为会话授予二次认证权限。
// timeoutMin 为缓存分钟数：0 表示跟随浏览器会话（关闭浏览器后失效），N 表示 N 分钟内免重复验证。
func (am *AuthManager) GrantSecondaryAuth(token string, timeoutMin int) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if sess, ok := am.sessions[token]; ok {
		sess.SecondaryAuth = true
		sess.SecondaryExpiry = secondaryExpiryFor(sess, timeoutMin)
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

type rateBucketKey struct {
	purpose  string
	clientIP string
}

// rateBucket owns one credential/source allowance. Only rateMu accesses it.
type rateBucket struct {
	failures  []time.Time
	verifying bool
}

// RateLimitStatus describes the allowance after an attempt or admission check.
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
}

func rateLimitStatus(b *rateBucket, now time.Time) RateLimitStatus {
	if len(b.failures) >= rateLimitThreshold {
		return RateLimitStatus{RetryAfter: b.failures[0].Add(rateLimitWindow).Sub(now)}
	}
	if b.verifying {
		return RateLimitStatus{RetryAfter: time.Second}
	}
	return RateLimitStatus{Allowed: true, Remaining: rateLimitThreshold - len(b.failures)}
}

// verifyPasswordAttempt atomically admits one verification per credential/source.
// The expensive verifier runs outside the map lock so other peers remain usable.
// No separate check/record/reset API is exposed: callers cannot skip accounting.
func (am *AuthManager) verifyPasswordAttempt(purpose, clientIP string, verify func() bool) (valid bool, status RateLimitStatus) {
	key := rateBucketKey{purpose, clientIP}
	am.rateMu.Lock()
	b := am.rateBuckets[key]
	if b == nil {
		b = &rateBucket{}
		am.rateBuckets[key] = b
	}
	now := time.Now()
	pruneRateFailures(b, now)
	status = rateLimitStatus(b, now)
	if !status.Allowed {
		am.rateMu.Unlock()
		return false, status
	}
	b.verifying = true
	am.rateMu.Unlock()

	// Release the reservation even if the verifier panics. A failed verifier must
	// not leave a permanently blocked source or silently restore its allowance.
	defer func() {
		am.rateMu.Lock()
		defer am.rateMu.Unlock()
		b.verifying = false
		now := time.Now()
		pruneRateFailures(b, now)
		if valid {
			delete(am.rateBuckets, key)
			status = RateLimitStatus{Allowed: true, Remaining: rateLimitThreshold}
		} else {
			b.failures = append(b.failures, now)
			status = rateLimitStatus(b, now)
		}
	}()
	valid = verify()
	return
}

// CleanExpiredRateBuckets removes idle sources with no failures in the window.
func (am *AuthManager) CleanExpiredRateBuckets() {
	am.rateMu.Lock()
	defer am.rateMu.Unlock()
	now := time.Now()
	for key, b := range am.rateBuckets {
		pruneRateFailures(b, now)
		if !b.verifying && len(b.failures) == 0 {
			delete(am.rateBuckets, key)
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
