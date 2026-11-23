//go:build !windows

package web

func systemProxyURL() string { return "" }
