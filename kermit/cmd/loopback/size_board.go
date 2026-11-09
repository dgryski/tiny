//go:build tinygo && !virt

package main

// Small boards have 32 KB of RAM.
const maxFile = 12 * 1024
