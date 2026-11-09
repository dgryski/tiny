// Package kermit sends and receives files with the Kermit protocol over a
// serial line or any other byte stream, using only the basic features.
//
// Read must return (0, nil) when no byte is waiting, so the engine can see a
// timeout or a cancel. Transfers only allocate for received file names.
package kermit

import (
	"io"
	"time"
)

const (
	mark       = 0x01 // Ctrl-A, starts a packet
	charOffset = 32   // tochar() offset
)

const (
	typeSendInit   = 'S'
	typeFileHeader = 'F'
	typeData       = 'D'
	typeEOF        = 'Z'
	typeBreak      = 'B'
	typeACK        = 'Y'
	typeNAK        = 'N'
	typeError      = 'E'
)

type stateID int

const (
	stateIdle stateID = iota
	stateReceiveInit
	stateReceiveFile
	stateFileDone // after a file, waiting for the next file or Break
	stateComplete
	stateError
)

type packet struct {
	Len   uint8  // length of the fields after LEN, up to and including CHECK
	Seq   uint8  // sequence number, 0 to 63
	Type  byte   // packet type character
	Data  []byte // payload
	Check uint8  // block check character
}

// Engine sends and receives files, one transfer at a time. Only Cancel may
// be set from another goroutine.
type Engine struct {
	// Timeout bounds how long one packet read may take. Zero uses
	// the peer's request, or waits forever if it makes none.
	Timeout time.Duration

	// MaxRetries is the number of consecutive failures tolerated. Zero means 5.
	MaxRetries int

	// Cancel is polled during a transfer. When true, the transfer ends with
	// an error packet and ErrCanceled. A blocking Read needs a read deadline.
	Cancel func() bool

	// OnDiscard is called when the sender abandons the file being received,
	// so the caller can drop what it has stored. It may be nil. An error
	// stops the transfer.
	OnDiscard func() error

	state    stateID
	seq      uint8
	onData   func(payload []byte) error
	onHeader func(filename string) error

	peer    peerParams
	pending bool // a packet is sent and not yet acknowledged
	started bool // the receiver acknowledged the Send-Init
	sinit   [9]byte

	remote RemoteError

	rx   rxBuf
	tx   [txSize]byte
	data [maxDataLen]byte
}

// rxBuf holds one received packet. packet.Data points into it until the next read.
type rxBuf struct {
	one  [1]byte
	hdr  [3]byte
	data [maxDataLen]byte
}

const maxPad = 16

const txSize = maxPad + maxDataLen + 5 + 1

// peerParams are the Send-Init fields that govern what we send.
type peerParams struct {
	maxl    int           // largest LEN the peer will accept
	timeout time.Duration // how long to wait for the peer's packets, 0 if unset
	npad    int           // padding characters before each packet
	padc    byte          // padding character
	eol     byte          // packet terminator
	qctl    byte          // prefix the peer uses for control characters
}

func defaultPeer() peerParams {
	return peerParams{maxl: defaultMaxL, eol: '\r', qctl: quoteChar}
}

// parse reads the first six Send-Init fields, MAXL to QCTL. Missing or blank
// fields keep their defaults. See the Kermit Protocol Manual, 6th ed.
func (pp *peerParams) parse(d []byte) {
	field := func(i int) (byte, bool) {
		if i < len(d) && printable(d[i]) {
			return d[i], true
		}
		return 0, false
	}
	if c, ok := field(0); ok {
		if m := int(c - charOffset); m >= 10 && m <= maxDataLen+3 {
			pp.maxl = m
		}
	}
	if c, ok := field(1); ok {
		pp.timeout = time.Duration(c-charOffset) * time.Second
	}
	if c, ok := field(2); ok {
		pp.npad = min(int(c-charOffset), maxPad)
	}
	// PADC and EOL must be control characters other than the packet start.
	if c, ok := field(3); ok && c != ' ' {
		if p := c ^ 64; (p < 32 || p == 127) && p != mark {
			pp.padc = p
		}
	}
	if c, ok := field(4); ok && c != ' ' {
		if v := c - charOffset; v > 0 && v < 32 && v != mark {
			pp.eol = v
		}
	}
	if c, ok := field(5); ok && c != ' ' {
		pp.qctl = c
	}
}

func (e *Engine) timeoutFor() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return e.peer.timeout
}

