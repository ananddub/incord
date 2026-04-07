package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Messages
	MessagesSent = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ndiscord_messages_sent_total",
		Help: "Total messages sent",
	})
	MessagesDeleted = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ndiscord_messages_deleted_total",
		Help: "Total messages deleted",
	})

	// Active streams
	ActiveStreams = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "ndiscord_active_streams",
		Help: "Number of active streaming connections",
	}, []string{"type"}) // type: messages, typing, guild, friend, voice

	// Gateway sessions
	ActiveGatewaySessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ndiscord_gateway_sessions_active",
		Help: "Number of active gateway sessions",
	})

	// Events
	EventsPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ndiscord_events_published_total",
		Help: "Total events published to Redpanda",
	}, []string{"topic"})
	EventsConsumed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ndiscord_events_consumed_total",
		Help: "Total events consumed from Redpanda",
	}, []string{"topic"})

	// Users
	UsersRegistered = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ndiscord_users_registered_total",
		Help: "Total users registered",
	})
	ActiveUsers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ndiscord_users_online",
		Help: "Number of users currently online",
	})

	// Guilds
	GuildsCreated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "ndiscord_guilds_created_total",
		Help: "Total guilds created",
	})

	// Voice
	VoiceParticipants = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ndiscord_voice_participants",
		Help: "Number of users in voice channels",
	})

	// gRPC
	GrpcRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ndiscord_grpc_request_duration_seconds",
		Help:    "gRPC request duration in seconds",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method", "code"})
)
