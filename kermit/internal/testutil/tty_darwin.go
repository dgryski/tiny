//go:build !tinygo && darwin

package testutil

import "syscall"

const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA // does not wait for output to drain
)
