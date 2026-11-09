package kermit

import "io"

// Internals for tests, under names outside the package API.

const (
	Mark          = mark
	MaxData       = maxDataLen
	TypeACK       = typeACK
	TypeNAK       = typeNAK
	TypeError     = typeError
	StateError    = stateError
	StateComplete = stateComplete
	ErrChecksum   = errChecksum
	ErrMalformed  = errMalformed
)

type (
	Packet       = packet
	SerialDevice = io.ReadWriter
)

func (e *Engine) State() stateID { return e.state }

// EncodePacket writes one packet to rw with no padding and no terminator.
func EncodePacket(rw io.ReadWriter, p *Packet) error {
	var buf [txSize]byte
	return encodePacket(rw, buf[:], p.Seq, p.Type, p.Data, 0, 0, 0)
}

// DecodePacket reads one packet from rw. It allocates.
func DecodePacket(rw io.ReadWriter) (*Packet, error) {
	var rx rxBuf
	p, err := decodePacket(rw, 0, &rx, nil)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func Quote(dst, src []byte) []byte {
	for _, c := range src {
		dst = appendQuoted(dst, c)
	}
	return dst
}

func Unquote(b []byte) []byte { return unquoteWith(b, quoteChar) }
