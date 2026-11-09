package kermit

import (
	"bytes"
	"testing"
)

type scripted struct {
	in  []byte
	pos int
}

func (s *scripted) Read(p []byte) (int, error) {
	if s.pos >= len(s.in) {
		return 0, errEOF
	}
	n := copy(p, s.in[s.pos:])
	s.pos += n
	return n, nil
}
func (s *scripted) Write(p []byte) (int, error) { return len(p), nil }

var errEOF = ErrTimeout

func frameBytes(seq uint8, typ byte, data []byte) []byte {
	var b bytes.Buffer
	_ = EncodePacket(&scriptedBuf{&b}, &Packet{Seq: seq, Type: typ, Data: data})
	return b.Bytes()
}

type scriptedBuf struct{ *bytes.Buffer }

func (scriptedBuf) Read([]byte) (int, error) { return 0, nil }

func TestReceiveDoesNotAllocate(t *testing.T) {
	var in []byte
	in = append(in, frameBytes(0, 'S', nil)...)
	in = append(in, frameBytes(1, 'F', []byte("a"))...)
	for i := 0; i < 20; i++ {
		in = append(in, frameBytes(uint8(2+i), 'D', []byte("hello#J world"))...)
	}
	in = append(in, frameBytes(22, 'Z', nil)...)
	in = append(in, frameBytes(23, 'B', nil)...)

	e := NewEngine(func(string) error { return nil }, func([]byte) error { return nil })
	dev := &scripted{in: in}
	allocs := testing.AllocsPerRun(20, func() {
		dev.pos = 0
		if err := e.ProcessStream(dev); err != nil {
			t.Fatal(err)
		}
	})
	// The filename string is one allocation.
	if allocs > 1 {
		t.Fatalf("ProcessStream allocated %v times per run", allocs)
	}
}

func TestSendDoesNotAllocate(t *testing.T) {
	var e Engine
	params := e.initParams()
	in := frameBytes(0, 'Y', params[:])
	for i := 1; i < 200; i++ {
		in = append(in, frameBytes(uint8(i)&63, 'Y', nil)...)
	}
	// About 65 packets, so a per packet allocation shows.
	content := bytes.Repeat([]byte("binary\x00\x01\xff#data"), 400)
	dev := &scripted{in: in}

	allocs := testing.AllocsPerRun(20, func() {
		dev.pos = 0
		if err := e.Send(dev, "file", content); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Send allocated %v times per run", allocs)
	}
}

func TestRemoteErrorDoesNotAllocate(t *testing.T) {
	var e Engine
	params := e.initParams()

	recvIn := append(frameBytes(0, 'S', nil), frameBytes(1, 'E', []byte("disk full"))...)
	recvDev := &scripted{in: recvIn}
	sendIn := append(frameBytes(0, 'Y', params[:]), frameBytes(1, 'E', []byte("disk full"))...)
	sendDev := &scripted{in: sendIn}

	allocs := testing.AllocsPerRun(20, func() {
		recvDev.pos = 0
		var re *RemoteError
		if err := e.ProcessStream(recvDev); !asRemote(err, &re) || string(re.Msg) != "disk full" {
			t.Fatalf("ProcessStream returned %v", err)
		}
		sendDev.pos = 0
		if err := e.Send(sendDev, "f", []byte("x")); !asRemote(err, &re) || string(re.Msg) != "disk full" {
			t.Fatalf("Send returned %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("an error packet allocated %v times per run", allocs)
	}
}

func asRemote(err error, re **RemoteError) bool {
	r, ok := err.(*RemoteError)
	*re = r
	return ok
}
