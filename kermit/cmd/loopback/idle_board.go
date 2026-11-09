//go:build tinygo && !virt

package main

import "time"

// Sleeping between polls saves power on a board.
func idle() { time.Sleep(time.Millisecond) }
