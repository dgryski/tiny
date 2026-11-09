package kermit_test

// Tests of packet encoding, quoting and the engine state machine.

import (
	"bytes"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dgryski/tiny/kermit"
	"github.com/dgryski/tiny/kermit/internal/testutil"
)

type mockSerial struct {
	in  *bytes.Buffer
	out *bytes.Buffer
}

func (m *mockSerial) Read(p []byte) (int, error) { return m.in.Read(p) }

func (m *mockSerial) Write(p []byte) (int, error) { return m.out.Write(p) }

func frame(t *testing.T, seq uint8, typ byte, data string) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := kermit.EncodePacket(&b, &kermit.Packet{Seq: seq, Type: typ, Data: []byte(data)}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func rawFrame(seq uint8, typ byte, data string) []byte {
	var b bytes.Buffer
	_ = kermit.EncodePacket(&mockSerial{out: &b}, &kermit.Packet{Seq: seq, Type: typ, Data: []byte(data)})
	return b.Bytes()
}

// ackDevice ACKs every packet in order and records what the sender wrote.
type ackDevice struct {
	wrote   bytes.Buffer
	pending bytes.Buffer
	seq     uint8
	params  string // data for the ACK to the Send-Init
}

func (d *ackDevice) Write(p []byte) (int, error) { return d.wrote.Write(p) }

func (d *ackDevice) Read(p []byte) (int, error) {
	if d.pending.Len() == 0 {
		data := ""
		if d.seq == 0 {
			data = d.params
		}
		d.pending.Write(rawFrame(d.seq, 'Y', data))
		d.seq = (d.seq + 1) & 63
	}
	return d.pending.Read(p)
}

func TestChecksumKnownVector(t *testing.T) {
	// LEN='#' SEQ=' ' TYPE='S', sum=150, (150+2)&63+32 = '8'
	got := frame(t, 0, 'S', "")
	want := []byte{0x01, '#', ' ', 'S', '8'}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	data := string(bytes.Repeat([]byte("xyz"), 31))
	b := bytes.NewBuffer(frame(t, 5, 'D', data[:kermit.MaxData]))
	p, err := kermit.DecodePacket(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Seq != 5 || p.Type != 'D' || string(p.Data) != data[:kermit.MaxData] {
		t.Fatalf("bad packet %+v", p)
	}
}

func TestDecodeSkipsNoise(t *testing.T) {
	in := append([]byte("garbage"), frame(t, 1, 'Y', "")...)
	p, err := kermit.DecodePacket(bytes.NewBuffer(in))
	if err != nil || p.Type != 'Y' {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

func TestDecodeErrors(t *testing.T) {
	bad := frame(t, 0, 'D', "hello")
	bad[len(bad)-1] ^= 1
	if _, err := kermit.DecodePacket(bytes.NewBuffer(bad)); !errors.Is(err, kermit.ErrChecksum) {
		t.Fatalf("want checksum error, got %v", err)
	}
	if _, err := kermit.DecodePacket(bytes.NewBuffer([]byte{1, 5, ' ', 'D', 'x'})); !errors.Is(err, kermit.ErrMalformed) {
		t.Fatalf("want malformed error, got %v", err)
	}
}

func TestEncodeTooLong(t *testing.T) {
	err := kermit.EncodePacket(&mockSerial{out: &bytes.Buffer{}}, &kermit.Packet{Type: 'D', Data: make([]byte, kermit.MaxData+1)})
	if !errors.Is(err, kermit.ErrTooLong) {
		t.Fatalf("got %v", err)
	}
}

func TestEngineTransfer(t *testing.T) {
	var in bytes.Buffer
	in.Write(frame(t, 0, 'S', ""))
	in.Write(frame(t, 1, 'F', "a.txt"))
	in.Write(frame(t, 2, 'D', "hello "))
	in.Write(frame(t, 3, 'D', "world"))
	in.Write(frame(t, 4, 'Z', ""))
	in.Write(frame(t, 5, 'B', ""))

	m := &mockSerial{in: &in, out: &bytes.Buffer{}}
	var name string
	var got []byte
	e := kermit.NewEngine(
		func(f string) error { name = f; return nil },
		func(b []byte) error { got = append(got, b...); return nil },
	)
	if err := e.ProcessStream(m); err != nil {
		t.Fatal(err)
	}
	if name != "a.txt" || string(got) != "hello world" {
		t.Fatalf("name=%q data=%q", name, got)
	}
	if e.State() != kermit.StateComplete {
		t.Fatalf("state %v", e.State())
	}
	for i := 0; i < 6; i++ {
		p, err := kermit.DecodePacket(m.out)
		if err != nil || p.Type != 'Y' || int(p.Seq) != i {
			t.Fatalf("reply %d: %+v %v", i, p, err)
		}
	}
}

func TestEngineEOFReturns(t *testing.T) {
	m := &mockSerial{in: &bytes.Buffer{}, out: &bytes.Buffer{}}
	e := kermit.NewEngine(nil, nil)
	if err := e.ProcessStream(m); err == nil {
		t.Fatal("expected error on EOF")
	}
	if m.out.Len() != 0 {
		t.Fatalf("unexpected output %q", m.out.Bytes())
	}
}

func TestEngineNAKsBadChecksum(t *testing.T) {
	bad := frame(t, 0, 'S', "")
	bad[len(bad)-1] ^= 1
	var in bytes.Buffer
	in.Write(bad)
	m := &mockSerial{in: &in, out: &bytes.Buffer{}}
	e := kermit.NewEngine(nil, nil)
	_ = e.ProcessStream(m)
	p, err := kermit.DecodePacket(m.out)
	if err != nil || p.Type != 'N' {
		t.Fatalf("p=%+v err=%v", p, err)
	}
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"controls", "a#Jb##c#M", "a\nb#c\r"},
		{"printable after prefix is literal", "#a#b#!", "ab!"},
		{"control code A", "#A", "\x01"},
		{"delete", "#?", "\x7f"},
		{"underscore is a control code", "#_", "\x1f"},
		{"other prefix characters are literal", "#~#&", "~&"},
		{"8th bit kept on control", "#\xc1", "\x81"},
		{"8th bit delete", "#\xbf", "\xff"},
		{"8th bit prefix character", "#\xa3", "\xa3"},
		{"trailing prefix", "ab#", "ab#"},
	}
	for _, tc := range tests {
		got := kermit.Unquote([]byte(tc.in))
		if string(got) != tc.want {
			t.Errorf("%s: Unquote(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func sinit(maxl, timo, npad int, padc, eol, qctl byte) string {
	return string([]byte{
		byte(32 + maxl), byte(32 + timo), byte(32 + npad),
		padc ^ 64, byte(32 + int(eol)), qctl, 'N', '1', ' ',
	})
}

// idleDev reports no data once its input is used, like a polling UART.
type idleDev struct {
	in  bytes.Buffer
	out bytes.Buffer
}

func (d *idleDev) Read(p []byte) (int, error) {
	if d.in.Len() == 0 {
		time.Sleep(time.Millisecond)
		return 0, nil
	}
	return d.in.Read(p)
}
func (d *idleDev) Write(p []byte) (int, error) { return d.out.Write(p) }

func packets(b []byte) []*kermit.Packet {
	buf := bytes.NewBuffer(b)
	var ps []*kermit.Packet
	for {
		p, err := kermit.DecodePacket(&mockSerial{in: buf, out: &bytes.Buffer{}})
		if err != nil {
			return ps
		}
		ps = append(ps, p)
	}
}

func receive(t *testing.T, e *kermit.Engine, input ...[]byte) (out []byte, err error) {
	t.Helper()
	m := &mockSerial{in: &bytes.Buffer{}, out: &bytes.Buffer{}}
	for _, in := range input {
		m.in.Write(in)
	}
	err = e.ProcessStream(m)
	return m.out.Bytes(), err
}

func TestReceiverUsesSenderPrefix(t *testing.T) {
	// the sender announces '^' as its control prefix
	var name string
	var got []byte
	e := kermit.NewEngine(
		func(n string) error { name = n; return nil },
		func(p []byte) error { got = append(got, p...); return nil },
	)
	_, err := receive(t, e,
		frame(t, 0, 'S', sinit(94, 0, 0, 0, 13, '^')),
		frame(t, 1, 'F', "a^^b#c"),
		frame(t, 2, 'D', "x^Jy^^z#w^A"),
		frame(t, 3, 'Z', ""),
		frame(t, 4, 'B', ""),
	)
	if err != nil {
		t.Fatal(err)
	}
	if name != "a^b#c" {
		t.Errorf("name = %q", name)
	}
	if string(got) != "x\ny^z#w\x01" {
		t.Errorf("data = %q", got)
	}
}

func TestReceiverPadsAndTerminates(t *testing.T) {
	// the sender wants 2 NULs before and a linefeed after every packet it gets
	e := kermit.NewEngine(nil, nil)
	out, _ := receive(t, e, frame(t, 0, 'S', sinit(94, 0, 2, 0, 10, '#')))
	if len(out) < 17 || !bytes.Equal(out[:3], []byte{0, 0, kermit.Mark}) || out[16] != '\n' {
		t.Fatalf("ACK to Send-Init was %q", out)
	}
	if ps := packets(out); len(ps) == 0 || ps[0].Type != 'Y' {
		t.Fatalf("no ACK in %q", out)
	}
}

func TestDefaultTerminatorIsCR(t *testing.T) {
	// No parameters, so no padding and a carriage return.
	e := kermit.NewEngine(nil, nil)
	out, _ := receive(t, e, frame(t, 0, 'S', ""))
	if out[0] != kermit.Mark {
		t.Fatalf("padding sent: %q", out)
	}
	ack := rawFrame(0, 'Y', string(initData(t, out)))
	if !bytes.HasPrefix(out, ack) || out[len(ack)] != '\r' {
		t.Fatalf("got %q, want the ACK followed by CR", out)
	}
}

func initData(t *testing.T, out []byte) []byte {
	t.Helper()
	ps := packets(out)
	if len(ps) == 0 {
		t.Fatalf("no packet in %q", out)
	}
	return ps[0].Data
}

func TestSendInitAckCarriesParameters(t *testing.T) {
	e := kermit.NewEngine(nil, nil)
	out, _ := receive(t, e, frame(t, 0, 'S', ""), frame(t, 0, 'S', ""))
	ps := packets(out)
	if len(ps) < 2 {
		t.Fatalf("got %d packets", len(ps))
	}
	// a repeated Send-Init is answered with the parameters again
	for i := 0; i < 2; i++ {
		if ps[i].Type != 'Y' || len(ps[i].Data) != 9 {
			t.Errorf("ACK %d = %+v", i, ps[i])
		}
	}
}

func TestSenderHonorsPeerParameters(t *testing.T) {
	// the receiver wants 3 NULs before and a linefeed after each packet,
	// and packets no longer than 40
	dev := &ackDevice{params: sinit(40, 0, 3, 0, 10, '#')}
	content := bytes.Repeat([]byte("0123456789"), 30)
	if err := kermit.NewEngine(nil, nil).Send(dev, "f", content); err != nil {
		t.Fatal(err)
	}
	wrote := dev.wrote.Bytes()

	// The Send-Init has no padding and ends in CR.
	if wrote[0] != kermit.Mark {
		t.Errorf("Send-Init was padded: %q", wrote[:6])
	}
	ps := packets(wrote)
	if len(ps) < 6 || ps[0].Type != 'S' {
		t.Fatalf("packets: %d", len(ps))
	}
	for _, p := range ps[1:] {
		if p.Len > 40 {
			t.Errorf("%c packet LEN %d exceeds the peer's 40", p.Type, p.Len)
		}
	}
	// every later packet is NUL NUL NUL Mark ... LF
	later := wrote[bytes.IndexByte(wrote, '\r')+1:]
	n := bytes.Count(later, []byte{0, 0, 0, kermit.Mark})
	if n != len(ps)-1 {
		t.Errorf("%d padded packets, want %d", n, len(ps)-1)
	}
	if bytes.Count(later, []byte{'\n'}) < len(ps)-1 || bytes.Contains(later, []byte{'\r'}) {
		t.Errorf("terminators wrong in %q", later[:30])
	}
}

func TestErrorPacketIsNotEchoed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input [][]byte
		acks  int
	}{
		{"before the transfer", [][]byte{frame(t, 0, 'E', "no")}, 0},
		{"during the transfer", [][]byte{frame(t, 0, 'S', ""), frame(t, 1, 'E', "no")}, 1},
	} {
		e := kermit.NewEngine(nil, nil)
		out, err := receive(t, e, tc.input...)
		var re *kermit.RemoteError
		if !errors.As(err, &re) || string(re.Msg) != "no" {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		for _, p := range packets(out) {
			if p.Type == 'E' {
				t.Errorf("%s: answered an error packet with one", tc.name)
			}
		}
		if got := len(packets(out)); got != tc.acks {
			t.Errorf("%s: %d packets written, want %d", tc.name, got, tc.acks)
		}
	}
}

func TestSenderReportsFatalError(t *testing.T) {
	// the peer ACKs the Send-Init and then goes silent
	dev := &idleDev{}
	dev.in.Write(frame(t, 0, 'Y', ""))
	e := kermit.NewEngine(nil, nil)
	e.Timeout = 5 * time.Millisecond
	e.MaxRetries = 1
	err := e.Send(dev, "f", []byte("x"))
	if !errors.Is(err, kermit.ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	var last *kermit.Packet
	for _, p := range packets(dev.out.Bytes()) {
		last = p
	}
	if last == nil || last.Type != 'E' {
		t.Fatalf("the peer was not told: last packet %+v", last)
	}
}

func TestReceiverUsesPeerTimeout(t *testing.T) {
	// the sender asks for 1 second and then stops sending
	dev := &idleDev{}
	dev.in.Write(frame(t, 0, 'S', sinit(94, 1, 0, 0, 13, '#')))
	dev.in.Write(frame(t, 1, 'F', "f"))
	e := kermit.NewEngine(nil, nil)
	e.MaxRetries = 1

	start := time.Now()
	err := e.ProcessStream(dev)
	if !errors.Is(err, kermit.ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d < 1500*time.Millisecond || d > 4*time.Second {
		t.Errorf("gave up after %v, want about two timeouts of 1s", d)
	}

	// ACK for S, ACK for F, then the last ACK again, then an error packet
	var types []byte
	var seqs []uint8
	for _, p := range packets(dev.out.Bytes()) {
		types = append(types, p.Type)
		seqs = append(seqs, p.Seq)
	}
	if string(types) != "YYYE" || seqs[2] != 1 {
		t.Errorf("packets %q seqs %v, want YYYE with the repeat of seq 1", types, seqs)
	}
}

func TestCallerTimeoutBeatsPeerTimeout(t *testing.T) {
	dev := &idleDev{}
	dev.in.Write(frame(t, 0, 'S', sinit(94, 60, 0, 0, 13, '#')))
	e := kermit.NewEngine(nil, nil)
	e.Timeout = 20 * time.Millisecond
	e.MaxRetries = 1

	start := time.Now()
	err := e.ProcessStream(dev)
	if !errors.Is(err, kermit.ErrTimeout) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestInvalidPadAndTerminatorAreIgnored(t *testing.T) {
	tests := []struct {
		name string
		data string
		pad  int // pad characters still sent, as NUL
	}{
		{"terminator is the packet start", sinit(94, 0, 0, 0, kermit.Mark, '#'), 0},
		{"terminator is printable", sinit(94, 0, 0, 0, '!', '#'), 0},
		{"pad is the packet start", sinit(94, 0, 3, kermit.Mark, 13, '#'), 3},
		{"pad is printable", sinit(94, 0, 3, 'A', 13, '#'), 3},
	}
	for _, tc := range tests {
		out, _ := receive(t, kermit.NewEngine(nil, nil), frame(t, 0, 'S', tc.data))
		if len(out) <= tc.pad || bytes.Count(out, []byte{kermit.Mark}) != 1 {
			t.Errorf("%s: output %q has a stray packet start", tc.name, out)
			continue
		}
		// The defaults apply, NUL padding and a carriage return.
		if !bytes.Equal(out[:tc.pad], make([]byte, tc.pad)) || out[tc.pad] != kermit.Mark || out[len(out)-1] != '\r' {
			t.Errorf("%s: output %q, want %d NULs, the packet, then CR", tc.name, out, tc.pad)
		}
	}
}

const canceledText = "kermit: canceled"

func lastPacket(b []byte) *kermit.Packet {
	var last *kermit.Packet
	for _, p := range packets(b) {
		last = p
	}
	return last
}

func wantCancelPacket(t *testing.T, out []byte) {
	t.Helper()
	p := lastPacket(out)
	if p == nil || p.Type != 'E' || string(p.Data) != canceledText {
		t.Fatalf("last packet is %+v, want an error packet saying %q", p, canceledText)
	}
}

func flagAfter(d time.Duration) *atomic.Bool {
	var f atomic.Bool
	time.AfterFunc(d, func() { f.Store(true) })
	return &f
}

func TestCancelBeforeStart(t *testing.T) {
	always := func() bool { return true }

	t.Run("send", func(t *testing.T) {
		dev := &idleDev{}
		e := kermit.NewEngine(nil, nil)
		e.Cancel = always
		err := e.Send(dev, "f", []byte("x"))
		if !errors.Is(err, kermit.ErrCanceled) || dev.out.Len() != 0 {
			t.Fatalf("err = %v, wrote %q", err, dev.out.Bytes())
		}
	})
	t.Run("receive", func(t *testing.T) {
		dev := &idleDev{}
		e := kermit.NewEngine(nil, nil)
		e.Cancel = always
		err := e.ProcessStream(dev)
		if !errors.Is(err, kermit.ErrCanceled) || dev.out.Len() != 0 {
			t.Fatalf("err = %v, wrote %q", err, dev.out.Bytes())
		}
	})
}

func TestSendCancelMidTransfer(t *testing.T) {
	dev := &ackDevice{}
	e := kermit.NewEngine(nil, nil)
	e.Cancel = func() bool { return len(packets(dev.wrote.Bytes())) >= 5 }

	err := e.Send(dev, "f", bytes.Repeat([]byte("abcdefgh"), 200))
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	var types []byte
	for _, p := range packets(dev.wrote.Bytes()) {
		types = append(types, p.Type)
	}
	// The error replaces the next packet and has the next sequence number.
	if string(types) != "SFDDDE" {
		t.Fatalf("packets %q, want SFDDDE", types)
	}
	ps := packets(dev.wrote.Bytes())
	if ps[4].Seq != 4 || ps[5].Seq != 5 {
		t.Errorf("sequence numbers %d %d, want 4 then 5", ps[4].Seq, ps[5].Seq)
	}
	wantCancelPacket(t, dev.wrote.Bytes())
	if e.State() != kermit.StateError {
		t.Errorf("state = %v", e.State())
	}
}

func TestReceiveCancelMidTransfer(t *testing.T) {
	input := [][]byte{frame(t, 0, 'S', ""), frame(t, 1, 'F', "f")}
	for i := 0; i < 20; i++ {
		input = append(input, frame(t, uint8(2+i), 'D', "hello"))
	}
	calls := 0
	e := kermit.NewEngine(nil, func([]byte) error { calls++; return nil })
	e.Cancel = func() bool { return calls >= 3 }

	out, err := receive(t, e, input...)
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	if calls != 3 {
		t.Errorf("OnData called %d times, want 3", calls)
	}
	var types []byte
	for _, p := range packets(out) {
		types = append(types, p.Type)
	}
	if string(types) != "YYYYYE" {
		t.Errorf("packets %q, want five ACKs and an error", types)
	}
	wantCancelPacket(t, out)
}

func TestCancelWhileReceiverIsIdle(t *testing.T) {
	// no timeout is set, so only the cancel can end this
	dev := &idleDev{}
	dev.in.Write(frame(t, 0, 'S', ""))
	e := kermit.NewEngine(nil, nil)
	e.Cancel = flagAfter(50 * time.Millisecond).Load

	start := time.Now()
	err := e.ProcessStream(dev)
	if !errors.Is(err, kermit.ErrCanceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	wantCancelPacket(t, dev.out.Bytes())
}

func TestCancelWhileWaitingForSendInit(t *testing.T) {
	dev := &idleDev{}
	e := kermit.NewEngine(nil, nil)
	e.Cancel = flagAfter(50 * time.Millisecond).Load

	err := e.ProcessStream(dev)
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	// there is no transfer to cancel yet, so nothing is sent
	if dev.out.Len() != 0 {
		t.Errorf("wrote %q", dev.out.Bytes())
	}
}

func TestCancelWhileSenderWaitsForAck(t *testing.T) {
	dev := &idleDev{}
	dev.in.Write(frame(t, 0, 'Y', ""))
	e := kermit.NewEngine(nil, nil)
	e.Cancel = flagAfter(50 * time.Millisecond).Load

	start := time.Now()
	err := e.Send(dev, "f", []byte("x"))
	if !errors.Is(err, kermit.ErrCanceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
	wantCancelPacket(t, dev.out.Bytes())
}

type noiseDev struct{ idleDev }

func (d *noiseDev) Read(p []byte) (int, error) {
	p[0] = 'x'
	time.Sleep(50 * time.Microsecond)
	return 1, nil
}

func TestCancelThroughLineNoise(t *testing.T) {
	e := kermit.NewEngine(nil, nil)
	e.Cancel = flagAfter(50 * time.Millisecond).Load

	start := time.Now()
	err := e.ProcessStream(&noiseDev{})
	if !errors.Is(err, kermit.ErrCanceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestCallbackCanCancel(t *testing.T) {
	e := kermit.NewEngine(nil, func([]byte) error { return kermit.ErrCanceled })
	out, err := receive(t, e,
		frame(t, 0, 'S', ""), frame(t, 1, 'F', "f"), frame(t, 2, 'D', "x"))
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	wantCancelPacket(t, out)
}

func TestCancelledSenderDoesNotSendErrorForPeersError(t *testing.T) {
	// an error from the peer ends the transfer and is not answered
	dev := &idleDev{}
	dev.in.Write(frame(t, 0, 'Y', ""))
	dev.in.Write(frame(t, 1, 'E', "peer is full"))
	e := kermit.NewEngine(nil, nil)
	e.Cancel = func() bool { return false }
	err := e.Send(dev, "f", []byte("x"))
	var re *kermit.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v", err)
	}
	if p := lastPacket(dev.out.Bytes()); p == nil || p.Type == 'E' {
		t.Errorf("answered the peer's error with %+v", p)
	}
}

// A blocking Read cannot see Cancel, so the caller sets a read deadline.
func TestCancelWakesBlockedRead(t *testing.T) {
	a, b := testutil.MemPair(t)
	// the peer acknowledges the Send-Init and then says nothing
	peerSaw := make(chan []byte, 1)
	go func() {
		p, err := kermit.DecodePacket(b)
		if err != nil || p.Type != 'S' {
			peerSaw <- nil
			return
		}
		kermit.EncodePacket(b, &kermit.Packet{Seq: 0, Type: 'Y'})
		var all []byte
		for {
			q, err := kermit.DecodePacket(b)
			if err != nil {
				break
			}
			all = append(all, q.Type)
		}
		peerSaw <- all
	}()

	var canceled atomic.Bool
	time.AfterFunc(300*time.Millisecond, func() {
		canceled.Store(true)
		a.SetReadDeadline(time.Now())
	})
	e := kermit.NewEngine(nil, nil)
	e.Cancel = canceled.Load

	start := time.Now()
	err := e.Send(a, "f", []byte("x"))
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v, want ErrCanceled", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v to stop a blocked Read", d)
	}

	a.Close()
	select {
	case types := <-peerSaw:
		if len(types) == 0 || types[len(types)-1] != 'E' {
			t.Errorf("peer saw packet types %q, want the last to be an error", types)
		}
	case <-time.After(3 * time.Second):
		t.Error("peer never finished reading")
	}
}

func TestCancelWakesBlockedReceive(t *testing.T) {
	a, b := testutil.MemPair(t)
	var canceled atomic.Bool
	time.AfterFunc(100*time.Millisecond, func() {
		canceled.Store(true)
		a.SetReadDeadline(time.Now())
	})
	e := kermit.NewEngine(nil, nil)
	e.Cancel = canceled.Load

	if err := e.ProcessStream(a); !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	// nothing had started, so nothing is sent
	b.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, _ := b.Read(make([]byte, 8)); n != 0 {
		t.Errorf("%d bytes were sent", n)
	}
}

func TestNoErrorPacketAfterBreak(t *testing.T) {
	// the cancel is noticed while waiting for the ACK to the Break
	dev := &ackDevice{}
	e := kermit.NewEngine(nil, nil)
	e.Cancel = func() bool { return len(packets(dev.wrote.Bytes())) >= 5 }

	err := e.Send(dev, "f", []byte("x"))
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	if p := lastPacket(dev.wrote.Bytes()); p == nil || p.Type != 'B' {
		t.Fatalf("last packet %+v, want the Break with nothing after it", p)
	}
}

type lateAck struct {
	ackDevice
	delay time.Duration
	last  time.Time
}

func (d *lateAck) Write(p []byte) (int, error) {
	d.last = time.Now()
	return d.ackDevice.Write(p)
}

func (d *lateAck) Read(p []byte) (int, error) {
	if d.pending.Len() == 0 && time.Since(d.last) < d.delay {
		time.Sleep(time.Millisecond)
		return 0, nil
	}
	return d.ackDevice.Read(p)
}

func countType(b []byte, typ byte) (n int) {
	for _, p := range packets(b) {
		if p.Type == typ {
			n++
		}
	}
	return
}

func TestCancelWaitsForLateReply(t *testing.T) {
	// the reply comes within the grace period, so one error goes out, after it
	dev := &lateAck{delay: 80 * time.Millisecond}
	e := kermit.NewEngine(nil, nil)
	e.Cancel = func() bool { return len(packets(dev.wrote.Bytes())) >= 5 }

	err := e.Send(dev, "f", bytes.Repeat([]byte("abcdefgh"), 200))
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	wantCancelPacket(t, dev.wrote.Bytes())
	if n := countType(dev.wrote.Bytes(), 'E'); n != 1 {
		t.Errorf("%d error packets, want 1", n)
	}
}

func TestCancelWithoutReplySendsErrorTwice(t *testing.T) {
	// No reply in the grace period, so the error goes out twice.
	dev := &lateAck{delay: 2 * time.Second}
	e := kermit.NewEngine(nil, nil)
	e.Cancel = func() bool { return len(packets(dev.wrote.Bytes())) >= 1 }

	// the Send-Init is acknowledged late, and the cancel comes first
	err := e.Send(dev, "f", []byte("x"))
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	// nothing had started, so nothing is reported
	if n := countType(dev.wrote.Bytes(), 'E'); n != 0 {
		t.Errorf("%d error packets before the Send-Init was acknowledged", n)
	}

	// after the Send-Init is acknowledged the same device stays silent
	dev2 := &idleDev{}
	dev2.in.Write(frame(t, 0, 'Y', ""))
	e2 := kermit.NewEngine(nil, nil)
	e2.Cancel = func() bool { return len(packets(dev2.out.Bytes())) >= 2 }
	if err := e2.Send(dev2, "f", []byte("x")); !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	ps := packets(dev2.out.Bytes())
	if len(ps) != 4 || ps[2].Type != 'E' || ps[3].Type != 'E' || ps[3].Seq != ps[2].Seq+1 {
		var got []string
		for _, p := range ps {
			got = append(got, string(p.Type)+string(rune('0'+p.Seq)))
		}
		t.Fatalf("packets %v, want S F E E with consecutive error numbers", got)
	}
}
