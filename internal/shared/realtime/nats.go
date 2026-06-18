package realtime

import (
	"context"
	"encoding/json"

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

func Publish[T any](live *LPubSub, subj string, data T) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return live.nc.Publish(subj, b)
}

func Subscribe[T any](live *LPubSub, ctx context.Context, subj string, handler func(data T)) error {
	sub, err := live.nc.Subscribe(subj, func(msg *nats.Msg) {
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
	return nil
}

func MultiSubscribe[T any](
	live *LPubSub,
	ctx context.Context,
	subjects []string,
	handler func(data T),
) error {
	subs := make([]*nats.Subscription, 0, len(subjects))
	for _, subject := range subjects {
		sub, err := live.nc.Subscribe(subject, func(msg *nats.Msg) {
			var value T
			if err := json.Unmarshal(msg.Data, &value); err != nil {
				return
			}
			handler(value)
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
