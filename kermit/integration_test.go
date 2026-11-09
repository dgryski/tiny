//go:build !tinygo

package kermit_test

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dgryski/tiny/kermit"
	"github.com/dgryski/tiny/kermit/internal/testutil"
)

func TestEngineAgainstCKermit(t *testing.T) {
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}

	tests := []struct {
		name    string
		content []byte
	}{
		{"short.txt", []byte("Hello from the C-Kermit integration test!\n")},
		{"multi.txt", bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog.\n"), 100)},
		{"empty.txt", nil},
		{"all.bin", testutil.AllBytes()},
		{"data.gz", testutil.GzipData(t)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, tc.name)
			if err := os.WriteFile(src, tc.content, 0o644); err != nil {
				t.Fatal(err)
			}

			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			// A raw socket avoids Telnet newline translation.
			script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,send %s,exit", ln.Addr().String(), src)
			cmd := exec.Command(path, "-Y", "-q", "-C", script)
			var stderr, stdout bytes.Buffer
			cmd.Stderr = &stderr
			cmd.Stdout = &stdout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()

			if tcp, ok := ln.(*net.TCPListener); ok {
				_ = tcp.SetDeadline(time.Now().Add(5 * time.Second))
			}
			conn, err := ln.Accept()
			if err != nil {
				t.Fatalf("kermit did not connect: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

			var name string
			var got bytes.Buffer
			e := kermit.NewEngine(
				func(f string) error { name = filepath.Base(f); return nil },
				func(b []byte) error { got.Write(b); return nil },
			)

			if err := e.ProcessStream(conn); err != nil {
				t.Fatalf("ProcessStream: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			_ = conn.Close()
			_ = cmd.Wait()

			if name != tc.name {
				t.Errorf("filename = %q, want %q", name, tc.name)
			}
			if !bytes.Equal(got.Bytes(), tc.content) {
				t.Errorf("content mismatch: got %d bytes, want %d", got.Len(), len(tc.content))
			}
			testutil.CheckGzip(t, tc.name, got.Bytes())
		})
	}
}

func TestEngineSendToCKermit(t *testing.T) {
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}

	tests := []struct {
		name    string
		content []byte
	}{
		{"short.txt", []byte("Hello from the engine\n")},
		{"multi.txt", bytes.Repeat([]byte("The quick brown fox jumps over the lazy dog.\n"), 100)},
		{"all.bin", testutil.AllBytes()},
		{"data.gz", testutil.GzipData(t)},
		{"empty.txt", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,set receive confirm off,receive,exit", ln.Addr().String())
			cmd := exec.Command(path, "-Y", "-q", "-C", script)
			cmd.Dir = dir
			var stderr, stdout bytes.Buffer
			cmd.Stderr = &stderr
			cmd.Stdout = &stdout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()

			if tcp, ok := ln.(*net.TCPListener); ok {
				_ = tcp.SetDeadline(time.Now().Add(5 * time.Second))
			}
			conn, err := ln.Accept()
			if err != nil {
				t.Fatalf("kermit did not connect: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

			e := kermit.NewEngine(nil, nil)
			e.Timeout = time.Second
			if err := e.Send(pollConn{conn}, tc.name, tc.content); err != nil {
				t.Fatalf("Send: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
			}
			// Send returns after the final ACK.
			_ = conn.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()

			got, err := os.ReadFile(filepath.Join(dir, tc.name))
			if err != nil {
				t.Fatalf("received file missing: %v", err)
			}
			if !bytes.Equal(got, tc.content) {
				t.Errorf("content mismatch: got %d bytes, want %d", len(got), len(tc.content))
			}
			testutil.CheckGzip(t, tc.name, got)
		})
	}
}

// pollConn turns read deadline errors into empty reads.
type pollConn struct{ net.Conn }

func (p pollConn) Read(b []byte) (int, error) {
	_ = p.Conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	n, err := p.Conn.Read(b)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return n, nil
	}
	return n, err
}

var oddNames = []string{"a#b.txt", "c##d.txt", "e~f.txt", "g&h.txt", "#lead.txt", "trail#"}

// TestFilenamesFromCKermit sends files named with prefix characters from
// C-Kermit to the engine.
func TestFilenamesFromCKermit(t *testing.T) {
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}
	for _, name := range oddNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, name)
			if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,send %s,exit", ln.Addr().String(), src)
			cmd := exec.Command(path, "-Y", "-q", "-C", script)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()

			ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
			conn, err := ln.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))

			var got string
			e := kermit.NewEngine(
				func(f string) error { got = filepath.Base(f); return nil },
				func(b []byte) error { return nil },
			)
			if err := e.ProcessStream(conn); err != nil {
				t.Fatalf("ProcessStream: %v", err)
			}
			conn.Close()
			cmd.Wait()
			if got != name {
				t.Errorf("name = %q, want %q", got, name)
			}
		})
	}
}