func (e *Engine) canceled() bool {
	return e.Cancel != nil && e.Cancel()
}

func isCanceled(err error) bool {
	e, ok := err.(errCode)
	return ok && e == ErrCanceled
}

func callbackText(err error, def string) string {
	if isCanceled(err) {
		return ErrCanceled.Error()
	}
	return def
}

func isTimeout(err error) bool {
	e, ok := err.(errCode)
	return ok && e == ErrTimeout
}

func (e *Engine) retries() int {
	if e.MaxRetries > 0 {
		return e.MaxRetries
	}
	return defaultRetries
}

func recoverable(err error) bool {
	e, ok := err.(errCode)
	return ok && (e == errChecksum || e == errMalformed || e == ErrTimeout)
}

// NewEngine returns an Engine. A received file calls onHeader with its name,
// then onData with each piece. Either may be nil. An error stops the transfer.
func NewEngine(onHeader func(string) error, onData func([]byte) error) *Engine {
	return &Engine{
		state:    stateIdle,
		seq:      0,
		onHeader: onHeader,
		onData:   onData,
	}
}

type errCode int

const (
	errChecksum errCode = iota + 1
	errMalformed
	errTooLong
	errProtocol
	errState
	errTimeout
	errCanceled
)

const (
	// ErrTimeout is returned when the retries ran out.
	ErrTimeout = errTimeout

	// ErrCanceled is returned when a transfer was canceled.
	ErrCanceled = errCanceled

	// ErrTooLong is returned when a file name does not fit in one packet.
	ErrTooLong = errTooLong
)

func (e errCode) Error() string {
	switch e {
	case errChecksum:
		return "kermit: checksum mismatch"
	case errMalformed:
		return "kermit: malformed packet"
	case errTooLong:
		return "kermit: packet data too long"
	case errTimeout:
		return "kermit: timeout"
	case errProtocol:
		return "kermit: protocol error"
	case errState:
		return "kermit: protocol state mismatch"
	case errCanceled:
		return "kermit: canceled"
	}
	return "kermit: unknown error"
}

// RemoteError is the message from an error packet sent by the peer. The
// Engine owns it, so Msg is only valid until the next call on the Engine.
type RemoteError struct{ Msg []byte }

func (e *RemoteError) Error() string { return "kermit: remote error: " + string(e.Msg) }

// remoteError keeps the peer's message in the Engine, which avoids allocating.
func (e *Engine) remoteError(data []byte) *RemoteError {
	n := copy(e.data[:], unquoteWith(data, e.peer.qctl))
	e.remote.Msg = e.data[:n]
	return &e.remote
}

// The Kermit Protocol Manual, 6th ed., suggests about 5 retries.
const defaultRetries = 5

const defaultMaxL = 80

// maxDataLen is the largest data field in a standard 94 byte packet.
const maxDataLen = 91

// checksum is the Kermit type 1 block check.
func checksum(length, seq, typ byte, data []byte) uint8 {
	sum := uint32(length) + uint32(seq) + uint32(typ)
	for _, b := range data {
		sum += uint32(b)
	}
	return uint8(((sum + ((sum & 0xC0) >> 6)) & 0x3F) + charOffset)
}

const quoteChar = '#'

func unquoteWith(b []byte, q byte) []byte {
	n := 0
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == q && i+1 < len(b) {
			i++
			c = b[i]
			// Only control codes are toggled. Kermit Protocol Manual, 6th ed., section 4.4.
			if a := c & 0x7F; a != q && ((a >= '@' && a <= '_') || a == '?') {
				c ^= 64
			}
		}
		b[n] = c
		n++
	}
	return b[:n]
}

func printable(b byte) bool { return b >= charOffset && b < 127 }

func (e *Engine) encode(sd io.ReadWriter, seq uint8, typ byte, data []byte) error {
	return encodePacket(sd, e.tx[:], seq, typ, data, e.peer.npad, e.peer.padc, e.peer.eol)
}

