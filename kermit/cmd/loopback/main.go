//go:build tinygo

// Command loopback is the device side of the hardware test. It receives a
// file with Kermit, keeps it in RAM and sends it back under the same name.
package main

import (
	"errors"
	"io"
	"time"

	"github.com/dgryski/tiny/kermit"
)

var (
	store   [maxFile]byte
	storeN  int
	nameBuf [96]byte
	nameN   int

	errFull = errors.New("file too large")
)

type poller struct{ io.ReadWriter }

func (p poller) Read(b []byte) (int, error) {
	n, err := p.ReadWriter.Read(b)
	if n == 0 && err == nil {
		idle()
	}
	return n, err
}

func main() {
	dev := poller{device()}

	recv := kermit.NewEngine(
		func(name string) error {
			nameN = copy(nameBuf[:], name)
			storeN = 0
			return nil
		},
		func(p []byte) error {
			if storeN+len(p) > maxFile {
				return errFull
			}
			storeN += copy(store[storeN:], p)
			return nil
		},
	)

	send := kermit.NewEngine(nil, nil)
	send.Timeout = 2 * time.Second

	for {
		if err := recv.ProcessStream(dev); err != nil {
			continue
		}
		// Let the peer start listening.
		time.Sleep(200 * time.Millisecond)
		_ = send.Send(dev, string(nameBuf[:nameN]), store[:storeN])
	}
}
