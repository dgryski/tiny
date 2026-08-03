//go:build !tinygo

package rc522

type machineSPI interface {
	Transfer(byte) (byte, error)
}

type machinePin struct{}

func (machinePin) High() {}
func (machinePin) Low()  {}

type Device struct {
	bus   machineSPI
	cs    machinePin
	reset machinePin
}

func New(bus machineSPI, cs, reset machinePin) *Device {
	return &Device{bus: bus, cs: cs, reset: reset}
}

func (d *Device) initPins() {}
