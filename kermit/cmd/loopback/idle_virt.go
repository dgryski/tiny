//go:build virt

package main

// Sleeping is very slow in the emulator, so poll without it.
func idle() {}
