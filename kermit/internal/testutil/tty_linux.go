//go:build !tinygo && linux

package testutil

import "syscall"

const (
	ioctlGetTermios = syscall.TCGETS
	ioctlSetTermios = syscall.TCSETS // does not wait for output to drain
)
