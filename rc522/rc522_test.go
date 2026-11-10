//go:build !tinygo

package rc522

import (
	"bytes"
	"errors"
	"testing"
)

// fakeCard simulates a MIFARE Classic card with a 4, 7 or 10 byte UID.
type fakeCard struct {
	uid   []byte
	level int // cascade levels already selected
}

// frame returns the 4 UID bytes of cascade level l and its SAK.
func (c *fakeCard) frame(l int) ([]byte, byte) {
	switch len(c.uid) {
	case 4:
		return c.uid, 0x08
	case 7:
		if l == 0 {
			return append([]byte{piccCT}, c.uid[:3]...), 0x04
		}
		return c.uid[3:], 0x08
	default:
		switch l {
		case 0:
			return append([]byte{piccCT}, c.uid[:3]...), 0x04
		case 1:
			return append([]byte{piccCT}, c.uid[3:6]...), 0x04
		}
		return c.uid[6:], 0x08
	}
}

func bit(b []byte, i int) byte { return b[i/8] >> (i % 8) & 1 }

func crcA(data []byte) (byte, byte) {
	crc := uint16(0x6363)
	for _, b := range data {
		b ^= byte(crc)
		b ^= b << 4
		crc = crc>>8 ^ uint16(b)<<8 ^ uint16(b)<<3 ^ uint16(b)>>4
	}
	return byte(crc), byte(crc >> 8)
}

// fakeChip simulates the MFRC522 registers and the cards in range.
type fakeChip struct {
	regs  [64]byte
	fifo  []byte
	cards []*fakeCard

	selected bool
	started  bool
	reg      byte
	read     bool
}

func newTestDevice(cards ...*fakeCard) *Device {
	c := &fakeChip{cards: cards}
	cs := machinePin{onChange: func(high bool) { c.selected = !high; c.started = false }}
	return New(c, cs, machinePin{})
}

func (c *fakeChip) Transfer(b byte) (byte, error) {
	if !c.started {
		c.started = true
		c.read = b&0x80 != 0
		c.reg = b >> 1 & 0x3F
		return 0, nil
	}
	if c.read {
		return c.readReg(c.reg), nil
	}
	c.writeReg(c.reg, b)
	return 0, nil
}

func (c *fakeChip) readReg(reg byte) byte {
	switch reg {
	case regFIFOData:
		if len(c.fifo) == 0 {
			return 0
		}
		v := c.fifo[0]
		c.fifo = c.fifo[1:]
		return v
	case regFIFOLevel:
		return byte(len(c.fifo))
	case regVersion:
		return 0x92
	}
	return c.regs[reg]
}

func (c *fakeChip) writeReg(reg, v byte) {
	switch reg {
	case regFIFOData:
		c.fifo = append(c.fifo, v)
	case regFIFOLevel:
		if v&0x80 != 0 {
			c.fifo = nil
		}
	case regComIrq, regDivIrq:
		c.regs[reg] &^= v
	case regCommand:
		c.regs[reg] = v
		if v == pcdCalcCRC {
			lsb, msb := crcA(c.fifo)
			c.regs[regCRCResultL], c.regs[regCRCResultH] = lsb, msb
			c.regs[regDivIrq] |= 0x04
			c.fifo = nil
		}
	case regBitFraming:
		c.regs[reg] = v
		if v&0x80 != 0 && c.regs[regCommand] == pcdTransceive {
			c.transceive()
		}
	default:
		c.regs[reg] = v
	}
}

// transceive sends the FIFO to the cards and fills the FIFO with the answer.
func (c *fakeChip) transceive() {
	tx := c.fifo
	c.fifo = nil
	c.regs[regError] = 0
	c.regs[regControl] &^= 0x07

	var resp []byte
	switch {
	case len(tx) == 1 && (tx[0] == piccREQA || tx[0] == piccWUPA):
		for _, card := range c.cards {
			card.level = 0
		}
		if len(c.cards) > 0 {
			resp = []byte{0x04, 0x00}
		}
		if len(c.cards) > 1 {
			c.regs[regError] |= 0x08
		}
	case len(tx) >= 2 && (tx[0] == piccSEL_CL1 || tx[0] == piccSEL_CL2 || tx[0] == piccSEL_CL3):
		resp = c.selectOrAnticollision(tx)
	}

	if resp == nil {
		c.regs[regComIrq] |= 0x01 // timer
		return
	}
	c.fifo = resp
	c.regs[regComIrq] |= 0x30
}

