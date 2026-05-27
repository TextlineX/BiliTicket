//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

func showFatalIfNoConsole(title, text string) {
	if hasConsoleWindow() {
		return
	}
	showMessageBox(title, text)
}

func hasConsoleWindow() bool {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	p := k32.NewProc("GetConsoleWindow")
	hwnd, _, _ := p.Call()
	return hwnd != 0
}

func showMessageBox(title, text string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	p := user32.NewProc("MessageBoxW")

	t, _ := syscall.UTF16PtrFromString(text)
	ti, _ := syscall.UTF16PtrFromString(title)

	const (
		mbOK        = 0x00000000
		mbIconError = 0x00000010
	)

	_, _, _ = p.Call(
		0,
		uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(ti)),
		mbOK|mbIconError,
	)
}

