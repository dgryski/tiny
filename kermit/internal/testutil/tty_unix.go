//go:build !tinygo && (darwin || linux)

package testutil

import (
	"io"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// TTY is a raw serial port. Read returns (0, nil) when idle.
type TTY struct {
	f      *os.File
	ch     chan []byte
	rest   []byte
	closed chan struct{}
}

func OpenTTY(t *testing.T, path string) *TTY {
	t.Helper()
	// O_NONBLOCK and a non-draining ioctl keep a stalled board from hanging the open.
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	fd := f.Fd() // blocking mode again
	var tio syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&tio))); e != 0 {
		t.Fatalf("get termios %s: %v", path, e)
	}
	// Raw mode with 8 bits and no echo, translation or flow control.
	tio.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON | syscall.IXOFF
	tio.Oflag &^= syscall.OPOST
	tio.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	tio.Cflag &^= syscall.CSIZE | syscall.PARENB
	tio.Cflag |= syscall.CS8 | syscall.CREAD | syscall.CLOCAL
	// Reads return after 100ms so the reader goroutine can exit.
	tio.Cc[syscall.VMIN], tio.Cc[syscall.VTIME] = 0, 1
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlSetTermios, uintptr(unsafe.Pointer(&tio))); e != 0 {
		t.Fatalf("set termios %s: %v", path, e)
	}
	p := &TTY{f: f, ch: make(chan []byte, 64), closed: make(chan struct{})}
	go func() {
		defer close(p.ch)
		buf := make([]byte, 256)
		for {
			select {
			case <-p.closed:
				return
			default:
			}
			n, err := f.Read(buf)
			if n > 0 {
				select {
				case p.ch <- append([]byte(nil), buf[:n]...):
				case <-p.closed:
					return
				}
			}
			if err != nil && err != io.EOF {
				return
			}
		}
	}()
	t.Cleanup(func() { close(p.closed); time.Sleep(150 * time.Millisecond); f.Close() })
	return p
}

func (p *TTY) Read(b []byte) (int, error) {
	if len(p.rest) == 0 {
		select {
		case c, ok := <-p.ch:
			if !ok {
				return 0, io.EOF
			}
			p.rest = c
		case <-time.After(20 * time.Millisecond):
			return 0, nil
		}
	}
	n := copy(b, p.rest)
	p.rest = p.rest[n:]
	return n, nil
}

func (p *TTY) Write(b []byte) (int, error) { return p.f.Write(b) }

// Drain discards bytes queued on the port.
func (p *TTY) Drain() {
	end := time.After(time.Second)
	for {
		select {
		case <-p.ch:
		case <-time.After(200 * time.Millisecond):
			p.rest = nil
			return
		case <-end:
			p.rest = nil
			return
		}
	}
}
