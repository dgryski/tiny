package main

import (
	"image/color"
	"time"

	"tinygo.org/x/drivers/microbitmatrix"
)

func main() {
	device := microbitmatrix.New()
	device.Configure(microbitmatrix.Config{})

	for {
		r, c := int16(rand16()%5), int16(rand16()%5)

		if device.GetPixel(r, c) {
			device.SetPixel(r, c, color.RGBA{})
		} else {
			device.SetPixel(r, c, color.RGBA{R: 1})
		}

		for i := 0; i < 4; i++ {
			device.Display()
			time.Sleep(5 * time.Millisecond)
		}

	}
}

var seed16 uint16 = 1

func rand16() uint16 {
	seed16 ^= seed16 << 7
	seed16 ^= seed16 >> 9
	seed16 ^= seed16 << 8
	return seed16
}
