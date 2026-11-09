package kermit_test

// The fuzz checks and their seeds. TestSeeds runs them as plain tests since
// TinyGo cannot fuzz.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/dgryski/tiny/kermit"
	"github.com/dgryski/tiny/kermit/internal/testutil"
)

func validTransfer() []byte {
	var b []byte
	b = append(b, rawFrame(0, 'S', "")...)
	b = append(b, rawFrame(1, 'F', "a.txt")...)
	b = append(b, rawFrame(2, 'D', "hello#J#Mworld##")...)
	b = append(b, rawFrame(3, 'Z', "")...)
	b = append(b, rawFrame(4, 'B', "")...)
	return b
}

var seedsProcessStream = [][]byte{
	validTransfer(),
	[]byte{},
	[]byte{1, 0xFF, 0xFF, 'S', 0},
	append([]byte("noise"), rawFrame(0, 'S', "")...),
	rawFrame(5, 'D', "no init"),
	append(rawFrame(0, 'S', ""), rawFrame(0, 'S', "")...),
	[]byte("\x01, S18\x01\x8a!7 (%9"),
}

// checkProcessStream feeds arbitrary bytes to the receiver, which must not
// panic or hang.
func checkProcessStream(t *testing.T, in []byte) {
	m := &mockSerial{in: bytes.NewBuffer(in), out: &bytes.Buffer{}}
	e := kermit.NewEngine(
		func(name string) error { return nil },
		func(p []byte) error {
			if len(p) > kermit.MaxData {
				t.Fatalf("OnData got %d bytes", len(p))
			}
			return nil
		},
	)
	_ = e.ProcessStream(m)

	// Output must decode as ACK, NAK or error.
	for bytes.IndexByte(m.out.Bytes(), kermit.Mark) >= 0 {
		p, err := kermit.DecodePacket(m.out)
		if err != nil {
			t.Fatalf("receiver wrote an invalid packet: %v", err)
		}
		switch p.Type {
		case kermit.TypeACK, kermit.TypeNAK, kermit.TypeError:
		default:
			t.Fatalf("receiver wrote unexpected type %q", p.Type)
		}
		if p.Seq > 63 {
			t.Fatalf("bad sequence number %d", p.Seq)
		}
	}
}

var seedsDecodePacket = [][]byte{
	rawFrame(3, 'D', "hello"),
	rawFrame(0, 'S', ""),
	[]byte{1, 0x20, 0x20, 0x20, 0x20},
	[]byte{1, 0x7E, 0x20, 'D'},
	[]byte("\x01(oD)&)l#E"),
}

// checkDecodePacket checks that an accepted packet re-encodes to the bytes read.
func checkDecodePacket(t *testing.T, in []byte) {
	p, err := kermit.DecodePacket(&mockSerial{in: bytes.NewBuffer(in), out: &bytes.Buffer{}})
	if err != nil {
		return
	}
	if len(p.Data) > kermit.MaxData || p.Seq > 63 {
		t.Fatalf("out of range packet %+v", p)
	}
	var out bytes.Buffer
	if err := kermit.EncodePacket(&mockSerial{out: &out}, p); err != nil {
		t.Fatal(err)
	}
	// The decoder skips leading noise.
	if !bytes.Contains(in, out.Bytes()) {
		t.Fatalf("re-encoded frame %q not found in input %q", out.Bytes(), in)
	}
}

var seedsQuoteRoundTrip = [][]byte{
	[]byte("plain"),
	[]byte{0, 1, 31, 127, 128, 0x9F, 0xFF, '#', 0xA3, '&', '~'},
}

// checkQuoteRoundTrip checks that quoting then unquoting returns the input
// and that quoted output contains no raw control characters.
func checkQuoteRoundTrip(t *testing.T, in []byte) {
	q := kermit.Quote(nil, in)
	for _, c := range q {
		if a := c & 0x7F; a < 32 || a == 127 {
			t.Fatalf("raw control byte %#x", c)
		}
	}
	got := kermit.Unquote(append([]byte(nil), q...))
	if !bytes.Equal(got, in) {
		t.Fatalf("round trip: got %x want %x", got, in)
	}
}

var seedsSendReceive = []struct {
	name    string
	content []byte
}{
	{"a.txt", []byte("hello world\n")},
	{"b.bin", testutil.AllBytes()},
	{"c.gz", []byte{0x1f, 0x8b, 8, 0, 0xff, '#', '#', 0x23 | 0x80}},
	{"empty", []byte{}},
}

