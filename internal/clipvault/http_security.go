package clipvault

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	csrfHeaderName           = "X-ClipVault-CSRF"
	freshSecondaryHeaderName = "X-ClipVault-Fresh-Secondary"
	forwardedProtoHeaderName = "X-Forwarded-Proto"
	forwardedHostHeaderName  = "X-Forwarded-Host"
)

func validateReverseProxyConfig(search SearchConfig) error {
	proxy := search.ReverseProxy
	if proxy.PublicURL != "" {
		if _, err := parseReverseProxyPublicURL(proxy.PublicURL); err != nil {
			return err
		}
	}
	if !proxy.Enabled {
		return nil
	}
	if proxy.PublicURL == "" {
		return fmt.Errorf("reverse proxy publicUrl is required when enabled")
	}
	host, _, err := validateListenAddr(search.ListenAddr)
	if err != nil {
		return err
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("reverse proxy mode requires a loopback listen address")
	}
	return nil
}

func parseReverseProxyPublicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("reverse proxy publicUrl must be an absolute https URL without path, query, user info, or fragment")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && ip.IsUnspecified() {
		return nil, fmt.Errorf("reverse proxy publicUrl must identify an address clients can connect to")
	}
	return u, nil
}

func validateListenAddr(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", 0, fmt.Errorf("invalid listen address: %w", err)
	}
	if strings.TrimSpace(strings.Trim(host, "[]")) == "" {
		return "", 0, fmt.Errorf("listen host cannot be empty")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("listen port must be between 1 and 65535")
	}
	return host, port, nil
}

func isUnspecifiedHost(host string) bool {
	ip := net.ParseIP(strings.TrimSpace(strings.Trim(host, "[]")))
	return ip != nil && ip.IsUnspecified()
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func generateCSRFToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func isMutationMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func hostMatchesLoopbackPort(hostport string, port int) bool {
	host, portText, err := net.SplitHostPort(hostport)
	if err != nil || !isLoopbackHost(host) {
		return false
	}
	gotPort, err := strconv.Atoi(portText)
	return err == nil && gotPort == port
}

func isLocalInterfaceIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		var localIP net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			localIP = value.IP
		case *net.IPAddr:
			localIP = value.IP
		}
		if localIP != nil && localIP.Equal(ip) {
			return true
		}
	}
	return false
}

func hostAllowedForListen(hostport, listenHost string, port int) bool {
	host, portText, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	gotPort, err := strconv.Atoi(portText)
	if err != nil || gotPort != port {
		return false
	}
	if isLoopbackHost(host) {
		return true
	}
	requestHost := strings.TrimSpace(strings.Trim(host, "[]"))
	configuredHost := strings.TrimSpace(strings.Trim(listenHost, "[]"))
	if isUnspecifiedHost(configuredHost) {
		return isLocalInterfaceIP(net.ParseIP(requestHost))
	}
	requestIP := net.ParseIP(requestHost)
	configuredIP := net.ParseIP(configuredHost)
	if requestIP != nil && configuredIP != nil {
		return requestIP.Equal(configuredIP)
	}
	return strings.EqualFold(requestHost, configuredHost)
}

func originMatchesRequest(origin, requestHost string, port int) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" {
		return false
	}
	return u.Host == requestHost && hostMatchesLoopbackPort(u.Host, port)
}

func originMatchesAllowedRequest(origin, requestHost, listenHost string, port int) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" {
		return false
	}
	return u.Host == requestHost && hostAllowedForListen(u.Host, listenHost, port)
}

func originMatchesEndpoint(origin, scheme, host string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == scheme && strings.EqualFold(u.Host, host) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

type requestEndpoint struct {
	scheme  string
	host    string
	proxied bool
}

func (s *SearchServer) effectiveRequestEndpoint(r *http.Request) (requestEndpoint, bool) {
	directScheme := "http"
	if r.TLS != nil {
		directScheme = "https"
	}
	forwardedProtoValues := r.Header.Values(forwardedProtoHeaderName)
	forwardedHostValues := r.Header.Values(forwardedHostHeaderName)
	forwardedProto := strings.TrimSpace(r.Header.Get(forwardedProtoHeaderName))
	forwardedHost := strings.TrimSpace(r.Header.Get(forwardedHostHeaderName))
	if forwardedProto == "" && forwardedHost == "" {
		return requestEndpoint{scheme: directScheme, host: r.Host}, true
	}
	if !s.reverseProxy.Enabled || len(forwardedProtoValues) != 1 || len(forwardedHostValues) != 1 ||
		forwardedProto == "" || forwardedHost == "" ||
		strings.ContainsAny(forwardedProto, ", \t") || strings.ContainsAny(forwardedHost, ", \t") {
		return requestEndpoint{}, false
	}
	remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !isLoopbackHost(remoteHost) {
		return requestEndpoint{}, false
	}
	publicURL, err := parseReverseProxyPublicURL(s.reverseProxy.PublicURL)
	if err != nil || forwardedProto != "https" || !strings.EqualFold(forwardedHost, publicURL.Host) {
		return requestEndpoint{}, false
	}
	return requestEndpoint{scheme: "https", host: publicURL.Host, proxied: true}, true
}

func (s *SearchServer) requestUsesHTTPS(r *http.Request) bool {
	endpoint, ok := s.effectiveRequestEndpoint(r)
	return ok && endpoint.scheme == "https"
}

func (s *SearchServer) secureHTTP(next http.Handler, listenHost string, port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		endpoint, endpointOK := s.effectiveRequestEndpoint(r)
		if !endpointOK || (!endpoint.proxied && !hostAllowedForListen(r.Host, listenHost, port)) {
			writeHTTPSecurityError(w, r, "invalid_host", "请求主机无效")
			return
		}
		if isMutationMethod(r.Method) {
			if !originMatchesEndpoint(r.Header.Get("Origin"), endpoint.scheme, endpoint.host) {
				writeHTTPSecurityError(w, r, "invalid_origin", "请求来源无效")
				return
			}
			provided := r.Header.Get(csrfHeaderName)
			if subtle.ConstantTimeCompare([]byte(provided), []byte(s.csrfToken)) != 1 {
				w.Header().Set("X-ClipVault-CSRF-Invalid", "1")
				writeHTTPSecurityError(w, r, "csrf_invalid", "CSRF 验证失败")
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func writeHTTPSecurityError(w http.ResponseWriter, r *http.Request, code, message string) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
		return
	}
	http.Error(w, message, http.StatusForbidden)
}
