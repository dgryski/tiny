#!/bin/sh -x -e

tinygo build -print-allocs -gc=none -scheduler=none -target=arduino-nano
avrdude -v -V -patmega328p -carduino "-P/dev/cu.usbserial-14310" -b57600 -D -Uflash:w:blinky
