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

	"clipboard_ext/internal/licensing"
)

// 数据接口资源治理参数（PERF-01/02/03/04）：外部 API 是可牺牲的低优先级服务。
const (
	dataAPIMaxConcurrent  = 4               // 并发查询硬上限，满载快速 429
	dataAPIDefaultLimit   = 50              // 默认分页大小
	dataAPIMaxLimit       = 500             // 分页大小上限
	dataAPIQueryTimeout   = 5 * time.Second // 单次查询超时
	dataAPIMaxBodyBytes   = 1 << 20         // 请求体上限 1MiB
	dataAPIMaxBatchIDs    = 100             // batch-get 最大 ID 数
	dataAPIMaxContentSize = 64 << 20        // 二进制内容上限 64MiB
	dataAPITextPreviewMax = 4000            // 列表响应中文本预览最大字符数

	dataAPIRatePerSec = 10 // 每 IP 令牌桶速率
	dataAPIRateBurst  = 30 // 每 IP 令牌桶突发

	dataAPIAuditBuffer     = 1000 // 审计通道缓冲
	dataAPIAuditFlushEvery = 500 * time.Millisecond
	dataAPIAuditBatchSize  = 50
	dataAPIAuditBodyMax    = 8192 // 审计记录保存的原始请求体上限（runes）
)

// dataAPIError 是外部数据 API 的统一错误结构（{"error","message"}）。
type dataAPIError struct {
	Status  int
	Code    string
	Message string
}

func (e *dataAPIError) Error() string { return e.Code + ": " + e.Message }

func newDataAPIError(status int, code, message string) *dataAPIError {
	return &dataAPIError{Status: status, Code: code, Message: message}
}

// dataAPIStatus 是管理界面可见的运行时状态快照。
type dataAPIStatus struct {
	Running        bool   `json:"running"`
	URL            string `json:"url"`
	BindError      string `json:"bindError,omitempty"`
	ActiveQueries  int64  `json:"activeQueries"`
	RequestsTotal  int64  `json:"requestsTotal"`
	BusyRejections int64  `json:"busyRejections"`
	AuditDropped   int64  `json:"auditDropped"`
}

// DataAPIService 是独立监听的外部数据 API 服务（低优先级附加服务）。
// 剪贴板采集与主 Web 服务永远优先；本服务可拒绝、超时、降级甚至整体关闭。
// 数据读取统一委托 ExternalReadService（敏感可见性在共享服务强制，
// 协议层只做 DSL 解析与响应映射）。
type DataAPIService struct {
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

	auditCh      chan APIAuditEvent
	auditDone    chan struct{}
	auditClose   sync.Once
	auditDropped atomic.Int64
	// auditMu 保证"向 auditCh 发送"与"关闭 auditCh"互斥：发送方持 RLock，
	// Close 持 Lock 后关闭，避免退出期 handler 残余 emitAudit 向已关闭通道发送而 panic。
	auditMu     sync.RWMutex
	auditClosed bool

	activeQueries  atomic.Int64
	requestsTotal  atomic.Int64
	busyRejections atomic.Int64
}

type dataAPIRateBucket struct {
	tokens float64
	last   time.Time
}

// NewDataAPIService 创建服务（不监听），并启动异步审计 writer。
// readSvc 是与 MCP 共享的只读业务服务；store 仍用于 Token 认证与审计。
func NewDataAPIService(cfgMgr *ConfigManager, readSvc *ExternalReadService, store *Store, logger *DailyLogger, license *licensing.Manager, env Environment) *DataAPIService {
	if readSvc == nil {
		readSvc = NewExternalReadService(store, env)
	}
	svc := &DataAPIService{
		cfgMgr:    cfgMgr,
		readSvc:   readSvc,
		store:     store,
		logger:    logger,
		license:   license,
		env:       env,
		gov:       make(chan struct{}, dataAPIMaxConcurrent),
		rateMap:   map[string]*dataAPIRateBucket{},
		auditCh:   make(chan APIAuditEvent, dataAPIAuditBuffer),
		auditDone: make(chan struct{}),
	}
	svc.activatedFn = func() bool { return !FreeTierRestriction(env) }
	go svc.auditWriterLoop()
	return svc
}

// ApplyConfig 按配置启停/重绑监听。配置变更监听器同步调用，必须快速返回。
func (svc *DataAPIService) ApplyConfig(cfg Config) error {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	want := strings.TrimSpace(cfg.DataAPI.ListenAddr)
	if !cfg.DataAPI.Enabled {
		svc.stopLocked()
		return nil
	}
	if svc.running && svc.listen == want {
		return nil
	}
	svc.stopLocked()
	return svc.startLocked(want)
}

