package clipvault

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot images without browser cookies, credentials or script execution.
// The whole capture shares a deadline. Private network access is limited to
// the copied page's own origin; redirects cannot widen that permission.
func newCompositeImageLoader(parent context.Context, rawHTML []byte) (func(string, int) ([]byte, error), func()) {
	base, _ := url.Parse(compositeSourceURL(rawHTML))
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	return func(ref string, limit int) ([]byte, error) {
		ref = trimCompositeReference(ref)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if (filepath.IsAbs(ref) && !strings.HasPrefix(ref, "//")) || isCompositeInlineReference(ref) || strings.HasPrefix(strings.ToLower(ref), "file:") {
			return readCompositeImage(ref, limit)
		}
		u, err := url.Parse(ref)
		if err != nil {
			return nil, err
		}
		if !u.IsAbs() && base != nil {
			u = base.ResolveReference(u)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Hostname() == "" {
			return nil, errors.New("unsupported resource URL")
		}
		allowPrivate := sameCompositeOrigin(base, u)
		transport := &http.Transport{DisableKeepAlives: true, ResponseHeaderTimeout: time.Second}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, candidate := range ips {
				ip := candidate.IP
				if !allowPrivate && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
					last = errors.New("cross-origin private resource blocked")
					continue
				}
				connection, err := (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return connection, nil
				}
				last = err
			}
			if last == nil {
				last = errors.New("resource host has no address")
			}
			return nil, last
		}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Accept", "image/png,image/jpeg,image/gif")
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, errors.New("resource request failed")
		}
		if response.ContentLength > int64(limit) {
			return nil, ErrImageLimitExceeded
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
		if err != nil {
			return nil, err
		}
		if len(data) > limit {
			return nil, ErrImageLimitExceeded
		}
		if _, err = compositeImageMediaType(data); err != nil {
			return nil, err
		}
		return data, nil
	}, cancel
}

func sameCompositeOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || a.User != nil || (a.Scheme != "http" && a.Scheme != "https") {
		return false
	}
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