// TestFilenamesToCKermit sends files named with prefix characters from the
// engine to C-Kermit.
func TestFilenamesToCKermit(t *testing.T) {
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}
	for _, name := range oddNames {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,set receive confirm off,receive,exit", ln.Addr().String())
			cmd := exec.Command(path, "-Y", "-q", "-C", script)
			cmd.Dir = dir
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()

			ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
			conn, err := ln.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(10 * time.Second))

			e := kermit.NewEngine(nil, nil)
			e.Timeout = time.Second
			if err := e.Send(pollConn{conn}, name, []byte("x")); err != nil {
				t.Fatalf("Send: %v", err)
			}
			conn.Close()
			cmd.Process.Kill()
			cmd.Wait()

			entries, _ := os.ReadDir(dir)
			var names []string
			for _, en := range entries {
				names = append(names, en.Name())
			}
			if len(names) != 1 || names[0] != name {
				t.Errorf("C-Kermit wrote %q, want [%q]", names, name)
			}
		})
	}
}

type teeConn struct {
	net.Conn
	mu    sync.Mutex
	wrote bytes.Buffer
}

func (c *teeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.wrote.Write(p)
	c.mu.Unlock()
	return c.Conn.Write(p)
}

func (c *teeConn) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.wrote.Bytes()...)
}

// ckermitWants asks C-Kermit for 3 NUL pad characters before and a linefeed
// after every packet it receives, with packets no longer than 60.
const ckermitWants = "set receive padding 3,set receive pad-character 0,set receive end-of-packet 10,set receive packet-length 60"

// TestSendHonorsCKermitParameters sends to a C-Kermit that asks for padding,
// a linefeed terminator and short packets.
func TestSendHonorsCKermitParameters(t *testing.T) {
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}
	dir := t.TempDir()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,set receive confirm off,%s,receive,exit", ln.Addr().String(), ckermitWants)
	cmd := exec.Command(path, "-Y", "-q", "-C", script)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	conn := &teeConn{Conn: c}

	content := bytes.Repeat([]byte("0123456789abcdef"), 40)
	e := kermit.NewEngine(nil, nil)
	e.Timeout = time.Second
	if err := e.Send(pollConn{conn}, "p.bin", content); err != nil {
		t.Fatalf("Send: %v", err)
	}
	c.Close()
	cmd.Process.Kill()
	cmd.Wait()

	got, _ := os.ReadFile(filepath.Join(dir, "p.bin"))
	if !bytes.Equal(got, content) {
		t.Errorf("file differs: %d bytes, want %d", len(got), len(content))
	}

	wrote := conn.bytes()
	cr := bytes.IndexByte(wrote, '\r')
	if wrote[0] != kermit.Mark || cr < 0 {
		t.Fatalf("Send-Init should be unpadded and end in CR: %q", wrote[:min(len(wrote), 20)])
	}
	later := wrote[cr+1:]
	ps := packets(wrote)
	if len(ps) < 6 {
		t.Fatalf("only %d packets", len(ps))
	}
	// Repeated Send-Inits go out before the parameters are known, so skip them.
	after := 0
	for _, p := range ps {
		if p.Type == 'S' {
			continue
		}
		after++
		if p.Len > 60 {
			t.Errorf("%c packet LEN %d, C-Kermit asked for at most 60", p.Type, p.Len)
		}
	}
	if n := bytes.Count(later, []byte{0, 0, 0, kermit.Mark}); n != after {
		t.Errorf("%d packets had the 3 pad characters, want %d", n, after)
	}
	if n := bytes.Count(later, []byte{'\n'}); n < after {
		t.Errorf("%d linefeed terminators, want %d", n, after)
	}
}

