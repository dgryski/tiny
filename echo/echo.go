package main

import (
	"image/color"
	"machine"
	"time"

	"tinygo.org/x/drivers/microbitmatrix"
)

type state int

const (
	StateInput state = iota
	StateOutput
	StateOutputDelayOn
	StateOutputDelayOff
)

func main() {
	device := microbitmatrix.New()
	device.Configure(microbitmatrix.Config{})

	left := machine.BUTTONA
	left.Configure(machine.PinConfig{Mode: machine.PinInput})

	right := machine.BUTTONB
	right.Configure(machine.PinConfig{Mode: machine.PinInput})

	var input [256]bool
	var i int
	var o int

	var currentState state = StateInput

	var delayIterations int

	var oldLeft bool
	var oldRight bool

	for {

		switch currentState {

		case StateInput:
			var button bool

			if oldLeft && !left.Get() && oldRight && !right.Get() {
				// both buttons high -> low
				currentState = StateOutput
				oldLeft = false
				oldRight = false
				o = 0
				break
			}

			button = left.Get()
			if oldLeft && !button {
				input[i] = false
				i++
			}
			if !button {
				device.SetPixel(0, 0, color.RGBA{R: 1})
			} else {
				device.SetPixel(0, 0, color.RGBA{})
			}
			oldLeft = button

			button = right.Get()
			if oldRight && !button {
				input[i] = true
				i++
			}
			if !button {
				device.SetPixel(0, 4, color.RGBA{R: 1})
			} else {
				device.SetPixel(0, 4, color.RGBA{})
			}
			oldRight = button

		case StateOutput:
			if o == i {
				currentState = StateInput
				i = 0
				break
			}

			if !input[o] {
				device.SetPixel(0, 0, color.RGBA{R: 1})
				device.SetPixel(0, 4, color.RGBA{})
			} else {
				device.SetPixel(0, 0, color.RGBA{})
				device.SetPixel(0, 4, color.RGBA{R: 1})
			}
			o++
			delayIterations = 10
			currentState = StateOutputDelayOn

		case StateOutputDelayOn:
			if delayIterations == 0 {
				delayIterations = 5
				device.SetPixel(0, 0, color.RGBA{})
				device.SetPixel(0, 4, color.RGBA{})
				currentState = StateOutputDelayOff
				break
			}

			delayIterations--

		case StateOutputDelayOff:
			if delayIterations == 0 {
				currentState = StateOutput
				break
			}

			delayIterations--
		}

		for i := 0; i < 10; i++ {
			device.Display()
			time.Sleep(5 * time.Millisecond)
		}
	}
}
