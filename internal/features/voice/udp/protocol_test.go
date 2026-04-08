package udp

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	h := &Header{
		Version:   1,
		Type:      PacketTypeAudio,
		Sequence:  100,
		Timestamp: 123456,
		SSRC:      42,
	}
	payload := []byte("opus-audio-frame")

	pkt := EncodePacket(h, payload)

	decoded, data, err := DecodeHeader(pkt)
	require.NoError(t, err)

	assert.Equal(t, h.Version, decoded.Version)
	assert.Equal(t, h.Type, decoded.Type)
	assert.Equal(t, h.Sequence, decoded.Sequence)
	assert.Equal(t, h.Timestamp, decoded.Timestamp)
	assert.Equal(t, h.SSRC, decoded.SSRC)
	assert.Equal(t, uint16(len(payload)), decoded.PayloadLen)
	assert.Equal(t, payload, data)
}

func TestAllPacketTypes(t *testing.T) {
	types := []PacketType{
		PacketTypeAudio,
		PacketTypeVideo,
		PacketTypeScreen,
		PacketTypeHeartbeat,
		PacketTypeHandshake,
	}

	for _, pt := range types {
		h := &Header{
			Version:   1,
			Type:      pt,
			Sequence:  1,
			Timestamp: 1,
			SSRC:      1,
		}
		pkt := EncodePacket(h, []byte{0xAA})

		decoded, data, err := DecodeHeader(pkt)
		require.NoError(t, err, "packet type %d", pt)
		assert.Equal(t, pt, decoded.Type)
		assert.Equal(t, []byte{0xAA}, data)
	}
}

func TestMaxValues(t *testing.T) {
	h := &Header{
		Version:   255,
		Type:      PacketType(255),
		Sequence:  math.MaxUint16,
		Timestamp: math.MaxUint32,
		SSRC:      math.MaxUint32,
	}
	payload := make([]byte, 1000)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	pkt := EncodePacket(h, payload)
	decoded, data, err := DecodeHeader(pkt)
	require.NoError(t, err)

	assert.Equal(t, uint8(255), decoded.Version)
	assert.Equal(t, PacketType(255), decoded.Type)
	assert.Equal(t, uint16(math.MaxUint16), decoded.Sequence)
	assert.Equal(t, uint32(math.MaxUint32), decoded.Timestamp)
	assert.Equal(t, uint32(math.MaxUint32), decoded.SSRC)
	assert.Equal(t, payload, data)
}

func TestEmptyPayload(t *testing.T) {
	h := &Header{
		Version:   1,
		Type:      PacketTypeHeartbeat,
		Sequence:  0,
		Timestamp: 0,
		SSRC:      0,
	}

	pkt := EncodePacket(h, nil)
	assert.Len(t, pkt, HeaderSize)

	decoded, data, err := DecodeHeader(pkt)
	require.NoError(t, err)
	assert.Equal(t, uint16(0), decoded.PayloadLen)
	assert.Empty(t, data)
}

func TestDecodeHeader_TooShort(t *testing.T) {
	short := make([]byte, HeaderSize-1)
	_, _, err := DecodeHeader(short)
	assert.ErrorIs(t, err, ErrPacketTooShort)
}

func TestDecodeHeader_PayloadTruncated(t *testing.T) {
	h := &Header{
		Version:    1,
		Type:       PacketTypeAudio,
		Sequence:   1,
		Timestamp:  1,
		SSRC:       1,
		PayloadLen: 100, // claims 100 bytes of payload
	}

	// Build a buffer that has the header but only 5 bytes of payload.
	buf := make([]byte, HeaderSize+5)
	h.Encode(buf)

	_, _, err := DecodeHeader(buf)
	assert.ErrorIs(t, err, ErrPayloadTruncated)
}

func TestHeaderString(t *testing.T) {
	h := &Header{
		Version:    1,
		Type:       PacketTypeAudio,
		Sequence:   10,
		Timestamp:  20,
		SSRC:       30,
		PayloadLen: 40,
	}
	s := h.String()
	assert.Contains(t, s, "ver=1")
	assert.Contains(t, s, "type=1")
	assert.Contains(t, s, "seq=10")
	assert.Contains(t, s, "ts=20")
	assert.Contains(t, s, "ssrc=30")
	assert.Contains(t, s, "len=40")
}
