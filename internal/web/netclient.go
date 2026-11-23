package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// externalClient builds an HTTP client for Steam/web requests. Priority:
// panel proxy setting -> environment proxy -> Windows system proxy.
func (s *Server) externalClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
	}
	raw := strings.TrimSpace(s.paths.Settings.Proxy)
	if raw == "" {
		raw = systemProxyURL()
	}
	if u := parseProxy(raw); u != nil {
		tr.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func parseProxy(raw string) *url.URL {
	raw = normalizeProxyServer(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return u
	default:
		return nil
	}
}

// normalizeProxyServer accepts both a plain host:port and the Windows
// per-protocol form "http=host:port;https=host:port".
func normalizeProxyServer(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	if strings.Contains(server, "=") {
		var httpProxy, httpsProxy, socksProxy string
		for _, part := range strings.Split(server, ";") {
			k, v, ok := strings.Cut(part, "=")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "http":
				httpProxy = withScheme(v, "http")
			case "https":
				httpsProxy = withScheme(v, "http")
			case "socks", "socks5":
				socksProxy = withScheme(v, "socks5")
			}
		}
		if httpProxy != "" {
			return httpProxy
		}
		if httpsProxy != "" {
			return httpsProxy
		}
		if socksProxy != "" {
			return socksProxy
		}
	}
	return withScheme(server, "http")
}

func withScheme(v, def string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if strings.Contains(v, "://") {
		return v
	}
	return def + "://" + v
}