// encodePacket builds the frame in buf, which holds txSize bytes.
func encodePacket(sd io.ReadWriter, buf []byte, seq uint8, typ byte, data []byte, npad int, padc, eol byte) error {
	if len(data) > maxDataLen {
		return errTooLong
	}

	i := 0
	for ; i < npad; i++ {
		buf[i] = padc
	}

	buf[i] = mark
	buf[i+1] = uint8(3+len(data)) + charOffset
	buf[i+2] = (seq & 63) + charOffset
	buf[i+3] = typ
	n := i + 4 + copy(buf[i+4:], data)
	buf[n] = checksum(buf[i+1], buf[i+2], buf[i+3], data)
	n++
	if eol != 0 {
		buf[n] = eol
		n++
	}

	_, err := sd.Write(buf[:n])
	return err
}

func (e *Engine) decode(sd io.ReadWriter) (packet, error) {
	return decodePacket(sd, e.timeoutFor(), &e.rx, e.Cancel)
}

// readFull fills buf. A zero deadline never expires.
func readFull(sd io.ReadWriter, buf []byte, deadline time.Time, cancel func() bool) error {
	got := 0
	for got < len(buf) {
		n, err := sd.Read(buf[got:])
		got += n
		if got == len(buf) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 && cancel != nil && cancel() {
			return ErrCanceled
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return ErrTimeout
		}
	}
	return nil
}

func decodePacket(sd io.ReadWriter, timeout time.Duration, rx *rxBuf, cancel func() bool) (packet, error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	return decodeUntil(sd, deadline, rx, cancel)
}

// decodeUntil reads one packet. A zero deadline never expires.
func decodeUntil(sd io.ReadWriter, deadline time.Time, rx *rxBuf, cancel func() bool) (packet, error) {
	for {
		n, err := sd.Read(rx.one[:])
		if n == 1 && rx.one[0] == mark {
			break
		}
		if err != nil {
			return packet{}, err
		}
		// Noise on the line must not hide a cancel or a timeout.
		if cancel != nil && cancel() {
			return packet{}, ErrCanceled
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return packet{}, ErrTimeout
		}
	}

	if err := readFull(sd, rx.hdr[:], deadline, cancel); err != nil {
		return packet{}, err
	}
	h := rx.hdr

	if !printable(h[0]) || !printable(h[1]) {
		return packet{}, errMalformed
	}
	rawLen := h[0] - charOffset
	if h[1]-charOffset > 63 || rawLen < 3 || rawLen > maxDataLen+3 {
		return packet{}, errMalformed
	}
	dataLen := rawLen - 3

	p := packet{
		Len:  rawLen,
		Seq:  h[1] - charOffset,
		Type: h[2],
		Data: rx.data[:dataLen],
	}

	if dataLen > 0 {
		if err := readFull(sd, p.Data, deadline, cancel); err != nil {
			return packet{}, err
		}
	}

	if err := readFull(sd, rx.one[:], deadline, cancel); err != nil {
		return packet{}, err
	}
	p.Check = rx.one[0]

	if p.Check != checksum(h[0], h[1], h[2], p.Data) {
		return packet{}, errChecksum
	}

	return p, nil
}

