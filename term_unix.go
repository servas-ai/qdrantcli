//go:build linux || darwin

package main

import (
	"syscall"
	"unsafe"
)

// disableEcho schaltet das Terminal-Echo auf stdin ab und liefert eine Restore-Funktion.
func disableEcho() func() {
	fd := uintptr(syscall.Stdin)
	var old syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(ioctlGet), uintptr(unsafe.Pointer(&old))); e != 0 {
		return func() {}
	}
	n := old
	n.Lflag &^= syscall.ECHO
	n.Lflag |= syscall.ICANON
	syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(ioctlSet), uintptr(unsafe.Pointer(&n)))
	return func() { syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(ioctlSet), uintptr(unsafe.Pointer(&old))) }
}
