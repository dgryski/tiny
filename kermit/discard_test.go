package kermit_test

import (
	"errors"
	"testing"

	"github.com/dgryski/tiny/kermit"
)

func TestDiscard(t *testing.T) {
	for _, tc := range []struct {
		name string
		z    string
		want int
	}{
		{"Z with D", "D", 1},
		{"plain Z", "", 0},
	} {
		discards := 0
		var got []byte
		e := kermit.NewEngine(nil, func(p []byte) error { got = append(got, p...); return nil })
		e.OnDiscard = func() error { discards++; return nil }
		_, err := receive(t, e,
			frame(t, 0, 'S', ""),
			frame(t, 1, 'F', "a"),
			frame(t, 2, 'D', "x"),
			frame(t, 3, 'Z', tc.z),
			frame(t, 4, 'B', ""),
		)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if discards != tc.want {
			t.Errorf("%s: OnDiscard called %d times, want %d", tc.name, discards, tc.want)
		}
		if string(got) != "x" {
			t.Errorf("%s: data = %q", tc.name, got)
		}
	}
}

func TestDiscardOnce(t *testing.T) {
	// The ACK for Z is lost, so the sender repeats it.
	discards := 0
	e := kermit.NewEngine(nil, nil)
	e.OnDiscard = func() error { discards++; return nil }
	_, err := receive(t, e,
		frame(t, 0, 'S', ""),
		frame(t, 1, 'F', "a"),
		frame(t, 2, 'Z', "D"),
		frame(t, 2, 'Z', "D"),
		frame(t, 3, 'B', ""),
	)
	if err != nil {
		t.Fatal(err)
	}
	if discards != 1 {
		t.Errorf("OnDiscard called %d times, want 1", discards)
	}
}

func TestDiscardError(t *testing.T) {
	boom := errors.New("boom")
	e := kermit.NewEngine(nil, nil)
	e.OnDiscard = func() error { return boom }
	out, err := receive(t, e,
		frame(t, 0, 'S', ""),
		frame(t, 1, 'F', "a"),
		frame(t, 2, 'Z', "D"),
	)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	ps := packets(out)
	if last := ps[len(ps)-1]; last.Type != 'E' {
		t.Errorf("last packet is %c, want an error packet", last.Type)
	}
}