func (c *fakeChip) selectOrAnticollision(tx []byte) []byte {
	l := int(tx[0]-piccSEL_CL1) / 2
	var cands []*fakeCard
	for _, card := range c.cards {
		if card.level == l {
			cands = append(cands, card)
		}
	}

	if tx[1] == 0x70 {
		var resp []byte
		for _, card := range cands {
			f, sak := card.frame(l)
			if bytes.Equal(tx[2:6], f) {
				card.level++
				lsb, msb := crcA([]byte{sak})
				resp = []byte{sak, lsb, msb}
			}
		}
		return resp
	}

	whole := int(tx[1]>>4) - 2
	txLastBits := int(tx[1] & 0x0F)
	known := whole*8 + txLastBits

	var frames [][]byte
	for _, card := range cands {
		f, _ := card.frame(l)
		f = append(append([]byte{}, f...), f[0]^f[1]^f[2]^f[3])
		match := true
		for i := 0; i < known; i++ {
			if bit(f, i) != bit(tx[2:], i) {
				match = false
			}
		}
		if match {
			frames = append(frames, f)
		}
	}
	if len(frames) == 0 {
		return nil
	}

	// Cards answer at the same time so the bits are ORed together.
	out := make([]byte, 5)
	collPos := 0
	for i := known; i < 40; i++ {
		var v byte
		differ := false
		for _, f := range frames {
			v |= bit(f, i)
			differ = differ || bit(f, i) != bit(frames[0], i)
		}
		if collPos == 0 && differ {
			collPos = i + 1
		}
		if collPos == 0 || i < collPos {
			out[i/8] |= v << (i % 8)
		}
	}
	// Bits before known are not received. The first byte is aligned to them.
	out = out[whole:]
	out[0] &= 0xFF << txLastBits

	if collPos != 0 {
		c.regs[regError] |= 0x08
		c.regs[regColl] = byte(collPos) & 0x1F
	}
	return out
}

func TestReadUID(t *testing.T) {
	uid4a := []byte{0x12, 0x34, 0x56, 0x78}
	uid4b := []byte{0x12, 0x34, 0x57, 0x01}
	uid4c := []byte{0x92, 0x00, 0x00, 0x00}
	uid7 := []byte{0x04, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	uid7b := []byte{0x04, 0xAA, 0xBB, 0xCC, 0xDD, 0xEF, 0x00}
	uid10 := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xAA}

	tests := []struct {
		name  string
		cards [][]byte
		want  []byte
	}{
		{"4 byte", [][]byte{uid4a}, uid4a},
		{"7 byte", [][]byte{uid7}, uid7},
		{"10 byte", [][]byte{uid10}, uid10},
		// The card with a 1 at the first differing bit wins.
		{"two 4 byte", [][]byte{uid4a, uid4b}, uid4b},
		{"two 4 byte, first bit", [][]byte{uid4a, uid4c}, uid4c},
		{"three 4 byte", [][]byte{uid4a, uid4b, uid4c}, uid4c},
		{"two 7 byte", [][]byte{uid7, uid7b}, uid7b},
		// Bits are sent LSB first so 0x12 beats 0x88 at bit 1.
		{"4 and 7 byte", [][]byte{uid4a, uid7}, uid4a},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cards []*fakeCard
			for _, u := range tt.cards {
				cards = append(cards, &fakeCard{uid: u})
			}
			d := newTestDevice(cards...)
			if ok, err := d.Request(); !ok || err != nil {
				t.Fatalf("Request() = %v, %v", ok, err)
			}
			got, err := d.ReadUID()
			if err != nil {
				t.Fatalf("ReadUID() error: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("ReadUID() = %x, want %x", got, tt.want)
			}
		})
	}
}

func TestNoCard(t *testing.T) {
	d := newTestDevice()
	if ok, err := d.Request(); ok || err != nil {
		t.Errorf("Request() = %v, %v, want false, nil", ok, err)
	}
	if _, err := d.ReadUID(); !errors.Is(err, ErrReadTimeout) {
		t.Errorf("ReadUID() error = %v, want %v", err, ErrReadTimeout)
	}
}
