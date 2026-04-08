package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

func main() {
	cfg := config.Load()
	logger.Init("debug")
	log := logger.Log

	log.Info().Msg("starting ndiscord voice server")

	addr := fmt.Sprintf("%s:%d", cfg.Voice.UDPHost, cfg.Voice.UDPPort)
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", addr).Msg("failed to listen on UDP")
	}
	defer conn.Close()

	log.Info().Str("addr", addr).Msg("UDP voice server listening")

	// TODO: Start voice session manager
	// TODO: Start packet router goroutines

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down voice server")
}
