//go:build windows

package main

import (
	_ "embed"
	"os/exec"
	"runtime"

	"github.com/getlantern/systray"
)

//go:embed assets/icon.ico
var trayIcon []byte

const traySupported = true

// trayRun owns the Win32 message loop and must run on the main goroutine.
func trayRun(url, dataDir string, quit func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("dstlauncher  " + url)

		openItem := systray.AddMenuItem("打开面板", url)
		dirItem := systray.AddMenuItem("打开数据目录", dataDir)
		systray.AddSeparator()
		quitItem := systray.AddMenuItem("退出", "停止所有实例并退出")

		go func() {
			for {
				select {
				case <-openItem.ClickedCh:
					_ = openBrowser(url)
				case <-dirItem.ClickedCh:
					_ = exec.Command("explorer.exe", dataDir).Start()
				case <-quitItem.ClickedCh:
					quit()
					systray.Quit()
					return
				}
			}
		}()
	}, func() {})
}
