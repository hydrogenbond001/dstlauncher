//go:build !windows

package main

const traySupported = false

func trayRun(url, dataDir string, quit func()) {}
