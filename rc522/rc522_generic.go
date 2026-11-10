//go:build !tinygo

package rc522

type machineSPI interface {
	Transfer(byte) (byte, error)
}

// machinePin lets tests observe pin changes through onChange.
type machinePin struct {
	onChange func(high bool)
}

func (p machinePin) High() {
	if p.onChange != nil {
		p.onChange(true)
	}
}

func (p machinePin) Low() {
	if p.onChange != nil {
		p.onChange(false)
	}
}

type Device struct {
	bus   machineSPI
	cs    machinePin
	reset machinePin
}

func New(bus machineSPI, cs, reset machinePin) *Device {
	return &Device{bus: bus, cs: cs, reset: reset}
}

func (d *Device) initPins() {}
