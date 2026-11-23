package web

import "testing"

func TestNormalizeProxyServer(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"127.0.0.1:7890":        "http://127.0.0.1:7890",
		"http://127.0.0.1:7890": "http://127.0.0.1:7890",
		"http=127.0.0.1:7890;https=127.0.0.1:7891": "http://127.0.0.1:7890",
		"https=127.0.0.1:7891":                     "http://127.0.0.1:7891",
		"socks=127.0.0.1:1080":                     "socks5://127.0.0.1:1080",
		"socks5://127.0.0.1:1080":                  "socks5://127.0.0.1:1080",
	}
	for in, want := range cases {
		if got := normalizeProxyServer(in); got != want {
			t.Errorf("normalizeProxyServer(%q) = %q, want %q", in, got, want)
		}
	}
	if u := parseProxy("127.0.0.1:7890"); u == nil || u.Host != "127.0.0.1:7890" || u.Scheme != "http" {
		t.Errorf("parseProxy plain = %+v", u)
	}
	if u := parseProxy("ftp://x"); u != nil {
		t.Errorf("parseProxy ftp = %+v, want nil", u)
	}
}