// checkSendReceive sends content, replays it into the receiver and expects it back.
func checkSendReceive(t *testing.T, name string, content []byte) {
	// The quoted name must fit the default 77 byte data field.
	if len(name) == 0 || len(kermit.Quote(nil, []byte(name))) > 77 {
		t.Skip()
	}
	dev := &ackDevice{}
	if err := kermit.NewEngine(nil, nil).Send(dev, name, content); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var gotName string
	var got bytes.Buffer
	recv := kermit.NewEngine(
		func(n string) error { gotName = n; return nil },
		func(p []byte) error { got.Write(p); return nil },
	)
	m := &mockSerial{in: &dev.wrote, out: &bytes.Buffer{}}
	if err := recv.ProcessStream(m); err != nil && err != io.EOF {
		t.Fatalf("ProcessStream: %v", err)
	}
	if gotName != name || !bytes.Equal(got.Bytes(), content) {
		t.Fatalf("name %q/%q content %x/%x", gotName, name, got.Bytes(), content)
	}
}

var seedsSendCancel = []struct {
	name     string
	content  []byte
	cancelAt uint8
}{
	{"a.txt", []byte("hello world"), uint8(0)},
	{"b", testutil.AllBytes(), uint8(3)},
	{"c", bytes.Repeat([]byte("x"), 500), uint8(7)},
	{"d", []byte{}, uint8(2)},
}

// checkSendCancel cancels after some packets. Send must finish or tell the peer.
func checkSendCancel(t *testing.T, name string, content []byte, cancelAt uint8) {
	if len(name) == 0 || len(kermit.Quote(nil, []byte(name))) > 77 {
		t.Skip()
	}
	dev := &ackDevice{}
	e := kermit.NewEngine(nil, nil)
	e.Cancel = func() bool { return len(packets(dev.wrote.Bytes())) >= int(cancelAt) }

	err := e.Send(dev, name, content)
	wrote := dev.wrote.Bytes()

	var got bytes.Buffer
	recv := kermit.NewEngine(nil, func(p []byte) error { got.Write(p); return nil })
	rerr := recv.ProcessStream(&mockSerial{in: bytes.NewBuffer(append([]byte(nil), wrote...)), out: &bytes.Buffer{}})

	switch {
	case err == nil:
		if rerr != nil || !bytes.Equal(got.Bytes(), content) {
			t.Fatalf("finished, but the receiver got %d bytes, err %v", got.Len(), rerr)
		}
	case errors.Is(err, kermit.ErrCanceled):
		last := lastPacket(wrote)
		if last == nil {
			return
		}
		if last.Type == 'E' {
			var re *kermit.RemoteError
			if !errors.As(rerr, &re) {
				t.Fatalf("receiver err = %v, want the sender's error packet", rerr)
			}
			return
		}
		// A cancel after the Break changes nothing for the receiver.
		if last.Type == 'B' {
			if rerr != nil || !bytes.Equal(got.Bytes(), content) {
				t.Fatalf("Break was sent but the receiver got %d bytes, err %v", got.Len(), rerr)
			}
			return
		}
		if last.Type != 'S' || len(packets(wrote)) != 1 {
			t.Fatalf("canceled but the last packet is %c of %d", last.Type, len(packets(wrote)))
		}
	default:
		t.Fatalf("unexpected error %v", err)
	}
}

func TestSeeds(t *testing.T) {
	for i, in := range seedsProcessStream {
		t.Run(fmt.Sprintf("ProcessStream/%d", i), func(t *testing.T) { checkProcessStream(t, in) })
	}
	for i, in := range seedsDecodePacket {
		t.Run(fmt.Sprintf("DecodePacket/%d", i), func(t *testing.T) { checkDecodePacket(t, in) })
	}
	for i, in := range seedsQuoteRoundTrip {
		t.Run(fmt.Sprintf("QuoteRoundTrip/%d", i), func(t *testing.T) { checkQuoteRoundTrip(t, in) })
	}
	for i, s := range seedsSendReceive {
		t.Run(fmt.Sprintf("SendReceive/%d", i), func(t *testing.T) { checkSendReceive(t, s.name, s.content) })
	}
	for i, s := range seedsSendCancel {
		t.Run(fmt.Sprintf("SendCancel/%d", i), func(t *testing.T) { checkSendCancel(t, s.name, s.content, s.cancelAt) })
	}
}
