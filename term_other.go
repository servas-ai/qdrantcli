//go:build !linux && !darwin && !windows

package main

func disableEcho() func() { return func() {} }
