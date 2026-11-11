package wire

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	cases := []Packet{
		{Lane: 0, RaceID: 7, Seq: 1, Payload: Start{StartTS: 1_700_000_000_123_456_789, Event: "100 free"}},
		{Lane: 0, RaceID: 7, Seq: 1, Payload: Start{StartTS: -1, Event: ""}},
		{Lane: 3, RaceID: 7, Seq: 2, Payload: Split{DistanceM: 25, Elapsed: 12_340 * time.Millisecond}},
		{Lane: 3, RaceID: 7, Seq: 5, Payload: Finish{DistanceM: 100, Elapsed: 55_820 * time.Millisecond}},
		{Lane: 8, RaceID: 7, Payload: Heartbeat{LatestSeq: 4}},
		{Lane: 2, RaceID: 7, Payload: Nack{FromSeq: 3, ToSeq: 9}},
		{Lane: 1, Payload: SyncReq{T1: 42}},
		{Lane: 1, Payload: SyncResp{T1: 42, T2: 43, T3: 44}},
	}
	for _, want := range cases {
		t.Run(want.Payload.Type().String(), func(t *testing.T) {
			b, err := Marshal(want)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			got, err := Unmarshal(b)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round trip mismatch\n got: %#v\nwant: %#v", got, want)
			}
		})
	}
}

// Pins the exact header bytes so the format can't drift silently.
func TestHeaderLayout(t *testing.T) {
	b, err := Marshal(Packet{Lane: 4, RaceID: 0x01020304, Seq: 0x0A0B0C0D, Payload: Heartbeat{LatestSeq: 9}})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x53, 0x54, // magic
		0x01,             // version
		0x04,             // type HEARTBEAT
		0x04,             // lane
		0x00, 0x00, 0x00, // reserved
		0x01, 0x02, 0x03, 0x04, // race_id
		0x0A, 0x0B, 0x0C, 0x0D, // seq
		0x00, 0x00, 0x00, 0x04, // payload_len
		0x00, 0x00, 0x00, 0x09, // latest_seq
	}
	if !bytes.Equal(b, want) {
		t.Fatalf("got  % x\nwant % x", b, want)
	}
}

func TestUnmarshalRejects(t *testing.T) {
	good, _ := Marshal(Packet{Lane: 1, RaceID: 1, Seq: 1, Payload: Split{DistanceM: 25, Elapsed: time.Second}})

	mutate := func(f func(b []byte) []byte) []byte {
		return f(append([]byte(nil), good...))
	}

	cases := map[string]struct {
		in   []byte
		want error
	}{
		"empty":          {nil, ErrShort},
		"short header":   {good[:HeaderSize-1], ErrShort},
		"bad magic":      {mutate(func(b []byte) []byte { b[0] = 0; return b }), ErrBadMagic},
		"bad version":    {mutate(func(b []byte) []byte { b[2] = 99; return b }), ErrVersion},
		"unknown type":   {mutate(func(b []byte) []byte { b[3] = 200; return b }), ErrUnknownType},
		"truncated body": {good[:len(good)-1], ErrLength},
		"trailing bytes": {mutate(func(b []byte) []byte { return append(b, 0) }), ErrLength},
		"inverted nack": {func() []byte {
			b, _ := Marshal(Packet{Payload: Nack{FromSeq: 1, ToSeq: 2}})
			b[HeaderSize+3] = 9 // from_seq = 9 > to_seq = 2
			return b
		}(), ErrBadRange},
		"len lies": {mutate(func(b []byte) []byte {
			b[19]++ // payload_len says one more byte than present
			return b
		}), ErrLength},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Unmarshal(c.in); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestMarshalRejects(t *testing.T) {
	if _, err := Marshal(Packet{Payload: Start{Event: strings.Repeat("x", 256)}}); err == nil {
		t.Error("expected error for 256-byte event name")
	}
	if _, err := Marshal(Packet{Payload: Nack{FromSeq: 5, ToSeq: 4}}); err == nil {
		t.Error("expected error for inverted nack range")
	}
	if _, err := Marshal(Packet{}); err == nil {
		t.Error("expected error for nil payload")
	}
}

// Malformed datagrams come straight off the network; Unmarshal must never panic.
func FuzzUnmarshal(f *testing.F) {
	seed, _ := Marshal(Packet{Lane: 1, RaceID: 1, Seq: 1, Payload: Start{StartTS: 1, Event: "50 fly"}})
	f.Add(seed)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := Unmarshal(b)
		if err != nil {
			return
		}
		// Anything that decodes must re-encode to the same bytes.
		out, err := Marshal(p)
		if err != nil {
			t.Fatalf("decoded packet failed to re-encode: %v", err)
		}
		if !bytes.Equal(out, b) {
			// Reserved bytes are ignored on decode, so normalize them before comparing.
			norm := append([]byte(nil), b...)
			norm[5], norm[6], norm[7] = 0, 0, 0
			if !bytes.Equal(out, norm) {
				t.Fatalf("re-encode mismatch\n in: % x\nout: % x", b, out)
			}
		}
	})
}