// ProcessStream receives one or more files from a sender on rw. It returns
// nil when the sender ends the transfer.
func (e *Engine) ProcessStream(rw io.ReadWriter) error {
	e.state = stateIdle
	e.seq = 0
	e.peer = defaultPeer()

	fails := 0
	for {
		if e.canceled() {
			return e.cancelReceive(rw)
		}
		p, err := e.decode(rw)
		if err != nil {
			// A read deadline set by the caller to wake Read counts as a cancel.
			if isCanceled(err) || e.canceled() {
				return e.cancelReceive(rw)
			}
			if recoverable(err) {
				fails++
				if fails <= e.retries() {
					if isTimeout(err) && e.state != stateIdle {
						e.sendACKOrNAK(rw, typeACK, (e.seq-1)&63, nil)
					} else {
						e.sendACKOrNAK(rw, typeNAK, e.seq, nil)
					}
					continue
				}
				e.sendError(rw, "too many retries")
			}
			e.state = stateError
			return err
		}
		// Never answer an error packet with another one.
		if p.Type == typeError {
			e.state = stateError
			return e.remoteError(p.Data)
		}

		if p.Seq != e.seq {
			// A peer that keeps repeating a packet counts as a failure.
			fails++
			if fails > e.retries() {
				e.sendError(rw, "too many retries")
				return ErrTimeout
			}
			if p.Seq == ((e.seq - 1) & 63) {
				if p.Type == typeSendInit {
					e.sendACKOrNAK(rw, typeACK, p.Seq, e.initParams())
				} else {
					e.sendACKOrNAK(rw, typeACK, p.Seq, nil)
				}
				continue
			}
			e.sendACKOrNAK(rw, typeNAK, e.seq, nil)
			continue
		}
		fails = 0

		switch e.state {
		case stateIdle:
			if p.Type == typeSendInit {
				e.state = stateReceiveInit
				e.peer.parse(p.Data)
				e.sendACKOrNAK(rw, typeACK, e.seq, e.initParams())
				e.seq = (e.seq + 1) & 63
			} else {
				e.sendError(rw, "unexpected initial packet")
				return errProtocol
			}

		case stateReceiveInit, stateFileDone:
			switch p.Type {
			case typeFileHeader:
				filename := string(unquoteWith(p.Data, e.peer.qctl))
				if e.onHeader != nil {
					if err := e.onHeader(filename); err != nil {
						e.sendError(rw, callbackText(err, "failed to open file"))
						return err
					}
				}
				e.state = stateReceiveFile
				e.sendACKOrNAK(rw, typeACK, e.seq, nil)
				e.seq = (e.seq + 1) & 63

			case typeBreak:
				e.state = stateComplete
				e.sendACKOrNAK(rw, typeACK, e.seq, nil)
				return nil

			default:
				e.sendError(rw, "expected file header or break")
				return errState
			}

		case stateReceiveFile:
			switch p.Type {
			case typeData:
				if e.onData != nil {
					if err := e.onData(unquoteWith(p.Data, e.peer.qctl)); err != nil {
						e.sendError(rw, callbackText(err, "write failure"))
						return err
					}
				}
				e.sendACKOrNAK(rw, typeACK, e.seq, nil)
				e.seq = (e.seq + 1) & 63

			case typeEOF:
				// A Z packet with the data D discards the file.
				if len(p.Data) == 1 && p.Data[0] == 'D' && e.OnDiscard != nil {
					if err := e.OnDiscard(); err != nil {
						e.sendError(rw, callbackText(err, "discard failure"))
						return err
					}
				}
				e.state = stateFileDone
				e.sendACKOrNAK(rw, typeACK, e.seq, nil)
				e.seq = (e.seq + 1) & 63

			default:
				e.sendError(rw, "expected data or eof")
				return errState
			}
		}
	}
}

func (e *Engine) sendACKOrNAK(sd io.ReadWriter, pType byte, seq uint8, data []byte) {
	_ = e.encode(sd, seq, pType, data)
}

func (e *Engine) sendError(sd io.ReadWriter, msg string) {
	e.state = stateError
	n := copy(e.data[:], msg)
	_ = e.encode(sd, e.seq, typeError, e.data[:n])
}

// abort sends the error with the current and the next sequence number when a
// packet is unacknowledged, because a peer ignores an unexpected sequence.
func (e *Engine) abort(sd io.ReadWriter, msg string) {
	if e.pending {
		e.sendError(sd, msg)
		e.seq = (e.seq + 1) & 63
	}
	e.sendError(sd, msg)
}

func errText(err error) string {
	if e, ok := err.(errCode); ok {
		return e.Error()
	}
	return "transfer failed"
}

const cancelGrace = 250 * time.Millisecond

// awaitReply waits for the reply to packet seq. C-Kermit discards input that
// arrives before its reply, so the error packet waits for it.
func (e *Engine) awaitReply(sd io.ReadWriter, seq uint8) {
	p, err := decodePacket(sd, cancelGrace, &e.rx, nil)
	if err != nil {
		return
	}
	if (p.Type == typeACK && p.Seq == seq) || (p.Type == typeNAK && p.Seq == (seq+1)&63) {
		e.seq = (seq + 1) & 63
		e.pending = false
	}
}

// cancelReceive ends a canceled receive. The error packet replaces the reply
// to the sender's next packet.
func (e *Engine) cancelReceive(sd io.ReadWriter) error {
	if e.state != stateIdle {
		decodePacket(sd, cancelGrace, &e.rx, nil)
		e.sendError(sd, ErrCanceled.Error())
	}
	e.state = stateError
	return ErrCanceled
}

