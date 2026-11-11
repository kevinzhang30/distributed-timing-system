// Package wire encodes and decodes the UDP race packets described in
// docs/PROTOCOL.md §2–3. All integers are big-endian (network byte order).
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

const (
	Magic      uint16 = 0x5354
	Version    uint8  = 1
	HeaderSize        = 20

	// MaxPacketSize keeps packets under a typical Ethernet MTU (1500) minus
	// IP (20) and UDP (8) headers, so they are never IP-fragmented.
	MaxPacketSize = 1472
)

// Type identifies the payload that follows the header.
type Type uint8

const (
	TypeStart     Type = 1
	TypeSplit     Type = 2
	TypeFinish    Type = 3
	TypeHeartbeat Type = 4
	TypeNack      Type = 5
	TypeSyncReq   Type = 10
	TypeSyncResp  Type = 11
)

func (t Type) String() string {
	switch t {
	case TypeStart:
		return "START"
	case TypeSplit:
		return "SPLIT"
	case TypeFinish:
		return "FINISH"
	case TypeHeartbeat:
		return "HEARTBEAT"
	case TypeNack:
		return "NACK"
	case TypeSyncReq:
		return "SYNC_REQ"
	case TypeSyncResp:
		return "SYNC_RESP"
	}
	return fmt.Sprintf("Type(%d)", uint8(t))
}

var (
	ErrShort       = errors.New("wire: packet too short")
	ErrBadMagic    = errors.New("wire: bad magic")
	ErrVersion     = errors.New("wire: unsupported version")
	ErrUnknownType = errors.New("wire: unknown packet type")
	ErrLength      = errors.New("wire: payload length mismatch")
	ErrTooLarge    = errors.New("wire: packet exceeds max size")
	ErrBadRange    = errors.New("wire: nack from_seq > to_seq")
)

// Packet is one decoded UDP datagram: header fields plus a typed payload.
type Packet struct {
	Lane    uint8 // 0 = starter
	RaceID  uint32
	Seq     uint32 // per (race, lane); 0 for heartbeats and sync packets
	Payload Payload
}

// Payload is implemented (by value) by every packet body type.
type Payload interface {
	Type() Type
	appendTo(b []byte) ([]byte, error)
}

// decoder is implemented by pointers to payload types.
type decoder interface {
	decode(b []byte) error
}

// Start is sent by the starter to begin a race.
type Start struct {
	StartTS int64 // starter clock, unix nanoseconds
	Event   string
}

// Split is a lane touchpad hit partway through a race.
type Split struct {
	DistanceM uint16
	Elapsed   time.Duration // starter clock
}

// Finish is the final touch. Same layout as Split, different meaning.
type Finish struct {
	DistanceM uint16
	Elapsed   time.Duration
}

// Heartbeat lets receivers detect tail loss.
type Heartbeat struct {
	LatestSeq uint32
}

// Nack asks a lane to resend seqs in [FromSeq, ToSeq].
type Nack struct {
	FromSeq, ToSeq uint32
}

// SyncReq / SyncResp implement NTP-style clock offset estimation.
type SyncReq struct {
	T1 int64
}

type SyncResp struct {
	T1, T2, T3 int64
}

func (Start) Type() Type     { return TypeStart }
func (Split) Type() Type     { return TypeSplit }
func (Finish) Type() Type    { return TypeFinish }
func (Heartbeat) Type() Type { return TypeHeartbeat }
func (Nack) Type() Type      { return TypeNack }
func (SyncReq) Type() Type   { return TypeSyncReq }
func (SyncResp) Type() Type  { return TypeSyncResp }

var be = binary.BigEndian

// Marshal encodes p into a new byte slice.
func Marshal(p Packet) ([]byte, error) {
	if p.Payload == nil {
		return nil, errors.New("wire: nil payload")
	}
	b := make([]byte, HeaderSize, 64)
	be.PutUint16(b[0:], Magic)
	b[2] = Version
	b[3] = uint8(p.Payload.Type())
	b[4] = p.Lane
	// b[5:8] reserved, already zero
	be.PutUint32(b[8:], p.RaceID)
	be.PutUint32(b[12:], p.Seq)

	b, err := p.Payload.appendTo(b)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxPacketSize {
		return nil, ErrTooLarge
	}
	be.PutUint32(b[16:], uint32(len(b)-HeaderSize))
	return b, nil
}