// TestReceiveHonorsCKermitParameters receives from a C-Kermit that asks for
// padding and a linefeed terminator on the packets it receives.
func TestReceiveHonorsCKermitParameters(t *testing.T) {
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "r.bin")
	content := bytes.Repeat([]byte("0123456789abcdef"), 40)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,%s,send %s,exit", ln.Addr().String(), ckermitWants, src)
	cmd := exec.Command(path, "-Y", "-q", "-C", script)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	conn := &teeConn{Conn: c}

	var got bytes.Buffer
	e := kermit.NewEngine(nil, func(b []byte) error { got.Write(b); return nil })
	if err := e.ProcessStream(conn); err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	c.Close()
	cmd.Wait()
	if !bytes.Equal(got.Bytes(), content) {
		t.Errorf("data differs: %d bytes, want %d", got.Len(), len(content))
	}

	// The parameters arrive in the Send-Init, so every reply is padded.
	wrote := conn.bytes()
	n := bytes.Count(wrote, []byte{0, 0, 0, kermit.Mark})
	if n < 6 || wrote[0] != 0 || wrote[len(wrote)-1] != '\n' {
		t.Errorf("replies not padded and terminated (%d padded): %q", n, wrote[:min(len(wrote), 24)])
	}
}

// ckermitReceiver starts C-Kermit in receive mode with extra script commands first.
func ckermitReceiver(t *testing.T, extra string) (conn net.Conn, cmd *exec.Cmd, out *bytes.Buffer) {
	t.Helper()
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,%sreceive,exit", ln.Addr().String(), extra)
	cmd = exec.Command(path, "-Y", "-q", "-C", script)
	cmd.Dir = t.TempDir()
	out = &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	conn, err = ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	return conn, cmd, out
}

func waitExit(cmd *exec.Cmd, d time.Duration) bool {
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// TestErrorPacketFromCKermit gets an E packet from C-Kermit, which declines
// the incoming file because confirmation is on, and checks we do not answer it.
func TestErrorPacketFromCKermit(t *testing.T) {
	c, _, _ := ckermitReceiver(t, "set receive confirm on,")
	conn := &teeConn{Conn: c}

	e := kermit.NewEngine(nil, nil)
	e.Timeout = time.Second
	err := e.Send(pollConn{conn}, "x.bin", []byte("data"))
	var re *kermit.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want the error packet from C-Kermit", err)
	}
	t.Logf("C-Kermit said: %q", string(re.Msg))
	for _, p := range packets(conn.bytes()) {
		if p.Type == 'E' {
			t.Errorf("we answered the error packet with one")
		}
	}
}

// TestOurErrorPacketStopsCKermit fails a started send with a too long name and
// checks C-Kermit stops.
func TestOurErrorPacketStopsCKermit(t *testing.T) {
	c, cmd, out := ckermitReceiver(t, "set receive confirm off,")
	conn := &teeConn{Conn: c}

	// 50 prefix characters quote to 100 bytes, more than a packet holds.
	name := string(bytes.Repeat([]byte("#"), 50))
	e := kermit.NewEngine(nil, nil)
	e.Timeout = time.Second
	err := e.Send(pollConn{conn}, name, []byte("data"))
	if !errors.Is(err, kermit.ErrTooLong) {
		t.Fatalf("err = %v, want ErrTooLong", err)
	}
	last := lastPacket(conn.bytes())
	if last == nil || last.Type != 'E' {
		t.Fatalf("no error packet was sent, last = %+v", last)
	}
	// C-Kermit should give up and finish its script.
	if !waitExit(cmd, 5*time.Second) {
		t.Fatalf("C-Kermit did not stop after our error packet:\n%s", out.String())
	}
}

