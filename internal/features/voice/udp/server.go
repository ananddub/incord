package udp

import (
	"fmt"
	"net"
	"sync"

	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

const (
	maxPacketSize = 1500 // Standard MTU
	writeChanSize = 256
)

// Participant represents a connected voice client tracked by the SFU.
type Participant struct {
	SSRC      uint32
	ChannelID string
	UserID    string
	Addr      *net.UDPAddr
	Send      chan []byte // outbound packet queue
}

// Server is a UDP server that implements a simple SFU (Selective Forwarding Unit).
// It receives voice packets from participants and forwards them to all other
// participants in the same channel.
type Server struct {
	addr string
	conn *net.UDPConn

	mu           sync.RWMutex
	participants map[uint32]*Participant            // SSRC -> Participant
	channels     map[string]map[uint32]*Participant // channel_id -> SSRC -> Participant

	done chan struct{}
}

// NewServer creates a new UDP voice server.
func NewServer(host string, port int) *Server {
	return &Server{
		addr:         fmt.Sprintf("%s:%d", host, port),
		participants: make(map[uint32]*Participant),
		channels:     make(map[string]map[uint32]*Participant),
		done:         make(chan struct{}),
	}
}

// RegisterParticipant adds a participant to the SFU routing table.
// This should be called after a user successfully joins a voice channel via gRPC.
func (s *Server) RegisterParticipant(ssrc uint32, channelID, userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := &Participant{
		SSRC:      ssrc,
		ChannelID: channelID,
		UserID:    userID,
		Send:      make(chan []byte, writeChanSize),
	}

	s.participants[ssrc] = p

	if s.channels[channelID] == nil {
		s.channels[channelID] = make(map[uint32]*Participant)
	}
	s.channels[channelID][ssrc] = p

	logger.Log.Info().
		Uint32("ssrc", ssrc).
		Str("channel_id", channelID).
		Str("user_id", userID).
		Msg("participant registered in UDP server")
}

// UnregisterParticipant removes a participant from the SFU routing table.
func (s *Server) UnregisterParticipant(ssrc uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.participants[ssrc]
	if !ok {
		return
	}

	close(p.Send)
	delete(s.participants, ssrc)

	if ch, exists := s.channels[p.ChannelID]; exists {
		delete(ch, ssrc)
		if len(ch) == 0 {
			delete(s.channels, p.ChannelID)
		}
	}

	logger.Log.Info().Uint32("ssrc", ssrc).Msg("participant unregistered from UDP server")
}

// Start begins listening for UDP packets and processing them.
func (s *Server) Start() error {
	udpAddr, err := net.ResolveUDPAddr("udp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP: %w", err)
	}
	s.conn = conn

	logger.Log.Info().Str("addr", s.addr).Msg("UDP voice server started")

	// Start the reader goroutine
	go s.readLoop()

	return nil
}

// Stop shuts down the UDP server gracefully.
func (s *Server) Stop() {
	close(s.done)
	if s.conn != nil {
		s.conn.Close()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for ssrc, p := range s.participants {
		close(p.Send)
		delete(s.participants, ssrc)
	}
	s.channels = make(map[string]map[uint32]*Participant)

	logger.Log.Info().Msg("UDP voice server stopped")
}

// readLoop continuously reads packets from the UDP connection and routes them.
func (s *Server) readLoop() {
	buf := make([]byte, maxPacketSize)

	for {
		select {
		case <-s.done:
			return
		default:
		}

		n, remoteAddr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				logger.Log.Error().Err(err).Msg("error reading UDP packet")
				continue
			}
		}

		if n < HeaderSize {
			continue
		}

		header, payload, err := DecodeHeader(buf[:n])
		if err != nil {
			logger.Log.Debug().Err(err).Msg("failed to decode UDP packet header")
			continue
		}

		s.handlePacket(header, payload, remoteAddr)
	}
}

// handlePacket processes a single received UDP packet.
func (s *Server) handlePacket(header *Header, payload []byte, remoteAddr *net.UDPAddr) {
	s.mu.RLock()
	sender, ok := s.participants[header.SSRC]
	s.mu.RUnlock()

	if !ok {
		// Unknown SSRC -- could be a handshake from a new participant
		if header.Type == PacketTypeHandshake {
			logger.Log.Debug().
				Uint32("ssrc", header.SSRC).
				Str("addr", remoteAddr.String()).
				Msg("handshake from unknown SSRC, waiting for registration")
		}
		return
	}

	// Update the sender's address if it changed (NAT traversal / roaming)
	if sender.Addr == nil || !sender.Addr.IP.Equal(remoteAddr.IP) || sender.Addr.Port != remoteAddr.Port {
		s.mu.Lock()
		sender.Addr = remoteAddr
		s.mu.Unlock()

		// Start a writer goroutine for this participant if this is the first packet
		go s.writeLoop(sender)
	}

	switch header.Type {
	case PacketTypeHeartbeat:
		// Respond with the same heartbeat packet
		s.sendTo(sender, EncodePacket(header, payload))

	case PacketTypeHandshake:
		// Echo back the handshake to confirm connectivity
		s.sendTo(sender, EncodePacket(header, payload))

	case PacketTypeAudio, PacketTypeVideo, PacketTypeScreen:
		// SFU: forward to all other participants in the same channel
		s.forwardToChannel(sender, header, payload)
	}
}

// forwardToChannel sends a packet to all participants in the same channel except the sender.
func (s *Server) forwardToChannel(sender *Participant, header *Header, payload []byte) {
	s.mu.RLock()
	channelParticipants, ok := s.channels[sender.ChannelID]
	if !ok {
		s.mu.RUnlock()
		return
	}

	// Build the packet once, share it across sends
	packet := EncodePacket(header, payload)

	for ssrc, p := range channelParticipants {
		if ssrc == sender.SSRC {
			continue // skip sender
		}
		if p.Addr == nil {
			continue // participant hasn't sent any packet yet
		}
		select {
		case p.Send <- packet:
		default:
			// Drop packet if the outbound buffer is full
			logger.Log.Debug().
				Uint32("ssrc", p.SSRC).
				Msg("dropping packet, send buffer full")
		}
	}
	s.mu.RUnlock()
}

// sendTo queues a packet for a specific participant.
func (s *Server) sendTo(p *Participant, packet []byte) {
	select {
	case p.Send <- packet:
	default:
	}
}

// writeLoop drains the participant's Send channel and writes packets to the network.
func (s *Server) writeLoop(p *Participant) {
	for packet := range p.Send {
		if p.Addr == nil {
			continue
		}
		if _, err := s.conn.WriteToUDP(packet, p.Addr); err != nil {
			select {
			case <-s.done:
				return
			default:
				logger.Log.Debug().
					Err(err).
					Uint32("ssrc", p.SSRC).
					Msg("error writing UDP packet")
			}
		}
	}
}
