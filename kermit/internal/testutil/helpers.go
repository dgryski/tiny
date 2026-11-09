// Package testutil has helpers shared by the kermit tests. Nothing here needs
// the network or another program, so it builds with TinyGo as well.
package testutil

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func AllBytes() []byte {
	var b []byte
	for r := 0; r < 4; r++ {
		for i := 0; i < 256; i++ {
			b = append(b, byte(i))
		}
	}
	return b
}

// GzipSource is the uncompressed input behind GzipData.
func GzipSource() []byte {
	rng := rand.New(rand.NewSource(1))
	b := make([]byte, 20000)
	rng.Read(b)
	return append(b, bytes.Repeat([]byte("some compressible text\n"), 500)...)
}

// GzipData returns compressed bytes that cover every byte value.
func GzipData(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(GzipSource()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var seen [256]bool
	distinct := 0
	for _, c := range buf.Bytes() {
		if !seen[c] {
			seen[c] = true
			distinct++
		}
	}
	if distinct < 256 {
		t.Fatalf("gzip data only covers %d byte values", distinct)
	}
	return buf.Bytes()
}

// CheckGzip compares a decompressed *.gz file to the source.
func CheckGzip(t *testing.T, name string, data []byte) {
	t.Helper()
	if filepath.Ext(name) != ".gz" {
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Errorf("gzip header: %v", err)
		return
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Errorf("gunzip failed, line is not 8-bit clean: %v", err)
		return
	}
	if !bytes.Equal(out, GzipSource()) {
		t.Errorf("gunzip output differs from source")
	}
}

// MemConn is one end of an in-memory connection, for TinyGo which has no
// network. Writes never block, and reads block until data, close or deadline.
type MemConn struct {
	mu       sync.Mutex
	in       []byte
	eof      bool
	deadline time.Time
	wake     chan struct{}
	peer     *MemConn
}

var errDeadline = errors.New("i/o timeout")

// MemPair returns two connected ends.
func MemPair(t *testing.T) (a, b *MemConn) {
	a = &MemConn{wake: make(chan struct{}, 1)}
	b = &MemConn{wake: make(chan struct{}, 1)}
	a.peer, b.peer = b, a
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

func (c *MemConn) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *MemConn) Write(p []byte) (int, error) {
	c.peer.mu.Lock()
	c.peer.in = append(c.peer.in, p...)
	c.peer.mu.Unlock()
	c.peer.signal()
	return len(p), nil
}

func (c *MemConn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		if len(c.in) > 0 {
			n := copy(p, c.in)
			c.in = c.in[n:]
			c.mu.Unlock()
			return n, nil
		}
		if c.eof {
			c.mu.Unlock()
			return 0, io.EOF
		}
		dl := c.deadline
		c.mu.Unlock()

		var timeout <-chan time.Time
		if !dl.IsZero() {
			if !time.Now().Before(dl) {
				return 0, errDeadline
			}
			timeout = time.After(time.Until(dl))
		}
		select {
		case <-c.wake:
		case <-timeout:
		}
	}
}

// SetReadDeadline sets when a Read gives up.
func (c *MemConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	c.signal()
	return nil
}

func (c *MemConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

// Close ends the connection. The other end reads what is left, then EOF.
func (c *MemConn) Close() error {
	c.peer.mu.Lock()
	c.peer.eof = true
	c.peer.mu.Unlock()
	c.peer.signal()
	return nil
}
