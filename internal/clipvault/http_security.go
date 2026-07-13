package clipvault

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
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

func (s *SearchServer) secureHTTP(next http.Handler, listenHost string, port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		if !hostAllowedForListen(r.Host, listenHost, port) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if isMutationMethod(r.Method) {
			if !originMatchesAllowedRequest(r.Header.Get("Origin"), r.Host, listenHost, port) {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
			provided := r.Header.Get(csrfHeaderName)
			if subtle.ConstantTimeCompare([]byte(provided), []byte(s.csrfToken)) != 1 {
				w.Header().Set("X-ClipVault-CSRF-Invalid", "1")
				http.Error(w, "csrf validation failed", http.StatusForbidden)
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
