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
)

func validateListenAddr(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", 0, fmt.Errorf("invalid listen address: %w", err)
	}
	if strings.TrimSpace(strings.Trim(host, "[]")) == "" {
		return "", 0, fmt.Errorf("listen host cannot be empty")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1024 || port > 65535 {
		return "", 0, fmt.Errorf("listen port must be between 1024 and 65535")
	}
	return host, port, nil
}

// listenAddrAvailable 探测地址当前是否可绑定，用于保存配置时提前发现端口冲突。
func listenAddrAvailable(host string, port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return ln.Close()
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

func originMatchesEndpoint(origin, scheme, host string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == scheme && strings.EqualFold(u.Host, host) && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func requestUsesHTTPS(r *http.Request) bool {
	return r.TLS != nil
}

func (s *SearchServer) secureHTTP(next http.Handler, listenHost string, port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// blob: 供历史记录的图片预览（createObjectURL）使用，仅允许同源创建的 Blob。
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		if !hostAllowedForListen(r.Host, listenHost, port) {
			writeHTTPSecurityError(w, r, "invalid_host", "invalid host")
			return
		}
		if isMutationMethod(r.Method) {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if !originMatchesEndpoint(r.Header.Get("Origin"), scheme, r.Host) {
				writeHTTPSecurityError(w, r, "invalid_origin", "invalid origin")
				return
			}
			provided := r.Header.Get(csrfHeaderName)
			if subtle.ConstantTimeCompare([]byte(provided), []byte(s.csrfToken)) != 1 {
				w.Header().Set("X-ClipVault-CSRF-Invalid", "1")
				writeHTTPSecurityError(w, r, "csrf_invalid", "CSRF validation failed")
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