// initParams builds the Send-Init data field. It uses no 8-bit prefixing or repeat counts.
func (e *Engine) initParams() []byte {
	secs := byte(e.Timeout / time.Second)
	if secs > 90 {
		secs = 90
	}
	e.sinit = [9]byte{
		charOffset + maxDataLen + 3, charOffset + secs, charOffset, '@',
		charOffset + '\r', quoteChar, 'N', '1', ' ',
	}
	return e.sinit[:]
}

func quotedLen(c byte) int {
	if a := c & 0x7F; a < 32 || a == 127 || a == quoteChar {
		return 2
	}
	return 1
}

func appendQuoted(dst []byte, c byte) []byte {
	switch a := c & 0x7F; {
	case a < 32 || a == 127:
		return append(dst, quoteChar, c^64)
	case a == quoteChar:
		return append(dst, quoteChar, c)
	}
	return append(dst, c)
}

// exchange sends a packet and waits for its ACK. A NAK for the next sequence counts as an ACK.
func (e *Engine) exchange(sd io.ReadWriter, typ byte, data []byte) (packet, error) {
	seq := e.seq
	for try := 0; try <= e.retries(); try++ {
		if e.canceled() {
			if e.pending {
				e.awaitReply(sd, seq)
			}
			return packet{}, ErrCanceled
		}
		if err := e.encode(sd, seq, typ, data); err != nil {
			return packet{}, err
		}
		e.pending = true
		// Ignored packets must not extend the wait for the reply.
		var deadline time.Time
		if t := e.timeoutFor(); t > 0 {
			deadline = time.Now().Add(t)
		}
	wait:
		for {
			if e.canceled() {
				e.awaitReply(sd, seq)
				return packet{}, ErrCanceled
			}
			p, err := decodeUntil(sd, deadline, &e.rx, e.Cancel)
			if err != nil {
				if isCanceled(err) || e.canceled() {
					e.awaitReply(sd, seq)
					return packet{}, ErrCanceled
				}
				if recoverable(err) {
					break wait
				}
				return packet{}, err
			}
			switch {
			case p.Type == typeError:
				return packet{}, e.remoteError(p.Data)
			case p.Type == typeACK && p.Seq == seq:
				e.seq = (seq + 1) & 63
				e.pending = false
				return p, nil
			case p.Type == typeNAK && p.Seq == (seq+1)&63:
				e.seq = (seq + 1) & 63
				e.pending = false
				return p, nil
			case p.Type == typeNAK && p.Seq == seq:
				break wait
			}
		}
	}
	return packet{}, ErrTimeout
}

// Send transmits one file named name with the contents data to a receiver on
// rw. It returns nil when the whole file is acknowledged.
func (e *Engine) Send(rw io.ReadWriter, name string, data []byte) error {
	e.started = false
	err := e.send(rw, name, data)
	if err != nil {
		// Tell the peer, unless it sent the error.
		if _, remote := err.(*RemoteError); e.started && !remote {
			e.abort(rw, errText(err))
		}
		e.state = stateError
	} else {
		e.state = stateComplete
	}
	return err
}

func (e *Engine) send(rw io.ReadWriter, name string, data []byte) error {
	if len(name) > maxDataLen {
		return errTooLong
	}
	e.state = stateIdle
	e.seq = 0
	e.peer = defaultPeer()
	e.pending = false
	if e.canceled() {
		return ErrCanceled
	}

	ack, err := e.exchange(rw, typeSendInit, e.initParams())
	if err != nil {
		return err
	}
	e.started = true
	e.peer.parse(ack.Data)
	maxData := e.peer.maxl - 3

	fname := e.data[:0]
	for i := 0; i < len(name); i++ {
		if len(fname)+quotedLen(name[i]) > maxData {
			return errTooLong
		}
		fname = appendQuoted(fname, name[i])
	}
	if _, err := e.exchange(rw, typeFileHeader, fname); err != nil {
		return err
	}

	for len(data) > 0 {
		if e.canceled() {
			return ErrCanceled
		}
		out := e.data[:0]
		for len(data) > 0 && len(out)+quotedLen(data[0]) <= maxData {
			out = appendQuoted(out, data[0])
			data = data[1:]
		}
		if _, err := e.exchange(rw, typeData, out); err != nil {
			return err
		}
	}

	if _, err := e.exchange(rw, typeEOF, nil); err != nil {
		return err
	}
	// After the Break there is nothing left to cancel.
	e.started = false
	_, err = e.exchange(rw, typeBreak, nil)
	return err
}
