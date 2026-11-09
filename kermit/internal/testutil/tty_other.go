//go:build !tinygo && !darwin && !linux

package testutil

import "testing"

// Serial ports are only supported on macOS and Linux.
type TTY struct{}

func OpenTTY(t *testing.T, path string) *TTY {
	t.Skip("serial ports are only supported on macOS and Linux")
	return nil
}

func (*TTY) Read([]byte) (int, error)  { return 0, nil }
func (*TTY) Write([]byte) (int, error) { return 0, nil }
func (*TTY) Drain()                    {}