// startLocked 绑定并启动监听（调用方持锁）。
func (svc *DataAPIService) startLocked(addr string) error {
	host, port, err := validateListenAddr(addr)
	if err != nil {
		svc.bindError = err.Error()
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		svc.bindError = err.Error()
		svc.logger.Warn("data_api_bind_failed", map[string]any{"addr": addr, "error": err.Error()})
		return fmt.Errorf("data API listen %s: %w", addr, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", svc.handleHealth)
	mux.HandleFunc("GET /api/v1/metadata", svc.wrap(svc.handleMetadata, "get_metadata"))
	mux.HandleFunc("POST /api/v1/query", svc.wrap(svc.handleQuery, "query_items"))
	mux.HandleFunc("GET /api/v1/items/{id}", svc.wrap(svc.handleGetItem, "get_item"))
	mux.HandleFunc("POST /api/v1/items/batch-get", svc.wrap(svc.handleBatchGet, "batch_get"))
	mux.HandleFunc("GET /api/v1/items/{id}/content", svc.wrap(svc.handleItemContent, "get_content"))
	svc.server = &http.Server{
		Handler:           svc.recoverMiddleware(svc.secureHeaders(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// WriteTimeout 必须覆盖流式图片响应；连接取消由请求 context 保证。
		WriteTimeout:   5 * time.Minute,
		IdleTimeout:    30 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	svc.listen = addr
	svc.running = true
	svc.bindError = ""
	go func() {
		if err := svc.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			svc.logger.Error("data_api_server_failed", err, map[string]any{"addr": addr})
			svc.mu.Lock()
			svc.running = false
			svc.bindError = err.Error()
			svc.mu.Unlock()
		}
	}()
	svc.logger.Info("data_api_started", map[string]any{"addr": addr})
	return nil
}

// stopLocked 真正关闭监听（不只是拒绝请求），释放端口（NET 启停语义）。
func (svc *DataAPIService) stopLocked() {
	if svc.server == nil {
		svc.running = false
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.server.Shutdown(ctx); err != nil {
		svc.logger.Warn("data_api_shutdown_failed", map[string]any{"error": err.Error()})
	}
	svc.server = nil
	svc.running = false
	svc.listen = ""
	svc.logger.Info("data_api_stopped", nil)
}

// Status 返回运行时状态快照（并发安全）。
func (svc *DataAPIService) Status() dataAPIStatus {
	svc.mu.Lock()
	running, listen, bindError := svc.running, svc.listen, svc.bindError
	svc.mu.Unlock()
	status := dataAPIStatus{
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
			status.URL = "http://" + net.JoinHostPort(displayHost, strconv.Itoa(port))
		}
	}
	return status
}

// Close 停止监听并优雅关闭审计 writer（应用退出时调用一次）。
func (svc *DataAPIService) Close() {
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

// recoverMiddleware 保证外部请求中的 panic 不会让主进程退出（REL-01）。
func (svc *DataAPIService) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				svc.logger.Error("data_api_panic", fmt.Errorf("%v", rec), map[string]any{"path": r.URL.Path})
				svc.writeError(w, newDataAPIError(http.StatusInternalServerError, "internal_error", "Internal server error."))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// secureHeaders 外部 API 只返回 JSON，禁止缓存与嵌入。
func (svc *DataAPIService) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// activated 每次请求从磁盘重新读取真实激活状态（AUTH-04），不依赖缓存。
func (svc *DataAPIService) activated() bool {
	return svc.activatedFn()
}

// wrap 是认证端点的完整管线：启用检查 → 激活检查（每次请求）→ Bearer 认证 →
// 限流 → 并发治理 → 查询超时。审计事件由 handler 通过 svc.emitAudit 上报。
func (svc *DataAPIService) wrap(next func(w http.ResponseWriter, r *http.Request, token *APIToken), operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		svc.requestsTotal.Add(1)
		if !svc.activated() {
			svc.writeError(w, newDataAPIError(http.StatusForbidden, "activation_required", "ClipVault Data API requires an activated user."))
			return
		}
		token, ok := svc.authenticate(w, r)
		if !ok {
			return
		}
		if !svc.allowRequest(remoteIP(r)) {
			w.Header().Set("Retry-After", "1")
			err := newDataAPIError(http.StatusTooManyRequests, "too_many_requests", "Rate limit exceeded.")
			svc.writeError(w, err)
			svc.emitAudit(auditEventFor(r, token, operation, err))
			return
		}
		select {
		case svc.gov <- struct{}{}:
			defer func() { <-svc.gov }()
		default:
			svc.busyRejections.Add(1)
			w.Header().Set("Retry-After", "2")
			err := newDataAPIError(http.StatusTooManyRequests, "api_busy", "ClipVault Data API is busy. Retry later.")
			svc.writeError(w, err)
			svc.emitAudit(auditEventFor(r, token, operation, err))
			return
		}
		svc.activeQueries.Add(1)
		defer svc.activeQueries.Add(-1)
		ctx, cancel := context.WithTimeout(r.Context(), dataAPIQueryTimeout)
		defer cancel()
		next(w, r.WithContext(ctx), token)
	}
}

// authenticate 校验 Bearer Token（只允许 Authorization 头，禁止 query 传参）。
// HTTP 数据接口只认 channel='http' 的 Token（SEC-07：HTTP 与 MCP 令牌隔离）。
// 认证失败写 401 并留审计（operation=auth）。
func (svc *DataAPIService) authenticate(w http.ResponseWriter, r *http.Request) (*APIToken, bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		err := newDataAPIError(http.StatusUnauthorized, "missing_token", "Missing Authorization bearer token.")
		svc.writeError(w, err)
		svc.emitAudit(auditEventFor(r, nil, "auth", err))
		return nil, false
	}
	raw, found := strings.CutPrefix(header, "Bearer ")
	if !found || strings.TrimSpace(raw) == "" {
		err := newDataAPIError(http.StatusUnauthorized, "invalid_token", "Malformed Authorization header.")
		svc.writeError(w, err)
		svc.emitAudit(auditEventFor(r, nil, "auth", err))
		return nil, false
	}
	token, err := svc.store.AuthenticateAPIToken(r.Context(), strings.TrimSpace(raw), TokenChannelHTTP)
	if err != nil {
		var apiErr *dataAPIError
		switch {
		case errors.Is(err, errAPITokenExpired):
			apiErr = newDataAPIError(http.StatusUnauthorized, "token_expired", "The access token has expired.")
		case errors.Is(err, errAPITokenRevoked):
			apiErr = newDataAPIError(http.StatusUnauthorized, "token_revoked", "The access token has been revoked.")
		case errors.Is(err, errAPITokenNotFound), errors.Is(err, errAPITokenChannel):
			apiErr = newDataAPIError(http.StatusUnauthorized, "invalid_token", "The access token is invalid.")
		default:
			apiErr = newDataAPIError(http.StatusInternalServerError, "internal_error", "Internal server error.")
		}
		svc.writeError(w, apiErr)
		svc.emitAudit(auditEventFor(r, nil, "auth", apiErr))
		return nil, false
	}
	// 最后访问时间异步更新，不阻塞请求。
	go func(id int64, ts int64) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = svc.store.TouchAPITokenLastUsed(ctx, id, ts)
	}(token.ID, time.Now().UTC().UnixMilli())
	return &token, true
}

// readAccess 把已认证 Token 映射为共享读取权限；协议层不实现可见性策略。
func readAccessFor(token *APIToken) ReadAccess {
	return ReadAccess{AllowSensitive: token != nil && token.AllowSensitive}
}

// allowRequest 每 IP 令牌桶限流（10/s，突发 30）。
func (svc *DataAPIService) allowRequest(ip string) bool {
	svc.rateMu.Lock()
	defer svc.rateMu.Unlock()
	bucket, ok := svc.rateMap[ip]
	if !ok {
		if len(svc.rateMap) > 4096 {
			svc.rateMap = map[string]*dataAPIRateBucket{}
		}
		bucket = &dataAPIRateBucket{tokens: dataAPIRateBurst, last: time.Now()}
		svc.rateMap[ip] = bucket
	}
	now := time.Now()
	bucket.tokens += now.Sub(bucket.last).Seconds() * dataAPIRatePerSec
	if bucket.tokens > dataAPIRateBurst {
		bucket.tokens = dataAPIRateBurst
	}
	bucket.last = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ── 响应与审计 ────────────────────────────────────────────

func (svc *DataAPIService) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 写统一错误契约；绝不泄露堆栈、路径或 SQL 原文。
func (svc *DataAPIService) writeError(w http.ResponseWriter, err *dataAPIError) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(err.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Code, "message": err.Message})
}

// emitAudit 非阻塞投递审计事件；通道满时丢弃并计数（REL-02）。
// 与 Close 通过读写锁互斥，已关闭后直接丢弃，绝不向已关闭通道发送。
func (svc *DataAPIService) emitAudit(event APIAuditEvent) {
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

// auditEventFor 从请求构造审计事件骨架；成功路径由 handler 补全状态与计数。
func auditEventFor(r *http.Request, token *APIToken, operation string, failed *dataAPIError) APIAuditEvent {
	event := APIAuditEvent{
		Timestamp: time.Now().UTC(),
		Channel:   TokenChannelHTTP,
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
func (svc *DataAPIService) auditWriterLoop() {
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
			svc.logger.Warn("data_api_audit_write_failed", map[string]any{"error": err.Error(), "dropped": len(batch)})
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
