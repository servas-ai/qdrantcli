//go:build linux

package main

import "syscall"

const ioctlGet, ioctlSet = syscall.TCGETS, syscall.TCSETS
