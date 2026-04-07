package event

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

type Producer struct {
	client *kgo.Client
}

func NewProducer(cfg config.RedpandaConfig) (*Producer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create redpanda producer: %w", err)
	}

	logger.Log.Info().Strs("brokers", cfg.Brokers).Msg("connected to redpanda (producer)")
	return &Producer{client: client}, nil
}

func (p *Producer) Publish(ctx context.Context, topic string, key string, value any) error {
	if p == nil || p.client == nil {
		return nil
	}

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	record := &kgo.Record{
		Topic: topic,
		Key:   []byte(key),
		Value: data,
	}

	// Use background context - request context cancels too early for async produce
	p.client.Produce(context.Background(), record, func(r *kgo.Record, err error) {
		if err != nil {
			logger.Log.Error().Err(err).Str("topic", topic).Msg("failed to produce event")
		}
	})
	return nil
}

func (p *Producer) Close() {
	if p == nil || p.client == nil {
		return
	}
	p.client.Close()
}
