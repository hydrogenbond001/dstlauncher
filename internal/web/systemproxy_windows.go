//go:build windows

package web

import "golang.org/x/sys/windows/registry"

// systemProxyURL reads the current user's WinINET proxy (the same setting
// Chrome/Edge normally use). PAC/autoconfig URLs are not evaluated here.
func systemProxyURL() string {
	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return ""
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	if err != nil || enabled == 0 {
		return ""
	}
	server, _, err := key.GetStringValue("ProxyServer")
	if err != nil {
		return ""
	}
	return normalizeProxyServer(server)
}
