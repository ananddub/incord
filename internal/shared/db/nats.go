package db

import (
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/nats-io/nats.go"
)

func NewNATSClient(cfg config.NATSConfig) (*nats.Conn, error) {
	return nats.Connect(cfg.URL)
}
