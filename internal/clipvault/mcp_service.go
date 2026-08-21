package clipvault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"clipboard_ext/internal/licensing"
)

// MCP 资源治理参数：与数据接口 HTTP 治理口径一致（低优先级可牺牲服务）。
const (
	mcpEndpointPath     = "/mcp"
	mcpMaxConcurrent    = dataAPIMaxConcurrent
	mcpQueryTimeout     = dataAPIQueryTimeout
	mcpMaxBatchIDs      = dataAPIMaxBatchIDs
	mcpMaxContentSize   = dataAPIMaxContentSize
	mcpRatePerSec       = dataAPIRatePerSec
	mcpRateBurst        = dataAPIRateBurst
	mcpServerName       = "ClipVault"
	mcpServerVersionNum = "1"
)

// mcpError 是 MCP 服务层的协议错误（HTTP 层写 JSON-RPC 错误或 401）。
type mcpError struct {
	Status  int
	Code    string
	Message string
}

func newMCPError(status int, code, message string) *mcpError {
	return &mcpError{Status: status, Code: code, Message: message}
}

// mcpRequestContext 是一次已认证 MCP 请求的会话信息（绑定进 per-request
// Server 构造闭包，工具/资源 handler 由此获得访问权限与审计上下文）。
type mcpRequestContext struct {
	token    *APIToken
	access   ReadAccess
	remoteIP string
}

// MCPService 是独立监听的只读 MCP 服务（Streamable HTTP，端点 /mcp）。
// 与数据接口共享 ExternalReadService；令牌按 channel='mcp' 隔离；
// 失败/过载绝不影响剪贴板采集与主 Web 服务（低优先级失败隔离）。
type MCPService struct {
	cfgMgr  *ConfigManager
	readSvc *ExternalReadService
	store   *Store
	logger  *DailyLogger
	license *licensing.Manager
	env     Environment

	mu        sync.Mutex
	server    *http.Server
	listen    string
	running   bool
	bindError string

	gov chan struct{} // 并发信号量

	rateMu  sync.Mutex
	rateMap map[string]*dataAPIRateBucket

	// activatedFn 每次请求实时判定激活状态；测试可注入替代实现。
	activatedFn func() bool

	// schemaCache 复用工具 JSON Schema 推导结果（无状态每请求建 Server）。
	schemaCache *mcp.SchemaCache

	auditCh      chan APIAuditEvent
	auditDone    chan struct{}
	auditClose   sync.Once
	auditDropped atomic.Int64
	// auditMu 保证"向 auditCh 发送"与"关闭 auditCh"互斥（同 DataAPIService）。
	auditMu     sync.RWMutex
	auditClosed bool

	activeQueries  atomic.Int64
	requestsTotal  atomic.Int64
	busyRejections atomic.Int64
}

// NewMCPService 创建服务（不监听），并启动异步审计 writer。
func NewMCPService(cfgMgr *ConfigManager, readSvc *ExternalReadService, store *Store, logger *DailyLogger, license *licensing.Manager, env Environment) *MCPService {
	if readSvc == nil {
		readSvc = NewExternalReadService(store, env)
	}
	svc := &MCPService{
		cfgMgr:      cfgMgr,
		readSvc:     readSvc,
		store:       store,
		logger:      logger,
		license:     license,
		env:         env,
		gov:         make(chan struct{}, mcpMaxConcurrent),
		rateMap:     map[string]*dataAPIRateBucket{},
		schemaCache: mcp.NewSchemaCache(),
		auditCh:     make(chan APIAuditEvent, dataAPIAuditBuffer),
		auditDone:   make(chan struct{}),
	}
	svc.activatedFn = func() bool { return !FreeTierRestriction(env) }
	go svc.auditWriterLoop()
	return svc
}

