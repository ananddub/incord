package user

import (
	"github.com/ananddub/ndiscord_backend/internal/features/channel"
	"github.com/ananddub/ndiscord_backend/internal/features/presence"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/minio/minio-go/v7"
	"go.uber.org/fx"
)

type dependencies struct {
	fx.In

	Config       config.MinIOConfig
	MinIO        *minio.Client `name:"minio"`
	MinIOSigner  *minio.Client `name:"minioSigner"`
	Service      *Service
	Handler      *Handler
	BlockChecker *BlockChecker
	DMResolver   *channel.DMResolver
	Presence     *presence.Service
}

func wireDependencies(p dependencies) {
	p.Service.SetStorage(p.MinIO, p.MinIOSigner, p.Config.Bucket)
	p.Service.SetDMOpener(p.DMResolver)
	p.Service.SetPresenceReader(p.Presence)
	p.Handler.SetBlockChecker(p.BlockChecker)
	p.Presence.SetUserResolver(p.Service)
}

var Module = fx.Module("user",
	fx.Provide(NewRepository, NewService, NewHandler, NewBlockChecker, NewFriendLookup),
	fx.Invoke(wireDependencies),
)
