//go:build !tinygo

package kermit_test

// Device test against cmd/loopback, which keeps a file in RAM and sends it
// back. Set KERMIT_QEMU=1 for QEMU or KERMIT_DEVICE=port for a USB board.
//
// KERMIT_TARGET flashes the board first, KERMIT_DEVICE_CKERMIT=1 uses C-Kermit
// on it. TINYGO, QEMU and KERMIT_DEVICE_MAX (default 12288) are also read.

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dgryski/tiny/kermit"
	"github.com/dgryski/tiny/kermit/internal/testutil"
)

type link struct {
	capacity int // largest file the device accepts
	serial   func() kermit.SerialDevice
	attach   string // C-Kermit commands that connect, empty if unusable
}

type device struct {
	name    string
	newLink func(t *testing.T) *link
}

func toolPath(t *testing.T, env, def string) string {
	t.Helper()
	name := os.Getenv(env)
	if name == "" {
		name = def
	}
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found (set %s)", name, env)
	}
	return path
}

func buildFirmware(t *testing.T, dir, target string) string {
	t.Helper()
	tinygo := toolPath(t, "TINYGO", "tinygo")
	out := filepath.Join(dir, "loopback-"+target+".elf")
	cmd := exec.Command(tinygo, "build", "-target="+target, "-o", out, "./cmd/loopback")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building cmd/loopback for %s: %v\n%s", target, err, b)
	}
	return out
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// qemuDevice runs the firmware on the RISC-V virt machine. The test listens
// and the serial port connects, so the port is always ready.
func qemuDevice(t *testing.T, firmware string) *device {
	qemu := toolPath(t, "QEMU", "qemu-system-riscv32")
	return &device{
		name: "qemu",
		newLink: func(t *testing.T) *link {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()

			// The machine matches the cores setting in targets/riscv-qemu.json.
			cmd := exec.Command(qemu,
				"-machine", "virt,aclint=on", "-smp", "4",
				"-display", "none", "-monitor", "none",
				"-serial", "tcp:"+ln.Addr().String(),
				"-bios", "none", "-kernel", firmware)
			out := &syncBuf{}
			cmd.Stdout, cmd.Stderr = out, out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { cmd.Wait(); close(done) }()
			t.Cleanup(func() {
				cmd.Process.Kill()
				<-done
				if t.Failed() && out.String() != "" {
					t.Logf("qemu said:\n%s", out.String())
				}
			})

			ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
			qc, err := ln.Accept()
			if err != nil {
				t.Fatalf("qemu did not connect its serial port: %v\n%s", err, out.String())
			}
			t.Cleanup(func() { qc.Close() })

			// C-Kermit connects here and bytes are bridged both ways.
			kln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { kln.Close() })
			go func() {
				kc, err := kln.Accept()
				if err != nil {
					return
				}
				defer kc.Close()
				go io.Copy(qc, kc)
				io.Copy(kc, qc)
			}()

			return &link{
				capacity: 64 * 1024,
				serial:   func() kermit.SerialDevice { return pollConn{qc} },
				attach:   "set host " + kln.Addr().String() + " /raw-socket",
			}
		},
	}
}

func boardDevice(t *testing.T, port string) *device {
	capacity := 12 * 1024
	if s := os.Getenv("KERMIT_DEVICE_MAX"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatalf("KERMIT_DEVICE_MAX: %v", err)
		}
		capacity = n
	}
	return &device{
		name: "board",
		newLink: func(t *testing.T) *link {
			path := port
			if m, _ := filepath.Glob(port); len(m) > 0 {
				path = m[0]
			}
			var dev *testutil.TTY
			l := &link{
				capacity: capacity,
				serial: func() kermit.SerialDevice {
					if dev == nil {
						dev = testutil.OpenTTY(t, path)
						dev.Drain()
					}
					return dev
				},
			}
			if os.Getenv("KERMIT_DEVICE_CKERMIT") != "" {
				l.attach = "set line " + path + ",set speed 115200,set flow none,set carrier-watch off"
			}
			return l
		},
	}
}