// TestReceiverErrorStopsCKermit sends an error packet because the receiver's
// callback fails, and checks a C-Kermit sender stops.
func TestReceiverErrorStopsCKermit(t *testing.T) {
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "z.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("x"), 500), 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,send %s,exit", ln.Addr().String(), src)
	cmd := exec.Command(path, "-Y", "-q", "-C", script)
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))

	calls := 0
	e := kermit.NewEngine(nil, func([]byte) error {
		calls++
		if calls == 2 {
			return errors.New("flash full")
		}
		return nil
	})
	if err := e.ProcessStream(c); err == nil {
		t.Fatal("ProcessStream succeeded although the callback failed")
	}
	if !waitExit(cmd, 5*time.Second) {
		t.Fatalf("C-Kermit did not stop after our error packet:\n%s", out.String())
	}
	t.Logf("C-Kermit output: %q", out.String())
}

type slowConn struct {
	net.Conn
	pause time.Duration
}

func (c slowConn) Write(p []byte) (int, error) {
	time.Sleep(c.pause)
	return c.Conn.Write(p)
}

func TestCancelSendToCKermit(t *testing.T) {
	c, cmd, out := ckermitReceiver(t, "set receive confirm off,")
	conn := &teeConn{Conn: c}

	e := kermit.NewEngine(nil, nil)
	e.Timeout = time.Second
	e.Cancel = func() bool { return len(packets(conn.bytes())) >= 8 }
	err := e.Send(pollConn{conn}, "big.bin", bytes.Repeat([]byte("0123456789"), 5000))
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	wantCancelPacket(t, conn.bytes())
	if !waitExit(cmd, 5*time.Second) {
		t.Fatalf("C-Kermit kept waiting after our cancel:\n%s", out.String())
	}
}

func TestCancelByTimeSendToCKermit(t *testing.T) {
	c, cmd, out := ckermitReceiver(t, "set receive confirm off,")
	conn := &teeConn{Conn: c}

	// The first Send-Init is lost, so the transfer starts after the 1 second retry.
	deadline := time.Now().Add(2500 * time.Millisecond)
	e := kermit.NewEngine(nil, nil)
	e.Timeout = time.Second
	e.Cancel = func() bool { return time.Now().After(deadline) }
	slow := slowConn{Conn: conn, pause: 20 * time.Millisecond}
	err := e.Send(pollConn{slow}, "slow.bin", bytes.Repeat([]byte("0123456789"), 10000))
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	wantCancelPacket(t, conn.bytes())
	if !waitExit(cmd, 5*time.Second) {
		t.Fatalf("C-Kermit kept waiting after the time was up:\n%s", out.String())
	}
}

func TestCancelReceiveFromCKermit(t *testing.T) {
	path, err := exec.LookPath("kermit")
	if err != nil {
		t.Skip("kermit not found in PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(src, bytes.Repeat([]byte("0123456789"), 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	script := fmt.Sprintf("set host %s /raw-socket,set file type binary,set file names literal,send %s,exit", ln.Addr().String(), src)
	cmd := exec.Command(path, "-Y", "-q", "-C", script)
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	ln.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	conn := &teeConn{Conn: c}

	calls := 0
	e := kermit.NewEngine(nil, func([]byte) error { calls++; return nil })
	e.Cancel = func() bool { return calls >= 5 }
	err = e.ProcessStream(conn)
	if !errors.Is(err, kermit.ErrCanceled) {
		t.Fatalf("err = %v", err)
	}
	wantCancelPacket(t, conn.bytes())
	if !waitExit(cmd, 5*time.Second) {
		t.Fatalf("C-Kermit kept sending after our cancel:\n%s", out.String())
	}
}
