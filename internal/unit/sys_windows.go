//go:build windows

package unit

import (
	"os/exec"
	"strconv"
	"syscall"
)

const (
	createNewProcessGroup = 0x00000200
	// CREATE_NO_WINDOW: the child is a console app, but must not allocate a
	// console window. Without it a GUI-subsystem panel spawns a visible black
	// box for every frpc/frps/DST process.
	createNoWindow = 0x08000000
)

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup,
	}
}

// Batch files start the real server as a grandchild, so kill the whole tree.
func signalStop(pid int) error {
	return taskkill(pid)
}

func killTree(pid int) error {
	return taskkill(pid)
}

func taskkill(pid int) error {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	cmd.SysProcAttr = sysProcAttr()
	return cmd.Run()
}
