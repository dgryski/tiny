package kermit_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/dgryski/tiny/kermit"
)

// repeat is a line that never goes quiet. Read returns the bytes of in over
// and over and Write discards.
type repeat struct {
	in  []byte
	pos int
}

func (r *repeat) Read(b []byte) (int, error) {
	b[0] = r.in[r.pos%len(r.in)]
	r.pos++
	return 1, nil
}

func (r *repeat) Write(b []byte) (int, error) { return len(b), nil }

func packetBytes(t *testing.T, seq uint8, typ byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := kermit.EncodePacket(&buf, &kermit.Packet{Seq: seq, Type: typ}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// finishes fails the test if f is still running after 3 seconds.
func finishes(t *testing.T, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("still running after 3s")
		return nil
	}
}

func TestReceiveTimesOutOnNoise(t *testing.T) {
	e := kermit.NewEngine(nil, nil)
	e.Timeout = 100 * time.Millisecond
	err := finishes(t, func() error { return e.ProcessStream(&repeat{in: []byte("x")}) })
	if !errors.Is(err, kermit.ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
}

func TestSendTimesOutOnStaleACKs(t *testing.T) {
	e := kermit.NewEngine(nil, nil)
	e.Timeout = 100 * time.Millisecond
	line := &repeat{in: packetBytes(t, 5, 'Y')}
	err := finishes(t, func() error { return e.Send(line, "f", []byte("x")) })
	if !errors.Is(err, kermit.ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
}

func TestReceiveGivesUpOnRepeatedPacket(t *testing.T) {
	// The Send-Init is accepted once, then every copy is a duplicate.
	e := kermit.NewEngine(nil, nil)
	line := &repeat{in: packetBytes(t, 0, 'S')}
	err := finishes(t, func() error { return e.ProcessStream(line) })
	if !errors.Is(err, kermit.ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
}
