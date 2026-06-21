package realtime

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

type LPubSub struct {
	nc    *nats.Conn
	redis *redis.Client
}

func NewLPubSub(nc *nats.Conn, redis *redis.Client) (*LPubSub, error) {
	return &LPubSub{nc: nc, redis: redis}, nil
}

func Publish[T any](lsp *LPubSub, subj string, data T) error {
	b, err := json.Marshal(&data)
	if err != nil {
		return err
	}
	logger.Log.Info().Str("pub:subject", subj).RawJSON("raw:data", b).Msg("nats publish")
	return lsp.nc.Publish(subj, b)
}

func Subscribe[T any](lsp *LPubSub, ctx context.Context, subj string, handler func(data T)) error {
	sub, err := lsp.nc.Subscribe(subj, func(msg *nats.Msg) {
		var value T
		if err := json.Unmarshal(msg.Data, &value); err != nil {
			return
		}
		handler(value)
	})
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()
	<-ctx.Done()
	return ctx.Err()
}

func to_string(subjects []string) string {
	return "nats:subscribe:" + strings.Join(subjects, ":")
}

func MultiSubscribe[T any](
	lps *LPubSub,
	ctx context.Context,
	subjects []string,
	isChache bool,
	handler func(data T),
) error {
	subs := make([]*nats.Subscription, 0, len(subjects))
	if isChache {
		data, err := lps.redis.Get(ctx, to_string(subjects)).Bytes()
		if err == nil {
			var value T
			if err := json.Unmarshal(data, &value); err == nil {
				handler(value)
			}
		}
	}
	for _, subject := range subjects {
		sub, err := lps.nc.Subscribe(subject, func(msg *nats.Msg) {
			var value T
			if err := json.Unmarshal(msg.Data, &value); err != nil {
				return
			}
			handler(value)

			if isChache {
				logger.Log.Info().RawJSON(subject, msg.Data)
				lps.redis.Set(ctx, to_string(subjects), msg.Data, 5*time.Minute)
			}
		})
		if err != nil {
			for _, s := range subs {
				_ = s.Unsubscribe()
			}
			return err
		}
		subs = append(subs, sub)
	}

	defer func() {
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
	}()

	<-ctx.Done()
	return ctx.Err()
}
