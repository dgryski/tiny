package kermit_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/dgryski/tiny/kermit"
	"github.com/dgryski/tiny/kermit/internal/testutil"
)

func TestOddNamesLoopback(t *testing.T) {
	names := []string{"a#b.txt", "c##d.txt", "#lead", "trail#", "e~f&g", "tab\there", "del\x7fx", "hi\xe9\xa3"}
	for _, name := range names {
		a, b := testutil.MemPair(t)
		_ = a.SetDeadline(time.Now().Add(5 * time.Second))
		_ = b.SetDeadline(time.Now().Add(5 * time.Second))

		var got string
		recv := kermit.NewEngine(func(f string) error { got = f; return nil }, nil)
		done := make(chan error, 1)
		go func() { done <- recv.ProcessStream(b) }()

		if err := kermit.NewEngine(nil, nil).Send(a, name, []byte("x")); err != nil {
			t.Fatalf("%q: Send: %v", name, err)
		}
		if err := <-done; err != nil {
			t.Fatalf("%q: ProcessStream: %v", name, err)
		}
		a.Close()
		b.Close()
		if got != name {
			t.Errorf("name = %q, want %q", got, name)
		}
	}
}

func TestNameTooLongForPacket(t *testing.T) {
	// 50 prefix characters quote to 100 bytes, more than a packet holds.
	name := string(bytes.Repeat([]byte("#"), 50))
	var in bytes.Buffer
	in.Write(frame(t, 0, 'Y', ""))
	m := &mockSerial{in: &in, out: &bytes.Buffer{}}
	if err := kermit.NewEngine(nil, nil).Send(m, name, nil); !errors.Is(err, kermit.ErrTooLong) {
		t.Fatalf("got %v, want ErrTooLong", err)
	}
}

func TestSendReceiveLoopback(t *testing.T) {
	content := testutil.AllBytes()
	a, b := testutil.MemPair(t)
	defer a.Close()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(5 * time.Second))
	_ = b.SetDeadline(time.Now().Add(5 * time.Second))

	var name string
	var got bytes.Buffer
	recv := kermit.NewEngine(
		func(f string) error { name = f; return nil },
		func(p []byte) error { got.Write(p); return nil },
	)
	done := make(chan error, 1)
	go func() { done <- recv.ProcessStream(b) }()

	send := kermit.NewEngine(nil, nil)
	if err := send.Send(a, "data.bin", content); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if name != "data.bin" || !bytes.Equal(got.Bytes(), content) {
		t.Fatalf("name=%q got %d bytes want %d", name, got.Len(), len(content))
	}
}

func TestSendReceiveGzip(t *testing.T) {
	content := testutil.GzipData(t)
	a, b := testutil.MemPair(t)
	defer a.Close()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(5 * time.Second))
	_ = b.SetDeadline(time.Now().Add(5 * time.Second))

	var got bytes.Buffer
	recv := kermit.NewEngine(nil, func(p []byte) error { got.Write(p); return nil })
	done := make(chan error, 1)
	go func() { done <- recv.ProcessStream(b) }()

	if err := kermit.NewEngine(nil, nil).Send(a, "data.gz", content); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), content) {
		t.Fatalf("got %d bytes want %d", got.Len(), len(content))
	}
	testutil.CheckGzip(t, "data.gz", got.Bytes())
}

func TestSendRemoteError(t *testing.T) {
	a, b := testutil.MemPair(t)
	defer a.Close()
	defer b.Close()
	_ = a.SetDeadline(time.Now().Add(5 * time.Second))
	_ = b.SetDeadline(time.Now().Add(5 * time.Second))

	recv := kermit.NewEngine(
		func(string) error { return errors.New("no space") },
		nil,
	)
	go recv.ProcessStream(b)

	send := kermit.NewEngine(nil, nil)
	err := send.Send(a, "x", []byte("hi"))
	var re *kermit.RemoteError
	if !errors.As(err, &re) {
		t.Fatalf("want RemoteError, got %v", err)
	}
}
