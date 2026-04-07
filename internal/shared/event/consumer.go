package event

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

type Handler func(ctx context.Context, key, value []byte) error

type Consumer struct {
	client *kgo.Client
}

func NewConsumer(cfg config.RedpandaConfig, group string, topics []string) (*Consumer, error) {
	client, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create redpanda consumer: %w", err)
	}

	logger.Log.Info().Strs("brokers", cfg.Brokers).Str("group", group).Strs("topics", topics).Msg("connected to redpanda (consumer)")
	return &Consumer{client: client}, nil
}

func (c *Consumer) Start(ctx context.Context, handler Handler) error {
	for {
		fetches := c.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				logger.Log.Error().Err(e.Err).Str("topic", e.Topic).Msg("consumer fetch error")
			}
		}

		fetches.EachRecord(func(r *kgo.Record) {
			if err := handler(ctx, r.Key, r.Value); err != nil {
				logger.Log.Error().Err(err).Str("topic", r.Topic).Msg("failed to handle event")
			}
		})
	}
}

func (c *Consumer) Close() {
	c.client.Close()
}
