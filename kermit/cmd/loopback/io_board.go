//go:build tinygo && !virt

package main

import (
	"io"
	"machine"
)

// usbSerial adds Read to machine.Serial. It never blocks.
type usbSerial struct{ machine.Serialer }

func (s usbSerial) Read(b []byte) (int, error) {
	n := 0
	for n < len(b) && s.Buffered() > 0 {
		c, err := s.ReadByte()
		if err != nil {
			break
		}
		b[n] = c
		n++
	}
	return n, nil
}

func device() io.ReadWriter {
	machine.Serial.Configure(machine.UARTConfig{})
	return usbSerial{machine.Serial}
}
