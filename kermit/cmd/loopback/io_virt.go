//go:build virt

package main

import (
	"io"
	"runtime/volatile"
	"unsafe"
)

// The virt machine has an ns16550a UART here, polled directly.
const uartBase = 0x10000000

type ns16550 struct{}

func reg(off uintptr) *volatile.Register8 {
	return (*volatile.Register8)(unsafe.Pointer(uartBase + off))
}

const (
	regData   = 0
	regStatus = 5    // line status
	dataReady = 0x01 // a byte is waiting in the receive register
	txEmpty   = 0x20 // the transmit register is empty
)

func (ns16550) Read(b []byte) (int, error) {
	n := 0
	for n < len(b) && reg(regStatus).Get()&dataReady != 0 {
		b[n] = reg(regData).Get()
		n++
	}
	return n, nil
}

func (ns16550) Write(b []byte) (int, error) {
	for _, c := range b {
		for reg(regStatus).Get()&txEmpty == 0 {
		}
		reg(regData).Set(c)
	}
	return len(b), nil
}

func device() io.ReadWriter { return ns16550{} }
