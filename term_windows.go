//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

const enableEchoInput = 0x0004

// disableEcho schaltet das Konsolen-Echo ab und liefert eine Restore-Funktion.
func disableEcho() func() {
	h, err := syscall.GetStdHandle(syscall.STD_INPUT_HANDLE)
	if err != nil {
		return func() {}
	}
	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return func() {}
	}
	procSetConsoleMode.Call(uintptr(h), uintptr(mode&^enableEchoInput))
	return func() { procSetConsoleMode.Call(uintptr(h), uintptr(mode)) }
}
