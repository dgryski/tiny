//go:build tinygo

package rc522

import (
	"machine"
)

type Device struct {
	bus   *machine.SPI
	cs    machine.Pin
	reset machine.Pin
}

func New(bus *machine.SPI, cs, reset machine.Pin) *Device {
	return &Device{bus: bus, cs: cs, reset: reset}
}

func (d *Device) initPins() {
	d.cs.Configure(machine.PinConfig{Mode: machine.PinOutput})
	d.cs.High()
	d.reset.Configure(machine.PinConfig{Mode: machine.PinOutput})
	d.reset.High()
}
