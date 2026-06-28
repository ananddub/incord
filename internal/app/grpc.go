package app

import (
	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	mediav1 "github.com/ananddub/ndiscord_backend/gen/media/v1"
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	syncv1 "github.com/ananddub/ndiscord_backend/gen/sync/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// NewGRPCServer creates a gRPC server and registers every API service.
func NewGRPCServer(handlers *Handlers, interceptors middleware.GRPCInterceptors) *grpc.Server {
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(interceptors.Unary...),
		grpc.ChainStreamInterceptor(interceptors.Stream...),
	)
	reflection.Register(server)

	authv1.RegisterAuthServiceServer(server, handlers.Auth)
	userv1.RegisterUserServiceServer(server, handlers.User)
	guildv1.RegisterGuildServiceServer(server, handlers.Guild)
	channelv1.RegisterChannelServiceServer(server, handlers.Channel)
	messagev1.RegisterMessageServiceServer(server, handlers.Message)
	streamv1.RegisterStreamServiceServer(server, handlers.Stream)
	syncv1.RegisterSyncServiceServer(server, handlers.Sync)
	presencev1.RegisterPresenceServiceServer(server, handlers.Presence)
	mediav1.RegisterMediaServiceServer(server, handlers.Media)
	voicev1.RegisterVoiceServiceServer(server, handlers.Voice)

	return server
}
