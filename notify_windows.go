//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var procMessageBox = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

// notifyFatal is the only visible output in a windowsgui build before the
// panel exists; without it a bad port or data directory fails silently.
func notifyFatal(msg string) {
	text, err1 := syscall.UTF16PtrFromString(msg)
	caption, err2 := syscall.UTF16PtrFromString("dstlauncher")
	if err1 != nil || err2 != nil {
		return
	}
	const mbIconError = 0x00000010
	_, _, _ = procMessageBox.Call(0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(caption)),
		mbIconError)
}
