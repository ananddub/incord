package udp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// PacketType identifies the kind of payload in a UDP packet.
type PacketType uint8

const (
	PacketTypeAudio     PacketType = 1
	PacketTypeVideo     PacketType = 2
	PacketTypeScreen    PacketType = 3
	PacketTypeHeartbeat PacketType = 4
	PacketTypeHandshake PacketType = 5
)

// HeaderSize is the total number of bytes in a packet header.
const HeaderSize = 14

// Header represents the fixed-size header of a voice UDP packet.
//
// Wire format (14 bytes, big-endian):
//
//	Offset  Size  Field
//	0       1     Version
//	1       1     Type (PacketType)
//	2       2     Sequence
//	4       4     Timestamp
//	8       4     SSRC
//	12      2     PayloadLen
type Header struct {
	Version    uint8
	Type       PacketType
	Sequence   uint16
	Timestamp  uint32
	SSRC       uint32
	PayloadLen uint16
}

var (
	ErrPacketTooShort  = errors.New("packet too short for header")
	ErrPayloadTruncated = errors.New("payload truncated: fewer bytes than header declares")
)

// Encode serializes the header into the first HeaderSize bytes of buf.
// buf must be at least HeaderSize bytes long.
func (h *Header) Encode(buf []byte) {
	buf[0] = h.Version
	buf[1] = uint8(h.Type)
	binary.BigEndian.PutUint16(buf[2:4], h.Sequence)
	binary.BigEndian.PutUint32(buf[4:8], h.Timestamp)
	binary.BigEndian.PutUint32(buf[8:12], h.SSRC)
	binary.BigEndian.PutUint16(buf[12:14], h.PayloadLen)
}

// DecodeHeader parses a Header from raw bytes.
// Returns the header and the payload slice, or an error.
func DecodeHeader(data []byte) (*Header, []byte, error) {
	if len(data) < HeaderSize {
		return nil, nil, ErrPacketTooShort
	}

	h := &Header{
		Version:    data[0],
		Type:       PacketType(data[1]),
		Sequence:   binary.BigEndian.Uint16(data[2:4]),
		Timestamp:  binary.BigEndian.Uint32(data[4:8]),
		SSRC:       binary.BigEndian.Uint32(data[8:12]),
		PayloadLen: binary.BigEndian.Uint16(data[12:14]),
	}

	end := HeaderSize + int(h.PayloadLen)
	if len(data) < end {
		return nil, nil, ErrPayloadTruncated
	}

	return h, data[HeaderSize:end], nil
}

// EncodePacket builds a complete packet (header + payload) and returns the byte slice.
func EncodePacket(h *Header, payload []byte) []byte {
	h.PayloadLen = uint16(len(payload))
	buf := make([]byte, HeaderSize+len(payload))
	h.Encode(buf)
	copy(buf[HeaderSize:], payload)
	return buf
}

// String returns a human-readable representation of the header.
func (h *Header) String() string {
	return fmt.Sprintf("Header{ver=%d type=%d seq=%d ts=%d ssrc=%d len=%d}",
		h.Version, h.Type, h.Sequence, h.Timestamp, h.SSRC, h.PayloadLen)
}