// Unmarshal decodes a datagram. It never panics on malformed input.
func Unmarshal(b []byte) (Packet, error) {
	if len(b) < HeaderSize {
		return Packet{}, ErrShort
	}
	if be.Uint16(b[0:]) != Magic {
		return Packet{}, ErrBadMagic
	}
	if b[2] != Version {
		return Packet{}, ErrVersion
	}
	payloadLen := be.Uint32(b[16:])
	if uint64(payloadLen) != uint64(len(b)-HeaderSize) {
		return Packet{}, ErrLength
	}

	var pl decoder
	switch Type(b[3]) {
	case TypeStart:
		pl = &Start{}
	case TypeSplit:
		pl = &Split{}
	case TypeFinish:
		pl = &Finish{}
	case TypeHeartbeat:
		pl = &Heartbeat{}
	case TypeNack:
		pl = &Nack{}
	case TypeSyncReq:
		pl = &SyncReq{}
	case TypeSyncResp:
		pl = &SyncResp{}
	default:
		return Packet{}, ErrUnknownType
	}
	if err := pl.decode(b[HeaderSize:]); err != nil {
		return Packet{}, err
	}

	return Packet{
		Lane:    b[4],
		RaceID:  be.Uint32(b[8:]),
		Seq:     be.Uint32(b[12:]),
		Payload: deref(pl),
	}, nil
}

// deref returns payloads by value so callers can type-switch on wire.Split
// rather than *wire.Split, matching what they pass to Marshal.
func deref(p decoder) Payload {
	switch v := p.(type) {
	case *Start:
		return *v
	case *Split:
		return *v
	case *Finish:
		return *v
	case *Heartbeat:
		return *v
	case *Nack:
		return *v
	case *SyncReq:
		return *v
	case *SyncResp:
		return *v
	}
	panic(fmt.Sprintf("wire: unhandled payload %T", p))
}

// --- payload encoding ---

func (s Start) appendTo(b []byte) ([]byte, error) {
	if len(s.Event) > 255 {
		return nil, fmt.Errorf("wire: event name %d bytes, max 255", len(s.Event))
	}
	b = be.AppendUint64(b, uint64(s.StartTS))
	b = append(b, uint8(len(s.Event)))
	return append(b, s.Event...), nil
}

func (s *Start) decode(b []byte) error {
	if len(b) < 9 {
		return ErrShort
	}
	s.StartTS = int64(be.Uint64(b))
	n := int(b[8])
	if len(b) != 9+n {
		return ErrLength
	}
	s.Event = string(b[9:])
	return nil
}

func appendTouch(b []byte, dist uint16, elapsed time.Duration) []byte {
	b = be.AppendUint16(b, dist)
	return be.AppendUint64(b, uint64(elapsed))
}

func decodeTouch(b []byte) (uint16, time.Duration, error) {
	if len(b) != 10 {
		return 0, 0, ErrLength
	}
	return be.Uint16(b), time.Duration(be.Uint64(b[2:])), nil
}

func (s Split) appendTo(b []byte) ([]byte, error) { return appendTouch(b, s.DistanceM, s.Elapsed), nil }
func (s *Split) decode(b []byte) (err error) {
	s.DistanceM, s.Elapsed, err = decodeTouch(b)
	return err
}

func (f Finish) appendTo(b []byte) ([]byte, error) {
	return appendTouch(b, f.DistanceM, f.Elapsed), nil
}
func (f *Finish) decode(b []byte) (err error) {
	f.DistanceM, f.Elapsed, err = decodeTouch(b)
	return err
}

func (h Heartbeat) appendTo(b []byte) ([]byte, error) { return be.AppendUint32(b, h.LatestSeq), nil }
func (h *Heartbeat) decode(b []byte) error {
	if len(b) != 4 {
		return ErrLength
	}
	h.LatestSeq = be.Uint32(b)
	return nil
}

func (n Nack) appendTo(b []byte) ([]byte, error) {
	if n.FromSeq > n.ToSeq {
		return nil, ErrBadRange
	}
	b = be.AppendUint32(b, n.FromSeq)
	return be.AppendUint32(b, n.ToSeq), nil
}

func (n *Nack) decode(b []byte) error {
	if len(b) != 8 {
		return ErrLength
	}
	n.FromSeq, n.ToSeq = be.Uint32(b), be.Uint32(b[4:])
	if n.FromSeq > n.ToSeq {
		return ErrBadRange
	}
	return nil
}

func (s SyncReq) appendTo(b []byte) ([]byte, error) { return be.AppendUint64(b, uint64(s.T1)), nil }
func (s *SyncReq) decode(b []byte) error {
	if len(b) != 8 {
		return ErrLength
	}
	s.T1 = int64(be.Uint64(b))
	return nil
}

func (s SyncResp) appendTo(b []byte) ([]byte, error) {
	b = be.AppendUint64(b, uint64(s.T1))
	b = be.AppendUint64(b, uint64(s.T2))
	return be.AppendUint64(b, uint64(s.T3)), nil
}

func (s *SyncResp) decode(b []byte) error {
	if len(b) != 24 {
		return ErrLength
	}
	s.T1 = int64(be.Uint64(b))
	s.T2 = int64(be.Uint64(b[8:]))
	s.T3 = int64(be.Uint64(b[16:]))
	return nil
}
