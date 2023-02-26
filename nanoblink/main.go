package main

import (
	"machine"
	"time"
)

type buttonState uint8

const (
	buttonOff buttonState = iota
	buttonBlink
	buttonOn
	buttonMAX
)

type blinkState uint8

const (
	blinkReadDelay blinkState = iota
	blinkDelayOn
	blinkDelayOff
)

type blinker struct {
	state blinkState
	pot   machine.ADC
	led   machine.Pin
	delay time.Duration
	end   time.Time
}

func (b *blinker) run() {
	switch b.state {
	case blinkReadDelay:
		rawpot := b.pot.Get()
		freq := float32(rawpot) / float32(1<<16)
		b.delay = time.Duration(1000*freq) * time.Millisecond
		b.end = time.Now().Add(b.delay)
		b.led.High()
		b.state = blinkDelayOn
		fallthrough

	case blinkDelayOn:
		if now := time.Now(); now.After(b.end) {
			b.led.Low()
			b.end = now.Add(b.delay)
			b.state = blinkDelayOff
		}

	case blinkDelayOff:
		if now := time.Now(); now.After(b.end) {
			b.state = blinkReadDelay
		}
	}
}

type debouncer struct {
	b    bool
	prev bool
	last time.Time
}

func (d *debouncer) push(b bool) bool {
	d.b = b

	if d.b != d.prev {
		d.last = time.Now()
	}

	d.prev = b
	return d.prev
}

func (d *debouncer) debounced() (r bool, ok bool) {
	const debounceDelay = 10 * time.Millisecond

	if time.Now().Sub(d.last) < debounceDelay {
		return false, false
	}

	return d.b, true
}

func main() {
	machine.InitADC()

	bled := machine.LED
	bled.Configure(machine.PinConfig{Mode: machine.PinOutput})

	led := machine.D2
	led.Configure(machine.PinConfig{Mode: machine.PinOutput})

	pot := machine.ADC{machine.ADC5}
	pot.Configure(machine.ADCConfig{})

	button := machine.D3
	button.Configure(machine.PinConfig{Mode: machine.PinInput})

	var bState buttonState
	var buttonPressed bool

	blink := blinker{pot: pot, led: led}
	db := debouncer{}

	for {
		if db.push(button.Get()) {
			bled.High()
		} else {
			bled.Low()
		}

		if b, ok := db.debounced(); ok {
			if buttonPressed != b {
				buttonPressed = b

				if !buttonPressed {
					// change led state when button is released
					bState++
					if bState >= buttonMAX {
						bState = 0
					}
				}
			}
		}

		switch bState {
		case buttonOff:
			led.Low()

		case buttonBlink:
			blink.run()

		case buttonOn:
			led.High()
		}
	}
}
