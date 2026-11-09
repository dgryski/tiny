//go:build !tinygo

package kermit_test

import "testing"

// The fuzz targets. The checks and their seeds are in seeds_test.go.

func FuzzProcessStream(f *testing.F) {
	for _, in := range seedsProcessStream {
		f.Add(in)
	}
	f.Fuzz(checkProcessStream)
}

func FuzzDecodePacket(f *testing.F) {
	for _, in := range seedsDecodePacket {
		f.Add(in)
	}
	f.Fuzz(checkDecodePacket)
}

func FuzzQuoteRoundTrip(f *testing.F) {
	for _, in := range seedsQuoteRoundTrip {
		f.Add(in)
	}
	f.Fuzz(checkQuoteRoundTrip)
}

func FuzzSendReceive(f *testing.F) {
	for _, s := range seedsSendReceive {
		f.Add(s.name, s.content)
	}
	f.Fuzz(checkSendReceive)
}

func FuzzSendCancel(f *testing.F) {
	for _, s := range seedsSendCancel {
		f.Add(s.name, s.content, s.cancelAt)
	}
	f.Fuzz(checkSendCancel)
}