func flashBoard(t *testing.T, target, port string) {
	t.Helper()
	tinygo := toolPath(t, "TINYGO", "tinygo")
	args := []string{"flash", "-target=" + target}
	if !strings.ContainsAny(port, "*?[") {
		args = append(args, "-port", port)
	}
	cmd := exec.Command(tinygo, append(args, "./cmd/loopback")...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("flashing %s: %v\n%s", target, err, b)
	}
	for i := 0; i < 100; i++ {
		if m, _ := filepath.Glob(port); len(m) > 0 {
			time.Sleep(time.Second) // let the firmware start
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no serial port %s after flashing", port)
}

type peer struct {
	name      string
	roundTrip func(t *testing.T, l *link, name string, data []byte) (gotName string, got []byte, err error)
	usable    func(l *link) bool
}

func deviceEngine(onHeader func(string) error, onData func([]byte) error) *kermit.Engine {
	e := kermit.NewEngine(onHeader, onData)
	e.Timeout = 2 * time.Second
	e.MaxRetries = 10
	deadline := time.Now().Add(90 * time.Second)
	e.Cancel = func() bool { return time.Now().After(deadline) }
	return e
}

var goPeer = peer{
	name:   "go",
	usable: func(*link) bool { return true },
	roundTrip: func(t *testing.T, l *link, name string, data []byte) (string, []byte, error) {
		dev := l.serial()
		if err := deviceEngine(nil, nil).Send(dev, name, data); err != nil {
			return "", nil, fmt.Errorf("sending: %w", err)
		}
		var gotName string
		var got []byte
		e := deviceEngine(
			func(n string) error { gotName = n; return nil },
			func(p []byte) error { got = append(got, p...); return nil },
		)
		if err := e.ProcessStream(dev); err != nil {
			return gotName, got, fmt.Errorf("receiving: %w", err)
		}
		return gotName, got, nil
	},
}

var ckermitPeer = peer{
	name:   "ckermit",
	usable: func(l *link) bool { _, err := exec.LookPath("kermit"); return err == nil && l.attach != "" },
	roundTrip: func(t *testing.T, l *link, name string, data []byte) (string, []byte, error) {
		srcDir, recvDir := t.TempDir(), t.TempDir()
		src := filepath.Join(srcDir, name)
		if err := os.WriteFile(src, data, 0o644); err != nil {
			t.Fatal(err)
		}
		script := strings.Join([]string{
			l.attach, "set file type binary", "set file names literal",
			"set receive confirm off", "send " + src, "receive", "exit",
		}, ",")
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "kermit", "-Y", "-q", "-C", script)
		cmd.Dir = recvDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", nil, fmt.Errorf("kermit: %v\n%s", err, out)
		}
		entries, _ := os.ReadDir(recvDir)
		if len(entries) != 1 {
			var names []string
			for _, en := range entries {
				names = append(names, en.Name())
			}
			return "", nil, fmt.Errorf("C-Kermit received %q, want one file\n%s", names, out)
		}
		got, err := os.ReadFile(filepath.Join(recvDir, entries[0].Name()))
		return entries[0].Name(), got, err
	},
}

type deviceCase struct {
	name string
	data []byte
}

func gzipSized(t *testing.T, n int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	src := make([]byte, n)
	rng.Read(src)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(src)
	zw.Close()
	return buf.Bytes()
}

func deviceCases(t *testing.T, capacity int) []deviceCase {
	rng := rand.New(rand.NewSource(3))
	random := func(n int) []byte {
		b := make([]byte, n)
		rng.Read(b)
		return b
	}
	cases := []deviceCase{
		{"text.txt", []byte("hello from the device test\n")},
		{"all.bin", testutil.AllBytes()},
		{"zeros.bin", make([]byte, 4000)},
		{"controls.bin", bytes.Repeat([]byte{0, 1, 3, 13, 10, 26, 27, 127, '#', '~', '&', 0x80, 0xff}, 50)},
		{"a#b.bin", []byte("a name with a prefix character")},
		{"#lead", []byte("leading prefix")},
		{"trail#", []byte("trailing prefix")},
		{"empty.bin", nil},
		{"gz.bin", gzipSized(t, min(5000, capacity/3))},
	}
	// Sizes around the packet boundary, where quoting changes the packing.
	for _, n := range []int{1, 77, 78, 91, 92, 93} {
		cases = append(cases, deviceCase{fmt.Sprintf("rand%d.bin", n), random(n)})
	}
	cases = append(cases, deviceCase{"big.bin", random(min(capacity-200, 40000))})
	return cases
}

func assertRoundTrip(t *testing.T, l *link) {
	t.Helper()
	data := bytes.Repeat([]byte("the device still works "), 20)
	name, got, err := goPeer.roundTrip(t, l, "after.bin", data)
	if err != nil {
		t.Fatalf("the device did not recover: %v", err)
	}
	if name != "after.bin" || !bytes.Equal(got, data) {
		t.Fatalf("after recovery got %q with %d bytes, want after.bin with %d", name, len(got), len(data))
	}
}

type writeCounter struct {
	kermit.SerialDevice
	n *int
}

func (w writeCounter) Write(p []byte) (int, error) {
	*w.n++
	return w.SerialDevice.Write(p)
}

// readDeadline reports end of file once the time has passed.
type readDeadline struct {
	kermit.SerialDevice
	until time.Time
}

func (r readDeadline) Read(p []byte) (int, error) {
	if time.Now().After(r.until) {
		return 0, io.EOF
	}
	return r.SerialDevice.Read(p)
}

