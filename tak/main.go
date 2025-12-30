package main

import (
	"machine"
	"time"
)

/*
From https://www.ulisp.com/show?1EO1

(defun tak (x y z)
  (if (not (< y x))
      z
    (tak
     (tak (1- x) y z)
     (tak (1- y) z x)
     (tak (1- z) x y))))
*/

func tak(x, y, z int) int {
	if x <= y {
		return z
	}

	return tak(
		tak(x-1, y, z),
		tak(y-1, z, x),
		tak(z-1, x, y),
	)
}

func main() {

	var total int
	var nanos time.Duration

	const rounds = 100

	for range rounds {
		t0 := time.Now()
		total += tak(18, 12, 6)
		nanos += time.Since(t0)
	}

	println(total, nanos/rounds)

	blink()
}

func blink() {
	led := machine.LED
	led.Configure(machine.PinConfig{Mode: machine.PinOutput})
	for {
		led.Low()
		time.Sleep(time.Millisecond * 500)

		led.High()
		time.Sleep(time.Millisecond * 500)
	}
}