// ApplyConfig 按配置启停/重绑监听。配置变更监听器同步调用，必须快速返回。
func (svc *MCPService) ApplyConfig(cfg Config) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	want := strings.TrimSpace(cfg.MCP.ListenAddr)
	if !cfg.MCP.Enabled {
		svc.stopLocked()
		return nil
	}
	if svc.running && svc.listen == want {
		return nil
	}
	svc.stopLocked()
	return svc.startLocked(want)
}

// startLocked 绑定并启动监听（调用方持锁）。绑定失败只记录状态，不终止主进程。
func (svc *MCPService) startLocked(addr string) error {
	host, port, err := validateListenAddr(addr)
	if err != nil {
		svc.bindError = err.Error()
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		svc.bindError = err.Error()
		svc.logger.Warn("mcp_bind_failed", map[string]any{"addr": addr, "error": err.Error()})
		return fmt.Errorf("mcp listen %s: %w", addr, err)
	}
	// 无状态 Streamable HTTP：每请求构造绑定该请求权限的 Server 实例。
	sdkHandler := mcp.NewStreamableHTTPHandler(svc.serverForRequest, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		Logger:                       nil,
		MaxRequestBodyBytes:          1 << 20,
		PropagateRequestCancellation: true,
	})
	mux := http.NewServeMux()
	mux.Handle(mcpEndpointPath, svc.wrapMCP(sdkHandler))
	svc.server = &http.Server{
		Handler:           svc.recoverMiddleware(svc.secureHeaders(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	svc.listen = addr
	svc.running = true
	svc.bindError = ""
	go func() {
		if err := svc.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			svc.logger.Error("mcp_server_failed", err, map[string]any{"addr": addr})
			svc.mu.Lock()
			svc.running = false
			svc.bindError = err.Error()
			svc.mu.Unlock()
		}
	}()
	svc.logger.Info("mcp_started", map[string]any{"addr": addr})
	return nil
}

// stopLocked 真正关闭监听，释放端口。
func (svc *MCPService) stopLocked() {
	if svc.server == nil {
		svc.running = false
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.server.Shutdown(ctx); err != nil {
		svc.logger.Warn("mcp_shutdown_failed", map[string]any{"error": err.Error()})
	}
	svc.server = nil
	svc.running = false
	svc.listen = ""
	svc.logger.Info("mcp_stopped", nil)
}

// Status 返回运行时状态快照（并发安全）。
func (svc *MCPService) Status() externalServiceStatus {
	svc.mu.Lock()
	running, listen, bindError := svc.running, svc.listen, svc.bindError
	svc.mu.Unlock()
	status := externalServiceStatus{
		Running:        running,
		BindError:      bindError,
		ActiveQueries:  svc.activeQueries.Load(),
		RequestsTotal:  svc.requestsTotal.Load(),
		BusyRejections: svc.busyRejections.Load(),
		AuditDropped:   svc.auditDropped.Load(),
	}
	if running {
		host, port, err := validateListenAddr(listen)
		if err == nil {
			displayHost := host
			if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && ip.IsUnspecified() {
				displayHost = "127.0.0.1"
			}
			status.URL = "http://" + net.JoinHostPort(displayHost, strconv.Itoa(port)) + mcpEndpointPath
		}
	}
	return status
}

// Close 停止监听并优雅关闭审计 writer（应用退出时调用一次）。
func (svc *MCPService) Close() {
	svc.mu.Lock()
	svc.stopLocked()
	svc.mu.Unlock()
	svc.auditClose.Do(func() {
		svc.auditMu.Lock()
		svc.auditClosed = true
		close(svc.auditCh)
		svc.auditMu.Unlock()
		<-svc.auditDone
	})
}

// ── 中间件 ────────────────────────────────────────────────

// recoverMiddleware 保证外部请求中的 panic 不会让主进程退出。
func (svc *MCPService) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				svc.logger.Error("mcp_panic", fmt.Errorf("%v", rec), map[string]any{"path": r.URL.Path})
				writeMCPHTTPError(w, http.StatusInternalServerError, "internal_error", "Internal server error.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// secureHeaders 禁止缓存与嵌入。
func (svc *MCPService) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// wrapMCP 是 /mcp 端点的完整管线：激活检查（每次请求）→ Bearer 认证
// （channel='mcp'）→ 限流 → 并发治理 → 查询超时。认证/限流/忙拒绝写审计。
func (svc *MCPService) wrapMCP(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc.requestsTotal.Add(1)
		audit := svc.auditEventFor(r, nil, "auth", nil)
		started := time.Now()
		fail := func(err *mcpError) {
			audit.DurationMs = time.Since(started).Milliseconds()
			audit.StatusCode, audit.ErrorCode = err.Status, err.Code
			svc.emitAudit(audit)
			writeMCPHTTPError(w, err.Status, err.Code, err.Message)
			// 拒绝发生在 SDK 读 body 之前：排空剩余请求体，避免连接因 body
			// 残留被 TCP 重置（客户端表现为 ConnectionResetError 而非 429）。
			drainRejectedBody(w, r)
		}
		if !svc.activated() {
			fail(newMCPError(http.StatusForbidden, "activation_required", "ClipVault MCP requires an activated user."))
			return
		}
		token, apiErr := svc.authenticate(r)
		if apiErr != nil {
			fail(apiErr)
			return
		}
		audit.TokenID = token.ID
		audit.TokenName = truncateRunes(token.Name, 60)
		if !svc.allowRequest(remoteIP(r)) {
			w.Header().Set("Retry-After", "1")
			fail(newMCPError(http.StatusTooManyRequests, "too_many_requests", "Rate limit exceeded."))
			return
		}
		select {
		case svc.gov <- struct{}{}:
			defer func() { <-svc.gov }()
		default:
			svc.busyRejections.Add(1)
			w.Header().Set("Retry-After", "2")
			fail(newMCPError(http.StatusTooManyRequests, "mcp_busy", "ClipVault MCP is busy. Retry later."))
			return
		}
		svc.activeQueries.Add(1)
		defer svc.activeQueries.Add(-1)
		// 认证成功：请求级上下文（令牌 + 读取权限 + 远端 IP）注入 HTTP 上下文，
		// serverForRequest 据此构造绑定该权限的 per-request Server。
		reqCtx := &mcpRequestContext{
			token:    token,
			access:   ReadAccess{AllowSensitive: token.AllowSensitive},
			remoteIP: remoteIP(r),
		}
		ctx, cancel := context.WithTimeout(context.WithValue(r.Context(), mcpRequestContextKey{}, reqCtx), mcpQueryTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// mcpRequestContextKey 是 HTTP 请求上下文中 mcpRequestContext 的键。
type mcpRequestContextKey struct{}

// serverForRequest 为每个已认证请求构造绑定其权限的 MCP Server。
// 认证失败（无上下文）返回 nil，SDK 以 400 拒绝（正常流量已在中间件被拦截）。
func (svc *MCPService) serverForRequest(r *http.Request) *mcp.Server {
	reqCtx, _ := r.Context().Value(mcpRequestContextKey{}).(*mcpRequestContext)
	if reqCtx == nil || reqCtx.token == nil {
		return nil
	}
	return svc.newSDKServer(reqCtx)
}

// authenticate 校验 Bearer Token；MCP 只认 channel='mcp' 的令牌（SEC-07）。
func (svc *MCPService) authenticate(r *http.Request) (*APIToken, *mcpError) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return nil, newMCPError(http.StatusUnauthorized, "missing_token", "Missing Authorization bearer token.")
	}
	raw, found := strings.CutPrefix(header, "Bearer ")
	if !found || strings.TrimSpace(raw) == "" {
		return nil, newMCPError(http.StatusUnauthorized, "invalid_token", "Malformed Authorization header.")
	}
	token, err := svc.store.AuthenticateAPIToken(r.Context(), strings.TrimSpace(raw), TokenChannelMCP)
	if err != nil {
		switch {
		case errors.Is(err, errAPITokenExpired):
			return nil, newMCPError(http.StatusUnauthorized, "token_expired", "The access token has expired.")
		case errors.Is(err, errAPITokenRevoked):
			return nil, newMCPError(http.StatusUnauthorized, "token_revoked", "The access token has been revoked.")
		case errors.Is(err, errAPITokenNotFound), errors.Is(err, errAPITokenChannel):
			return nil, newMCPError(http.StatusUnauthorized, "invalid_token", "The access token is invalid.")
		default:
			return nil, newMCPError(http.StatusInternalServerError, "internal_error", "Internal server error.")
		}
	}
	// 最后访问时间异步更新，不阻塞请求。
	go func(id int64, ts int64) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = svc.store.TouchAPITokenLastUsed(ctx, id, ts)
	}(token.ID, time.Now().UTC().UnixMilli())
	return &token, nil
}

// activated 每次请求从磁盘重新读取真实激活状态，不依赖缓存。
func (svc *MCPService) activated() bool {
	return svc.activatedFn()
}

// allowRequest 每 IP 令牌桶限流（10/s，突发 30），与数据接口一致。
func (svc *MCPService) allowRequest(ip string) bool {
	svc.rateMu.Lock()
	defer svc.rateMu.Unlock()
	bucket, ok := svc.rateMap[ip]
	if !ok {
		if len(svc.rateMap) > 4096 {
			svc.rateMap = map[string]*dataAPIRateBucket{}
		}
		bucket = &dataAPIRateBucket{tokens: mcpRateBurst, last: time.Now()}
		svc.rateMap[ip] = bucket
	}
	now := time.Now()
	bucket.tokens += now.Sub(bucket.last).Seconds() * mcpRatePerSec
	if bucket.tokens > mcpRateBurst {
		bucket.tokens = mcpRateBurst
	}
	bucket.last = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

// writeMCPHTTPError 在 HTTP 层写错误（认证/限流发生在 JSON-RPC 之前）。
func writeMCPHTTPError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": code, "message": message})
}

// ── 审计 ──────────────────────────────────────────────────

// emitAudit 非阻塞投递审计事件；通道满时丢弃并计数。
func (svc *MCPService) emitAudit(event APIAuditEvent) {
	svc.auditMu.RLock()
	defer svc.auditMu.RUnlock()
	if svc.auditClosed {
		svc.auditDropped.Add(1)
		return
	}
	select {
	case svc.auditCh <- event:
	default:
		svc.auditDropped.Add(1)
	}
}

// auditEventFor 构造 MCP 审计事件骨架（channel=mcp）。
func (svc *MCPService) auditEventFor(r *http.Request, token *APIToken, operation string, failed *mcpError) APIAuditEvent {
	event := APIAuditEvent{
		Timestamp: time.Now().UTC(),
		Channel:   TokenChannelMCP,
		RemoteIP:  remoteIP(r),
		Method:    r.Method,
		Endpoint:  r.URL.Path,
		Operation: operation,
	}
	if token != nil {
		event.TokenID = token.ID
		event.TokenName = truncateRunes(token.Name, 60)
	}
	if failed != nil {
		event.StatusCode = failed.Status
		event.ErrorCode = failed.Code
	}
	return event
}

// auditWriterLoop 批量落盘审计：缓冲通道 + 定时 flush；故障只记日志不崩溃。
func (svc *MCPService) auditWriterLoop() {
	defer close(svc.auditDone)
	batch := make([]APIAuditEvent, 0, dataAPIAuditBatchSize)
	ticker := time.NewTicker(dataAPIAuditFlushEvery)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svc.store.InsertAPIAuditEvents(ctx, batch); err != nil {
			svc.logger.Warn("mcp_audit_write_failed", map[string]any{"error": err.Error(), "dropped": len(batch)})
			svc.auditDropped.Add(int64(len(batch)))
		}
		batch = batch[:0]
	}
	for {
		select {
		case event, ok := <-svc.auditCh:
			if !ok {
				flush()
				return
			}
			batch = append(batch, event)
			if len(batch) >= dataAPIAuditBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
