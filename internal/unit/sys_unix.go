//go:build !windows

package unit

import "syscall"

func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// Children run in their own process group, so signal the whole tree.
func signalStop(pid int) error {
	return syscall.Kill(-pid, syscall.SIGTERM)
}

func killTree(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
