package hd

import (
	"unsafe"
)

func Dump(p []byte) {
	var hd Dumper
	hd.Dump(p)
}

var hex = []byte("0123456789abcdef")

type Dumper struct {
	offset uint
	line   [83]byte
}

func (hx *Dumper) Dump(buf []byte) {
	for i := 0; i < len(buf); i += 16 {
		m := min(len(buf), i+16)
		hx.format(buf[i:m])
		hx.offset += uint(m - i)
	}
}

func (hx *Dumper) SetOffset(offs uint) {
	hx.offset = offs
}

func (hx *Dumper) format(buf []byte) {
	// addr:_(hex dump)+spaces+space+bar+chars+bar+newline
	line := hx.line[:]
	ptr := 0

	offs := hx.offset
	ptr += 8

	line[ptr] = ':'
	ptr--

	for offs > 0 {
		line[ptr] = hex[offs&0x0f]
		ptr--
		offs >>= 4
	}

	for ptr >= 0 {
		line[ptr] = '0'
		ptr--
	}

	ptr = 9
	line[ptr] = ' '
	ptr++

	for i, b := range buf {
		if i%4 == 0 {
			line[ptr] = ' '
			ptr++
		}

		line[ptr] = hex[b>>4]
		ptr++
		line[ptr] = hex[b&0x0f]
		ptr++
		line[ptr] = ' '
		ptr++

	}

	// fill in rest of line
	for i := len(buf); i < 16; i++ {
		if i%4 == 0 {
			line[ptr] = ' '
			ptr++
		}

		line[ptr] = ' '
		ptr++
		line[ptr] = ' '
		ptr++
		line[ptr] = ' '
		ptr++
	}

	line[ptr] = ' '
	ptr++
	line[ptr] = ' '
	ptr++
	line[ptr] = '|'
	ptr++

	for _, v := range buf {
		if v > 32 && v < 127 {
			line[ptr] = v
		} else {
			line[ptr] = '.'
		}
		ptr++
	}

	line[ptr] = '|'
	ptr++

	line[ptr] = '\n'
	ptr++

	print(unsafe.String(&line[0], ptr))
}