func TestDevice(t *testing.T) {
	var devices []*device
	if os.Getenv("KERMIT_QEMU") != "" {
		fw := buildFirmware(t, t.TempDir(), "riscv-qemu")
		devices = append(devices, qemuDevice(t, fw))
	}
	if port := os.Getenv("KERMIT_DEVICE"); port != "" {
		if target := os.Getenv("KERMIT_TARGET"); target != "" {
			flashBoard(t, target, port)
		}
		devices = append(devices, boardDevice(t, port))
	}
	if len(devices) == 0 {
		t.Skip("set KERMIT_QEMU=1 or KERMIT_DEVICE=<serial port>, see hardware_test.go")
	}

	for _, d := range devices {
		t.Run(d.name, func(t *testing.T) {
			peers := []peer{goPeer, ckermitPeer}

			t.Run("round trip", func(t *testing.T) {
				for _, p := range peers {
					t.Run(p.name, func(t *testing.T) {
						probe := d.newLink(t)
						if !p.usable(probe) {
							t.Skipf("%s cannot talk to this device", p.name)
						}
						for _, c := range deviceCases(t, probe.capacity) {
							t.Run(c.name, func(t *testing.T) {
								l := d.newLink(t)
								name, got, err := p.roundTrip(t, l, c.name, c.data)
								if err != nil {
									t.Fatal(err)
								}
								if name != c.name || !bytes.Equal(got, c.data) {
									t.Fatalf("got %q with %d bytes, want %q with %d", name, len(got), c.name, len(c.data))
								}
							})
						}
					})
				}
			})

			t.Run("file too big", func(t *testing.T) {
				l := d.newLink(t)
				dev := l.serial()
				big := bytes.Repeat([]byte("0123456789"), l.capacity/10+100)
				err := deviceEngine(nil, nil).Send(dev, "big.bin", big)
				var re *kermit.RemoteError
				if !errors.As(err, &re) {
					t.Fatalf("Send returned %v, want the device's error packet", err)
				}
				t.Logf("the device said: %q", string(re.Msg))
				assertRoundTrip(t, l)
			})

			t.Run("we cancel while sending", func(t *testing.T) {
				l := d.newLink(t)
				var sent int
				dev := writeCounter{l.serial(), &sent}
				e := deviceEngine(nil, nil)
				cancel := e.Cancel
				e.Cancel = func() bool { return sent >= 12 || cancel() }
				err := e.Send(dev, "big.bin", bytes.Repeat([]byte("0123456789"), 1000))
				if !errors.Is(err, kermit.ErrCanceled) {
					t.Fatalf("Send returned %v", err)
				}
				assertRoundTrip(t, l)
			})

			t.Run("we cancel while receiving", func(t *testing.T) {
				l := d.newLink(t)
				dev := l.serial()
				data := bytes.Repeat([]byte("0123456789"), 1000)
				if err := deviceEngine(nil, nil).Send(dev, "back.bin", data); err != nil {
					t.Fatal(err)
				}
				calls := 0
				e := deviceEngine(nil, func([]byte) error { calls++; return nil })
				cancel := e.Cancel
				e.Cancel = func() bool { return calls >= 3 || cancel() }
				if err := e.ProcessStream(dev); !errors.Is(err, kermit.ErrCanceled) {
					t.Fatalf("ProcessStream returned %v", err)
				}
				assertRoundTrip(t, l)
			})

			t.Run("peer goes silent", func(t *testing.T) {
				if testing.Short() {
					t.Skip("takes about 15 seconds")
				}
				l := d.newLink(t)
				dev := l.serial()
				// Stop mid transfer without an error packet. The Send-Init asks for a 2 second timeout.
				until := func(d time.Duration) kermit.SerialDevice {
					return readDeadline{dev, time.Now().Add(d)}
				}
				write := func(seq uint8, typ byte, data string) {
					p := kermit.Packet{Seq: seq, Type: typ, Data: []byte(data)}
					if err := kermit.EncodePacket(dev, &p); err != nil {
						t.Fatal(err)
					}
				}
				for i, step := range []struct {
					typ  byte
					data string
				}{{'S', sinit(94, 2, 0, 0, 13, '#')}, {'F', "x.bin"}, {'D', "hello"}} {
					write(uint8(i), step.typ, step.data)
					p, err := kermit.DecodePacket(until(5 * time.Second))
					if err != nil || p.Type != 'Y' {
						t.Fatalf("step %c: got %+v, err %v", step.typ, p, err)
					}
				}
				start := time.Now()
				for {
					p, err := kermit.DecodePacket(until(30 * time.Second))
					if err != nil {
						t.Fatalf("the device never gave up: %v", err)
					}
					if p.Type == 'E' {
						t.Logf("the device gave up after %v: %q", time.Since(start).Round(time.Second), p.Data)
						break
					}
				}
				assertRoundTrip(t, l)
			})
		})
	}
}
