package main

import (
	"machine"
	"time"

	"github.com/dgryski/tiny/hd"
	"github.com/dgryski/tiny/rc522"
)

var led = machine.LED

func init() {
	led.Configure(machine.PinConfig{Mode: machine.PinOutput})
}

func blink1() {
	led.Low()
	time.Sleep(time.Millisecond * 500)

	led.High()
	time.Sleep(time.Millisecond * 500)
}

func blink(n int) {
	for range n {
		blink1()
	}
}

var DefaultKey = [6]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}

func main() {

	println("startup..")

	blink(3)

	println("spi configure")
	machine.SPI0.Configure(machine.SPIConfig{
		Frequency: 4000000, // 4MHz
		Mode:      0,
	})

	csPin := machine.D4
	rstPin := machine.D1

	println("rc522.new")
	rfid := rc522.New(machine.SPI0, csPin, rstPin)
	println("rfid.init")
	err := rfid.Init()
	if err != nil {
		println("Failed to init:", err)
	}

	println("turning on antenna")
	rfid.AntennaOn()
	println("place tag")

	for {
		uuid, err := rfid.ReadUID()
		if err == nil {
			var hx hd.Dumper
			hx.Dump(uuid)
			hx.SetOffset(0)
			for block := byte(0); block < 64; block++ {
				err := rfid.Authenticate(rc522.AuthKeyA, block, DefaultKey[:], uuid)
				if err != nil {
					println("auth error:", err)
					break
				}
				data, err := rfid.ReadBlock(block)
				if err != nil {
					println("error during read block:", err)
					break
				}
				hx.Dump(data)
			}
			rfid.HaltA()
			rfid.StopCrypto()
		} else {
			println("read uid error:", err)

		}
		time.Sleep(500 * time.Millisecond)
	}
}
